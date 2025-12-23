package aggregator

import (
	"testing"
	"time"

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
	tokenKey := &TokenKey{
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
	// Create registry with very short timeout for testing
	sr := NewSessionRegistry(50 * time.Millisecond)
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

	// Wait for cleanup to run (cleanup runs at timeout/2 interval)
	time.Sleep(100 * time.Millisecond)

	// Session should be cleaned up due to inactivity
	if sr.Count() != 0 {
		t.Errorf("Expected 0 sessions after cleanup, got %d", sr.Count())
	}
}

func TestSessionRegistry_ActivityPreventsCleanup(t *testing.T) {
	// Create registry with short timeout
	sr := NewSessionRegistry(100 * time.Millisecond)
	defer sr.Stop()

	sessionID := "test-session-activity"

	sr.GetOrCreateSession(sessionID)

	// Keep session active
	for i := 0; i < 5; i++ {
		time.Sleep(30 * time.Millisecond)
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
		result := truncateSessionID(tt.input)
		if result != tt.expected {
			t.Errorf("truncateSessionID(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
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
