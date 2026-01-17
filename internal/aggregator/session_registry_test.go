package aggregator

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"muster/internal/oauth"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestNewSessionRegistry(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	if sr == nil {
		t.Fatal("Expected non-nil session registry")
	}

	if sr.Count() != 0 {
		t.Errorf("Expected 0 sessions, got %d", sr.Count())
	}
}

func TestSessionRegistry_GetOrCreateSession(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	sessionID := "test-session-123"

	// Create session
	session := sr.GetOrCreateSession(sessionID)
	if session == nil {
		t.Fatal("Expected non-nil session")
	}
	if session.SessionID != sessionID {
		t.Errorf("Expected session ID %s, got %s", sessionID, session.SessionID)
	}
	if sr.Count() != 1 {
		t.Errorf("Expected 1 session, got %d", sr.Count())
	}

	// Get existing session (should not create new)
	session2 := sr.GetOrCreateSession(sessionID)
	if session2 != session {
		t.Error("Expected same session instance")
	}
	if sr.Count() != 1 {
		t.Errorf("Expected still 1 session, got %d", sr.Count())
	}
}

func TestSessionRegistry_GetSession(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	sessionID := "test-session-456"

	// Session doesn't exist
	session, exists := sr.GetSession(sessionID)
	if exists {
		t.Error("Expected session to not exist")
	}
	if session != nil {
		t.Error("Expected nil session")
	}

	// Create and get session
	sr.GetOrCreateSession(sessionID)
	session, exists = sr.GetSession(sessionID)
	if !exists {
		t.Error("Expected session to exist")
	}
	if session == nil {
		t.Error("Expected non-nil session")
	}
}

func TestSessionRegistry_DeleteSession(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	sessionID := "test-session-789"

	// Create session with a connection (using nil client for simplicity)
	session := sr.GetOrCreateSession(sessionID)
	session.SetConnection("test-server", &SessionConnection{
		ServerName: "test-server",
		Status:     StatusSessionConnected,
		Client:     nil, // No mock client needed for this test
	})

	if sr.Count() != 1 {
		t.Errorf("Expected 1 session, got %d", sr.Count())
	}

	// Delete session
	sr.DeleteSession(sessionID)

	if sr.Count() != 0 {
		t.Errorf("Expected 0 sessions, got %d", sr.Count())
	}

	// Session should no longer exist
	_, exists := sr.GetSession(sessionID)
	if exists {
		t.Error("Expected session to not exist after deletion")
	}
}

func TestSessionRegistry_SetConnection(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	sessionID := "test-session-conn"
	serverName := "test-server"

	// Set connection (should create session automatically)
	conn := &SessionConnection{
		ServerName: serverName,
		Status:     StatusSessionConnected,
	}
	sr.SetConnection(sessionID, serverName, conn)

	if sr.Count() != 1 {
		t.Errorf("Expected 1 session, got %d", sr.Count())
	}

	// Get connection
	retrievedConn, exists := sr.GetConnection(sessionID, serverName)
	if !exists {
		t.Error("Expected connection to exist")
	}
	if retrievedConn.ServerName != serverName {
		t.Errorf("Expected server name %s, got %s", serverName, retrievedConn.ServerName)
	}
}

func TestSessionRegistry_SetPendingAuth(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	sessionID := "test-session-auth"
	serverName := "oauth-server"

	sr.SetPendingAuth(sessionID, serverName)

	conn, exists := sr.GetConnection(sessionID, serverName)
	if !exists {
		t.Error("Expected connection to exist")
	}
	if conn.Status != StatusSessionPendingAuth {
		t.Errorf("Expected status %s, got %s", StatusSessionPendingAuth, conn.Status)
	}
}

func TestSessionRegistry_UpgradeConnection(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	sessionID := "test-session-upgrade"
	serverName := "oauth-server"

	// Set pending auth first
	sr.SetPendingAuth(sessionID, serverName)

	// Upgrade connection with nil client for simplicity
	tokenKey := &oauth.TokenKey{
		SessionID: sessionID,
		Issuer:    "https://auth.example.com",
		Scope:     "openid",
	}

	err := sr.UpgradeConnection(sessionID, serverName, nil, tokenKey)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	conn, exists := sr.GetConnection(sessionID, serverName)
	if !exists {
		t.Error("Expected connection to exist")
	}
	if conn.Status != StatusSessionConnected {
		t.Errorf("Expected status %s, got %s", StatusSessionConnected, conn.Status)
	}
	if conn.TokenKey != tokenKey {
		t.Error("Expected token key to be set")
	}
}

