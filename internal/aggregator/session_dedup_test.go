package aggregator

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConcurrentStaleSessionID verifies that N concurrent requests with the
// same unknown/stale session ID create exactly one new session (not N).
func TestConcurrentStaleSessionID(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	a := &AggregatorServer{
		sessionRegistry: sr,
	}

	// Inner handler records the session ID it sees.
	type result struct {
		sessionID string
		status    int
	}
	const concurrency = 20
	results := make([]result, concurrency)

	staleID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee" // valid UUID v4, but unknown

	// Use a barrier so all goroutines start the handler at the same instant,
	// maximizing the chance they overlap inside sync.Once.Do.
	var ready sync.WaitGroup
	ready.Add(concurrency)
	gate := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(concurrency)

	handler := a.clientSessionIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()

			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set(api.ClientSessionIDHeader, staleID)
			w := httptest.NewRecorder()

			ready.Done()
			<-gate // wait for all goroutines to be ready

			handler.ServeHTTP(w, req)

			results[idx] = result{
				sessionID: w.Header().Get(api.ClientSessionIDHeader),
				status:    w.Code,
			}
		}(i)
	}

	ready.Wait()
	close(gate) // release all goroutines simultaneously
	wg.Wait()

	// All requests should succeed.
	for i, r := range results {
		assert.Equal(t, http.StatusOK, r.status, "request %d should succeed", i)
		assert.NotEmpty(t, r.sessionID, "request %d should have a session ID", i)
	}

	// All requests should get the SAME new session ID.
	firstID := results[0].sessionID
	for i, r := range results[1:] {
		assert.Equal(t, firstID, r.sessionID,
			"request %d should share the same session ID as request 0", i+1)
	}

	// The stale session ID should NOT be the new session ID.
	assert.NotEqual(t, staleID, firstID, "new session should have a different ID from the stale one")

	// Only ONE session should exist in the registry.
	assert.Equal(t, 1, sr.Count(), "exactly one session should be created")
}

// TestStaleSessionDedupEntryCleanup verifies that dedup entries are created
// during stale ID handling and removed by the periodic cleanup sweep.
func TestStaleSessionDedupEntryCleanup(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	a := &AggregatorServer{
		sessionRegistry: sr,
	}

	staleID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

	handler := a.clientSessionIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request with stale ID
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(api.ClientSessionIDHeader, staleID)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// The dedup entry should exist right after the request.
	_, exists := a.staleSessionDedup.Load(staleID)
	assert.True(t, exists, "dedup entry should exist immediately after request")
	assert.Equal(t, int64(1), a.staleSessionDedupCount.Load(), "counter should be 1")

	// Running cleanup before TTL should not remove the entry.
	a.cleanupStaleSessionDedup()
	_, exists = a.staleSessionDedup.Load(staleID)
	assert.True(t, exists, "entry should survive cleanup before TTL")

	// Manually set createdAt to the past to simulate TTL expiry.
	val, _ := a.staleSessionDedup.Load(staleID)
	entry := val.(*staleSessionEntry)
	entry.createdAt = time.Now().Add(-staleSessionDedupTTL - time.Second)

	// Now cleanup should remove the entry.
	a.cleanupStaleSessionDedup()
	_, exists = a.staleSessionDedup.Load(staleID)
	assert.False(t, exists, "entry should be removed after TTL")
	assert.Equal(t, int64(0), a.staleSessionDedupCount.Load(), "counter should be 0")
}

// TestStaleSessionDedupMaxEntries verifies that the dedup map enforces a size cap.
func TestStaleSessionDedupMaxEntries(t *testing.T) {
	sr := NewSessionRegistryWithLimits(5*time.Minute, 50000)
	defer sr.Stop()

	a := &AggregatorServer{
		sessionRegistry: sr,
	}

	// Fill the dedup map to the max
	a.staleSessionDedupCount.Store(staleSessionDedupMaxEntries)

	handler := a.clientSessionIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	staleID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(api.ClientSessionIDHeader, staleID)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should still succeed (bypasses dedup, creates session directly)
	assert.Equal(t, http.StatusOK, w.Code)
	newSessionID := w.Header().Get(api.ClientSessionIDHeader)
	assert.NotEmpty(t, newSessionID)

	// The dedup map should NOT have a new entry (cap was reached)
	_, exists := a.staleSessionDedup.Load(staleID)
	assert.False(t, exists, "dedup entry should not be created when map is full")
}

