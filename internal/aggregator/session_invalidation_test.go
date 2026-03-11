package aggregator

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"
)

func TestHandleSessionInvalidation(t *testing.T) {
	t.Run("invalidates existing session", func(t *testing.T) {
		sr := NewSessionRegistry(5 * time.Minute)
		defer sr.Stop()

		a := &AggregatorServer{sessionRegistry: sr}

		// Create a session
		session, err := sr.CreateSessionForSubject("user@example.com")
		if err != nil {
			t.Fatalf("failed to create session: %v", err)
		}
		sessionID := session.SessionID

		if sr.Count() != 1 {
			t.Fatalf("expected 1 session, got %d", sr.Count())
		}

		// Send DELETE /session
		req := httptest.NewRequest(http.MethodDelete, "/session", nil)
		req.Header.Set(api.ClientSessionIDHeader, sessionID)
		w := httptest.NewRecorder()

		a.handleSessionInvalidation(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204, got %d", w.Code)
		}
		if sr.Count() != 0 {
			t.Errorf("expected 0 sessions after invalidation, got %d", sr.Count())
		}
	})

	t.Run("returns 204 for already absent session", func(t *testing.T) {
		sr := NewSessionRegistry(5 * time.Minute)
		defer sr.Stop()

		a := &AggregatorServer{sessionRegistry: sr}

		// Generate a valid UUID v4 session ID that doesn't exist in the registry
		sessionID, err := GenerateSessionID()
		if err != nil {
			t.Fatalf("failed to generate session ID: %v", err)
		}

		req := httptest.NewRequest(http.MethodDelete, "/session", nil)
		req.Header.Set(api.ClientSessionIDHeader, sessionID)
		w := httptest.NewRecorder()

		a.handleSessionInvalidation(w, req)

		// Should still return 204 (idempotent)
		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204 for absent session, got %d", w.Code)
		}
	})

	t.Run("returns 400 for missing session ID header", func(t *testing.T) {
		sr := NewSessionRegistry(5 * time.Minute)
		defer sr.Stop()

		a := &AggregatorServer{sessionRegistry: sr}

		req := httptest.NewRequest(http.MethodDelete, "/session", nil)
		w := httptest.NewRecorder()

		a.handleSessionInvalidation(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("returns 400 for malformed session ID", func(t *testing.T) {
		sr := NewSessionRegistry(5 * time.Minute)
		defer sr.Stop()

		a := &AggregatorServer{sessionRegistry: sr}

		req := httptest.NewRequest(http.MethodDelete, "/session", nil)
		req.Header.Set(api.ClientSessionIDHeader, "not-a-uuid")
		w := httptest.NewRecorder()

		a.handleSessionInvalidation(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400 for malformed session ID, got %d", w.Code)
		}
	})

	t.Run("closes session connections on invalidation", func(t *testing.T) {
		sr := NewSessionRegistry(5 * time.Minute)
		defer sr.Stop()

		a := &AggregatorServer{sessionRegistry: sr}

		// Create a session with a mock client connection
		session, err := sr.CreateSessionForSubject("user@example.com")
		if err != nil {
			t.Fatalf("failed to create session: %v", err)
		}

		mockClient := &mockMCPClient{initialized: true}
		session.SetConnection("test-server", &SessionConnection{
			ServerName: "test-server",
			Status:     StatusSessionConnected,
			Client:     mockClient,
		})

		req := httptest.NewRequest(http.MethodDelete, "/session", nil)
		req.Header.Set(api.ClientSessionIDHeader, session.SessionID)
		w := httptest.NewRecorder()

		a.handleSessionInvalidation(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204, got %d", w.Code)
		}
		if !mockClient.closed {
			t.Error("expected mock client to be closed after session invalidation")
		}
	})
}