func TestSessionRegistry_UpgradeConnection_SessionNotFound(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	err := sr.UpgradeConnection("nonexistent", "server", nil, nil)
	if err == nil {
		t.Error("Expected error for nonexistent session")
	}

	if _, ok := err.(*SessionNotFoundError); !ok {
		t.Errorf("Expected SessionNotFoundError, got %T", err)
	}
}

func TestSessionState_GetConnectedServers(t *testing.T) {
	session := &SessionState{
		SessionID:   "test-session",
		CreatedAt:   time.Now(),
		Connections: make(map[string]*SessionConnection),
	}

	// Add various connections
	session.SetConnection("server1", &SessionConnection{
		ServerName: "server1",
		Status:     StatusSessionConnected,
	})
	session.SetConnection("server2", &SessionConnection{
		ServerName: "server2",
		Status:     StatusSessionPendingAuth,
	})
	session.SetConnection("server3", &SessionConnection{
		ServerName: "server3",
		Status:     StatusSessionConnected,
	})

	connected := session.GetConnectedServers()
	if len(connected) != 2 {
		t.Errorf("Expected 2 connected servers, got %d", len(connected))
	}

	pendingAuth := session.GetPendingAuthServers()
	if len(pendingAuth) != 1 {
		t.Errorf("Expected 1 pending auth server, got %d", len(pendingAuth))
	}
}

func TestSessionConnection_UpdateTools(t *testing.T) {
	conn := &SessionConnection{
		ServerName: "test-server",
		Status:     StatusSessionConnected,
	}

	tools := []mcp.Tool{
		{Name: "tool1", Description: "First tool"},
		{Name: "tool2", Description: "Second tool"},
	}

	conn.UpdateTools(tools)

	retrievedTools := conn.GetTools()
	if len(retrievedTools) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(retrievedTools))
	}
}

func TestSessionRegistry_Cleanup(t *testing.T) {
	// Create registry with short timeout for testing
	// Note: minCleanupInterval is 1 second, so timeout must be >= 2 seconds
	// for cleanup to run at timeout/2 interval
	sr := NewSessionRegistry(2 * time.Second)
	defer sr.Stop()

	sessionID := "test-session-cleanup"

	session := sr.GetOrCreateSession(sessionID)
	session.SetConnection("server", &SessionConnection{
		ServerName: "server",
		Status:     StatusSessionConnected,
		Client:     nil, // No mock client needed
	})

	if sr.Count() != 1 {
		t.Errorf("Expected 1 session, got %d", sr.Count())
	}

	// Wait for cleanup to run (cleanup runs at timeout/2 = 1 second interval)
	// and for session to expire (2 second timeout)
	time.Sleep(3 * time.Second)

	// Session should be cleaned up due to inactivity
	if sr.Count() != 0 {
		t.Errorf("Expected 0 sessions after cleanup, got %d", sr.Count())
	}
}

func TestSessionRegistry_ActivityPreventsCleanup(t *testing.T) {
	// Create registry with timeout that's longer than minCleanupInterval
	sr := NewSessionRegistry(3 * time.Second)
	defer sr.Stop()

	sessionID := "test-session-activity"

	sr.GetOrCreateSession(sessionID)

	// Keep session active by repeatedly accessing it over 2 seconds
	// This should prevent cleanup from removing the session
	for i := 0; i < 4; i++ {
		time.Sleep(500 * time.Millisecond)
		sr.GetOrCreateSession(sessionID) // This updates LastActivity
	}

	// Session should still exist because we kept it active
	if sr.Count() != 1 {
		t.Errorf("Expected 1 session, got %d", sr.Count())
	}
}

func TestSessionNotFoundError(t *testing.T) {
	err := &SessionNotFoundError{SessionID: "test-session-12345678-abcd"}
	expected := "session not found: test-ses..."

	if err.Error() != expected {
		t.Errorf("Expected error message %q, got %q", expected, err.Error())
	}
}