// TestTrackerCleanupOnSessionRemoval verifies that when a session is removed
// from the registry, the onSessionRemoved callback fires (which should clear
// the sessionInitTracker in production wiring).
func TestTrackerCleanupOnSessionRemoval(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	var removedIDs []string
	var mu sync.Mutex
	sr.SetOnSessionRemoved(func(sessionID string) {
		mu.Lock()
		removedIDs = append(removedIDs, sessionID)
		mu.Unlock()
	})

	// Create two sessions
	s1, err := sr.CreateSessionForSubject("user1")
	require.NoError(t, err)
	s2, err := sr.CreateSessionForSubject("user2")
	require.NoError(t, err)

	// Delete first session
	sr.DeleteSession(s1.SessionID)

	mu.Lock()
	assert.Contains(t, removedIDs, s1.SessionID, "removed callback should fire for deleted session")
	assert.NotContains(t, removedIDs, s2.SessionID, "removed callback should NOT fire for remaining session")
	mu.Unlock()
}

// TestGetOnSessionRemoved verifies that GetOnSessionRemoved returns the current callback.
func TestGetOnSessionRemoved(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	// Initially nil
	assert.Nil(t, sr.GetOnSessionRemoved())

	// Set a callback
	called := false
	sr.SetOnSessionRemoved(func(sessionID string) {
		called = true
	})

	fn := sr.GetOnSessionRemoved()
	require.NotNil(t, fn)

	// Call it and verify it's the same function
	fn("test")
	assert.True(t, called)
}

// TestChainedSessionRemovalCallbacks verifies that callbacks can be chained:
// read the old one, wrap it, set the new one.
func TestChainedSessionRemovalCallbacks(t *testing.T) {
	sr := NewSessionRegistryWithLimits(100*time.Millisecond, 100)
	defer sr.Stop()

	var callOrder []string
	var mu sync.Mutex

	// First callback
	sr.SetOnSessionRemoved(func(sessionID string) {
		mu.Lock()
		callOrder = append(callOrder, "first:"+sessionID)
		mu.Unlock()
	})

	// Chain a second callback
	prev := sr.GetOnSessionRemoved()
	sr.SetOnSessionRemoved(func(sessionID string) {
		prev(sessionID)
		mu.Lock()
		callOrder = append(callOrder, "second:"+sessionID)
		mu.Unlock()
	})

	session, err := sr.CreateSessionForSubject("user")
	require.NoError(t, err)

	sr.DeleteSession(session.SessionID)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"first:" + session.SessionID, "second:" + session.SessionID}, callOrder)
}

// TestSSOInitAfterSessionTimeout verifies that after a session times out and
// the callback fires, a subsequent request creates a new session and the
// callback fires for the removed session.
func TestSSOInitAfterSessionTimeout(t *testing.T) {
	// Use a very short timeout so cleanup happens quickly.
	// Note: minCleanupInterval is 1s, so we need to wait at least that long.
	sr := NewSessionRegistryWithLimits(50*time.Millisecond, 100)
	defer sr.Stop()

	var removedIDs sync.Map
	sr.SetOnSessionRemoved(func(sessionID string) {
		removedIDs.Store(sessionID, true)
	})

	// Create a session
	session, err := sr.CreateSessionForSubject("user")
	require.NoError(t, err)
	originalID := session.SessionID

	// Wait for the session to time out and the removal callback to fire.
	// We check the callback (not just Count()) because the callback runs
	// outside the registry lock and may lag behind session deletion.
	require.Eventually(t, func() bool {
		_, called := removedIDs.Load(originalID)
		return called
	}, 5*time.Second, 50*time.Millisecond, "onSessionRemoved should fire for timed-out session")

	assert.Equal(t, 0, sr.Count(), "session should be removed from registry")

	// Now create a new session (simulating the next request)
	newSession, err := sr.CreateSessionForSubject("user")
	require.NoError(t, err)
	assert.NotEqual(t, originalID, newSession.SessionID, "new session should have a different ID")
}