func TestConnectionNotFoundError(t *testing.T) {
	err := &ConnectionNotFoundError{ServerName: "test-server"}
	expected := "connection not found: test-server"

	if err.Error() != expected {
		t.Errorf("Expected error message %q, got %q", expected, err.Error())
	}
}

func TestTruncateSessionID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"short", "short"},
		{"exacteig", "exacteig"},
		{"123456789", "12345678..."},
		{"abcdefghijklmnop", "abcdefgh..."},
	}

	for _, tt := range tests {
		result := logging.TruncateSessionID(tt.input)
		if result != tt.expected {
			t.Errorf("TruncateSessionID(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestSessionRegistry_ConcurrentAccess(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	var wg sync.WaitGroup
	numGoroutines := 100
	numSessions := 10

	// Test concurrent GetOrCreateSession and GetSession
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("session-%d", id%numSessions)

			// Mix of operations
			session := sr.GetOrCreateSession(sessionID)
			if session == nil {
				t.Errorf("GetOrCreateSession returned nil for %s", sessionID)
				return
			}

			_, _ = sr.GetSession(sessionID)

			// Set and get connections
			serverName := fmt.Sprintf("server-%d", id%3)
			sr.SetConnection(sessionID, serverName, &SessionConnection{
				ServerName: serverName,
				Status:     StatusSessionConnected,
			})

			_, _ = sr.GetConnection(sessionID, serverName)

			// Get connected servers
			_ = session.GetConnectedServers()
		}(i)
	}

	wg.Wait()

	// Verify no panics occurred and state is consistent
	if sr.Count() != numSessions {
		t.Errorf("Expected %d sessions, got %d", numSessions, sr.Count())
	}
}

func TestSessionRegistry_GetAllSessions(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	// Create multiple sessions
	sr.GetOrCreateSession("session1")
	sr.GetOrCreateSession("session2")
	sr.GetOrCreateSession("session3")

	sessions := sr.GetAllSessions()
	if len(sessions) != 3 {
		t.Errorf("Expected 3 sessions, got %d", len(sessions))
	}

	// Verify it's a copy (modifications don't affect internal state)
	delete(sessions, "session1")
	if sr.Count() != 3 {
		t.Errorf("Deleting from returned map should not affect registry, got %d sessions", sr.Count())
	}
}

func TestSessionConnection_UpdateResources(t *testing.T) {
	conn := &SessionConnection{
		ServerName: "test-server",
		Status:     StatusSessionConnected,
	}

	resources := []mcp.Resource{
		{URI: "resource1", Name: "First resource"},
		{URI: "resource2", Name: "Second resource"},
	}

	conn.UpdateResources(resources)

	retrievedResources := conn.GetResources()
	if len(retrievedResources) != 2 {
		t.Errorf("Expected 2 resources, got %d", len(retrievedResources))
	}
}

func TestSessionConnection_UpdatePrompts(t *testing.T) {
	conn := &SessionConnection{
		ServerName: "test-server",
		Status:     StatusSessionConnected,
	}

	prompts := []mcp.Prompt{
		{Name: "prompt1", Description: "First prompt"},
		{Name: "prompt2", Description: "Second prompt"},
	}

	conn.UpdatePrompts(prompts)

	retrievedPrompts := conn.GetPrompts()
	if len(retrievedPrompts) != 2 {
		t.Errorf("Expected 2 prompts, got %d", len(retrievedPrompts))
	}
}

// Security-related tests

func TestValidateSessionID(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantErr   bool
	}{
		{
			name:      "valid session ID",
			sessionID: "abc123-def456",
			wantErr:   false,
		},
		{
			name:      "empty session ID",
			sessionID: "",
			wantErr:   true,
		},
		{
			name:      "session ID at max length",
			sessionID: string(make([]byte, MaxSessionIDLength)),
			wantErr:   false,
		},
		{
			name:      "session ID exceeds max length",
			sessionID: string(make([]byte, MaxSessionIDLength+1)),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSessionID(tt.sessionID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSessionID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSessionRegistry_SessionLimit(t *testing.T) {
	// Create registry with small limit for testing
	sr := NewSessionRegistryWithLimits(5*time.Minute, 3)
	defer sr.Stop()

	// Create sessions up to limit
	for i := 0; i < 3; i++ {
		session, err := sr.GetOrCreateSessionWithError(fmt.Sprintf("session-%d", i))
		if err != nil {
			t.Fatalf("Unexpected error creating session %d: %v", i, err)
		}
		if session == nil {
			t.Fatalf("Expected non-nil session for session-%d", i)
		}
	}

	if sr.Count() != 3 {
		t.Errorf("Expected 3 sessions, got %d", sr.Count())
	}

	// Try to create one more - should fail
	session, err := sr.GetOrCreateSessionWithError("session-4")
	if err == nil {
		t.Error("Expected error when exceeding session limit")
	}
	if session != nil {
		t.Error("Expected nil session when limit exceeded")
	}

	// Verify it's the right error type
	if _, ok := err.(*SessionLimitExceededError); !ok {
		t.Errorf("Expected SessionLimitExceededError, got %T", err)
	}
}

func TestSessionRegistry_InvalidSessionID(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	// Test empty session ID
	session, err := sr.GetOrCreateSessionWithError("")
	if err == nil {
		t.Error("Expected error for empty session ID")
	}
	if session != nil {
		t.Error("Expected nil session for empty session ID")
	}

	// Verify it's the right error type
	if _, ok := err.(*InvalidSessionIDError); !ok {
		t.Errorf("Expected InvalidSessionIDError, got %T", err)
	}

	// Test excessively long session ID
	longID := string(make([]byte, MaxSessionIDLength+100))
	session, err = sr.GetOrCreateSessionWithError(longID)
	if err == nil {
		t.Error("Expected error for excessively long session ID")
	}
	if session != nil {
		t.Error("Expected nil session for excessively long session ID")
	}
}

func TestSessionRegistry_UpgradeAlreadyConnected(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	sessionID := "test-session-upgrade-twice"
	serverName := "oauth-server"

	// Set pending auth
	sr.SetPendingAuth(sessionID, serverName)

	// First upgrade should succeed
	err := sr.UpgradeConnection(sessionID, serverName, nil, nil)
	if err != nil {
		t.Fatalf("First upgrade should succeed: %v", err)
	}

	// Verify connection is connected
	conn, exists := sr.GetConnection(sessionID, serverName)
	if !exists {
		t.Fatal("Expected connection to exist")
	}
	if conn.Status != StatusSessionConnected {
		t.Errorf("Expected status connected, got %s", conn.Status)
	}

	// Second upgrade should fail
	err = sr.UpgradeConnection(sessionID, serverName, nil, nil)
	if err == nil {
		t.Error("Expected error when upgrading already connected session")
	}

	// Verify it's the right error type
	if _, ok := err.(*ConnectionAlreadyEstablishedError); !ok {
		t.Errorf("Expected ConnectionAlreadyEstablishedError, got %T", err)
	}
}

func TestConnectionAlreadyEstablishedError(t *testing.T) {
	err := &ConnectionAlreadyEstablishedError{ServerName: "test-server"}
	expected := "connection already established: test-server"

	if err.Error() != expected {
		t.Errorf("Expected error message %q, got %q", expected, err.Error())
	}
}

func TestInvalidSessionIDError(t *testing.T) {
	err := &InvalidSessionIDError{Reason: "too long"}
	expected := "invalid session ID: too long"

	if err.Error() != expected {
		t.Errorf("Expected error message %q, got %q", expected, err.Error())
	}
}

func TestSessionLimitExceededError(t *testing.T) {
	err := &SessionLimitExceededError{Limit: 100, Current: 100}
	expected := "session limit exceeded: 100/100 sessions"

	if err.Error() != expected {
		t.Errorf("Expected error message %q, got %q", expected, err.Error())
	}
}

func TestNewSessionRegistryWithLimits_Defaults(t *testing.T) {
	// Test that negative maxSessions defaults to DefaultMaxSessions
	sr := NewSessionRegistryWithLimits(5*time.Minute, -1)
	defer sr.Stop()

	if sr.maxSessions != DefaultMaxSessions {
		t.Errorf("Expected maxSessions to be %d, got %d", DefaultMaxSessions, sr.maxSessions)
	}

	// Test that 0 maxSessions means unlimited
	sr2 := NewSessionRegistryWithLimits(5*time.Minute, 0)
	defer sr2.Stop()

	if sr2.maxSessions != 0 {
		t.Errorf("Expected maxSessions to be 0, got %d", sr2.maxSessions)
	}

	// With 0 limit, we should be able to create many sessions
	for i := 0; i < 100; i++ {
		_, err := sr2.GetOrCreateSessionWithError(fmt.Sprintf("session-%d", i))
		if err != nil {
			t.Fatalf("Unexpected error with unlimited sessions: %v", err)
		}
	}
}

// TestRemoveServerFromAllSessions tests that stale session connections are
// properly cleaned up when an MCPServer is deregistered. This is critical for
// issue #233: Auth status shows stale server names after MCPServer rename.
func TestRemoveServerFromAllSessions(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	serverName := "old-server-name"

	// Create multiple sessions with connections to the same server
	session1 := sr.GetOrCreateSession("session-1")
	session1.SetConnection(serverName, &SessionConnection{
		ServerName: serverName,
		Status:     StatusSessionConnected,
		Client:     nil, // No mock client needed for this test
	})

	session2 := sr.GetOrCreateSession("session-2")
	session2.SetConnection(serverName, &SessionConnection{
		ServerName: serverName,
		Status:     StatusSessionConnected,
		Client:     nil,
	})

	// Also add a different server connection to session1
	session1.SetConnection("other-server", &SessionConnection{
		ServerName: "other-server",
		Status:     StatusSessionConnected,
		Client:     nil,
	})

	// Verify connections exist
	conn1, exists := session1.GetConnection(serverName)
	if !exists || conn1 == nil {
		t.Fatal("Expected session1 to have connection to old-server-name")
	}

	conn2, exists := session2.GetConnection(serverName)
	if !exists || conn2 == nil {
		t.Fatal("Expected session2 to have connection to old-server-name")
	}

	// Remove the server from all sessions (simulates MCPServer deregistration)
	removedCount := sr.RemoveServerFromAllSessions(serverName)

	// Verify correct count was returned
	if removedCount != 2 {
		t.Errorf("Expected 2 connections removed, got %d", removedCount)
	}

	// Verify connections are removed from both sessions
	_, exists = session1.GetConnection(serverName)
	if exists {
		t.Error("Expected session1's connection to old-server-name to be removed")
	}

	_, exists = session2.GetConnection(serverName)
	if exists {
		t.Error("Expected session2's connection to old-server-name to be removed")
	}

	// Verify other connections are NOT affected
	otherConn, exists := session1.GetConnection("other-server")
	if !exists || otherConn == nil {
		t.Error("Expected session1's connection to other-server to remain")
	}

	// Verify sessions still exist (only connections removed, not sessions)
	if sr.Count() != 2 {
		t.Errorf("Expected 2 sessions to remain, got %d", sr.Count())
	}
}

// TestRemoveServerFromAllSessions_NoConnections tests that RemoveServerFromAllSessions
// handles the case where no sessions have connections to the server.
func TestRemoveServerFromAllSessions_NoConnections(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	// Create a session with a connection to a different server
	session := sr.GetOrCreateSession("session-1")
	session.SetConnection("different-server", &SessionConnection{
		ServerName: "different-server",
		Status:     StatusSessionConnected,
		Client:     nil,
	})

	// Try to remove a server that no session is connected to
	removedCount := sr.RemoveServerFromAllSessions("nonexistent-server")

	// Should return 0 and not error
	if removedCount != 0 {
		t.Errorf("Expected 0 connections removed, got %d", removedCount)
	}

	// Verify original connection is still there
	conn, exists := session.GetConnection("different-server")
	if !exists || conn == nil {
		t.Error("Expected connection to different-server to remain")
	}
}

// TestRemoveServerFromAllSessions_EmptyRegistry tests that RemoveServerFromAllSessions
// handles an empty session registry gracefully.
func TestRemoveServerFromAllSessions_EmptyRegistry(t *testing.T) {
	sr := NewSessionRegistry(5 * time.Minute)
	defer sr.Stop()

	// Try to remove from empty registry
	removedCount := sr.RemoveServerFromAllSessions("any-server")

	if removedCount != 0 {
		t.Errorf("Expected 0 connections removed from empty registry, got %d", removedCount)
	}
}
