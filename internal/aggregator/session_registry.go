package aggregator

import (
	"sync"
	"time"

	"muster/internal/oauth"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// ConnectionStatus represents the connection status of a session to a server.
type ConnectionStatus string

const (
	// StatusSessionConnected indicates the session is connected to the server.
	StatusSessionConnected ConnectionStatus = "connected"

	// StatusSessionPendingAuth indicates the session is waiting for authentication.
	StatusSessionPendingAuth ConnectionStatus = "pending_auth"

	// StatusSessionFailed indicates the session failed to connect to the server.
	StatusSessionFailed ConnectionStatus = "failed"
)

// SessionRegistry manages per-session state for OAuth-protected MCP servers.
//
// The registry maintains a thread-safe mapping of session IDs to their state,
// including per-server connections, cached tools, and authentication status.
// This enables session-scoped tool visibility where each user only sees tools
// from servers they have authenticated with.
//
// Key responsibilities:
//   - Session lifecycle management (creation, cleanup, timeout)
//   - Per-session connection tracking for OAuth-protected servers
//   - Session-specific tool caching
//   - Thread-safe access to session state
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*SessionState // sessionID -> state

	// Configuration
	sessionTimeout time.Duration // Duration after which idle sessions are cleaned up
	stopCleanup    chan struct{}
}

// SessionState holds per-session connection state.
//
// Each session maintains its own view of available tools based on which
// OAuth-protected servers the user has authenticated with. This state
// includes connection tracking, cached capabilities, and activity timestamps.
type SessionState struct {
	SessionID    string
	CreatedAt    time.Time
	LastActivity time.Time

	mu sync.RWMutex

	// Per-server connection state for this session
	// Maps server name to the session's connection to that server
	Connections map[string]*SessionConnection
}

// SessionConnection represents a session's connection to a specific server.
//
// For OAuth-protected servers, each session needs its own MCP client connection
// because:
//   - OAuth tokens are user-specific and may grant different permissions
//   - The remote MCP server may expose different tools based on user identity
//   - Connection state (ping, health) is per-user
type SessionConnection struct {
	ServerName  string
	Status      ConnectionStatus
	Client      MCPClient        // Session-specific MCP client (with user's token)
	TokenKey    *oauth.TokenKey  // Reference to the token in the token store
	ConnectedAt time.Time        // When the connection was established

	// Cached capabilities for this session's connection
	// These may differ from other sessions if the server returns different
	// tools based on user permissions
	mu        sync.RWMutex
	Tools     []mcp.Tool
	Resources []mcp.Resource
	Prompts   []mcp.Prompt
}

// NewSessionRegistry creates a new session registry with the specified timeout.
//
// The registry starts a background goroutine for periodic cleanup of idle sessions.
// Callers MUST call Stop() when done to prevent goroutine leaks.
//
// Args:
//   - sessionTimeout: Duration after which idle sessions are cleaned up (default: 30 minutes)
//
// Returns a new session registry ready for use.
func NewSessionRegistry(sessionTimeout time.Duration) *SessionRegistry {
	if sessionTimeout <= 0 {
		sessionTimeout = 30 * time.Minute
	}

	sr := &SessionRegistry{
		sessions:       make(map[string]*SessionState),
		sessionTimeout: sessionTimeout,
		stopCleanup:    make(chan struct{}),
	}

	// Start background cleanup goroutine
	go sr.cleanupLoop()

	return sr
}

// GetOrCreateSession returns the session state for a session ID, creating it if necessary.
//
// This method is the primary entry point for session management. It ensures that
// a session always exists when needed and updates the last activity timestamp.
//
// Args:
//   - sessionID: Unique identifier for the session
//
// Returns the session state (never nil).
func (sr *SessionRegistry) GetOrCreateSession(sessionID string) *SessionState {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	session, exists := sr.sessions[sessionID]
	if !exists {
		session = &SessionState{
			SessionID:    sessionID,
			CreatedAt:    time.Now(),
			LastActivity: time.Now(),
			Connections:  make(map[string]*SessionConnection),
		}
		sr.sessions[sessionID] = session
		logging.Debug("SessionRegistry", "Created new session: %s", logging.TruncateSessionID(sessionID))
	} else {
		session.UpdateActivity()
	}

	return session
}

// GetSession returns the session state for a session ID.
//
// Args:
//   - sessionID: Unique identifier for the session
//
// Returns the session state and true if found, nil and false otherwise.
func (sr *SessionRegistry) GetSession(sessionID string) (*SessionState, bool) {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	session, exists := sr.sessions[sessionID]
	if exists {
		session.UpdateActivity()
	}
	return session, exists
}

// DeleteSession removes a session and cleans up its connections.
//
// This method closes all session-specific MCP client connections and removes
// the session state from the registry.
//
// Args:
//   - sessionID: Unique identifier for the session to remove
func (sr *SessionRegistry) DeleteSession(sessionID string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	session, exists := sr.sessions[sessionID]
	if !exists {
		return
	}

	// Close all session-specific client connections
	session.CloseAllConnections()

	delete(sr.sessions, sessionID)
	logging.Debug("SessionRegistry", "Deleted session: %s", logging.TruncateSessionID(sessionID))
}

// GetConnection returns a session's connection to a specific server.
//
// Args:
//   - sessionID: Unique identifier for the session
//   - serverName: Name of the server
//
// Returns the connection and true if found, nil and false otherwise.
func (sr *SessionRegistry) GetConnection(sessionID, serverName string) (*SessionConnection, bool) {
	session, exists := sr.GetSession(sessionID)
	if !exists {
		return nil, false
	}

	return session.GetConnection(serverName)
}

// SetConnection sets a session's connection to a specific server.
//
// Args:
//   - sessionID: Unique identifier for the session
//   - serverName: Name of the server
//   - conn: The connection to set
func (sr *SessionRegistry) SetConnection(sessionID, serverName string, conn *SessionConnection) {
	session := sr.GetOrCreateSession(sessionID)
	session.SetConnection(serverName, conn)
}

// SetPendingAuth marks a server as pending authentication for a session.
//
// This creates a placeholder connection in pending_auth state that will be
// upgraded to connected once authentication succeeds.
//
// Args:
//   - sessionID: Unique identifier for the session
//   - serverName: Name of the server
func (sr *SessionRegistry) SetPendingAuth(sessionID, serverName string) {
	session := sr.GetOrCreateSession(sessionID)
	session.SetConnection(serverName, &SessionConnection{
		ServerName: serverName,
		Status:     StatusSessionPendingAuth,
	})
}

// UpgradeConnection upgrades a session's connection from pending_auth to connected.
//
// This is called after successful OAuth authentication to associate the
// authenticated MCP client with the session.
//
// Args:
//   - sessionID: Unique identifier for the session
//   - serverName: Name of the server
//   - client: The authenticated MCP client
//   - tokenKey: Reference to the token in the token store
//
// Returns an error if the session or connection is not found.
func (sr *SessionRegistry) UpgradeConnection(sessionID, serverName string, client MCPClient, tokenKey *oauth.TokenKey) error {
	session, exists := sr.GetSession(sessionID)
	if !exists {
		return &SessionNotFoundError{SessionID: sessionID}
	}

	return session.UpgradeConnection(serverName, client, tokenKey)
}

// GetAllSessions returns all active sessions.
//
// Returns a map of session IDs to session states. The returned map is a copy
// to prevent external modifications to the internal state.
func (sr *SessionRegistry) GetAllSessions() map[string]*SessionState {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	result := make(map[string]*SessionState, len(sr.sessions))
	for k, v := range sr.sessions {
		result[k] = v
	}
	return result
}

// Count returns the number of active sessions.
func (sr *SessionRegistry) Count() int {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	return len(sr.sessions)
}

// Stop stops the session registry and cleans up all sessions.
//
// This method closes all session-specific MCP client connections and stops
// the background cleanup goroutine.
func (sr *SessionRegistry) Stop() {
	close(sr.stopCleanup)

	sr.mu.Lock()
	defer sr.mu.Unlock()

	for _, session := range sr.sessions {
		session.CloseAllConnections()
	}
	sr.sessions = make(map[string]*SessionState)

	logging.Debug("SessionRegistry", "Session registry stopped")
}

// minCleanupInterval is the minimum interval between cleanup runs.
// This prevents excessive cleanup frequency when sessionTimeout is very short.
const minCleanupInterval = time.Second

// cleanupLoop periodically removes idle sessions from the registry.
func (sr *SessionRegistry) cleanupLoop() {
	cleanupInterval := sr.sessionTimeout / 2
	if cleanupInterval < minCleanupInterval {
		cleanupInterval = minCleanupInterval
	}
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sr.cleanup()
		case <-sr.stopCleanup:
			return
		}
	}
}

// cleanup removes all idle sessions from the registry.
func (sr *SessionRegistry) cleanup() {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	now := time.Now()
	count := 0

	for sessionID, session := range sr.sessions {
		session.mu.RLock()
		lastActivity := session.LastActivity
		session.mu.RUnlock()

		if now.Sub(lastActivity) > sr.sessionTimeout {
			session.CloseAllConnections()
			delete(sr.sessions, sessionID)
			count++
		}
	}

	if count > 0 {
		logging.Debug("SessionRegistry", "Cleaned up %d idle sessions", count)
	}
}

// UpdateActivity updates the last activity timestamp for the session.
func (s *SessionState) UpdateActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastActivity = time.Now()
}

// GetConnection returns the session's connection to a specific server.
func (s *SessionState) GetConnection(serverName string) (*SessionConnection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	conn, exists := s.Connections[serverName]
	return conn, exists
}

// SetConnection sets the session's connection to a specific server.
func (s *SessionState) SetConnection(serverName string, conn *SessionConnection) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Connections[serverName] = conn
	s.LastActivity = time.Now()
}

// UpgradeConnection upgrades a connection from pending_auth to connected.
func (s *SessionState) UpgradeConnection(serverName string, client MCPClient, tokenKey *oauth.TokenKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	conn, exists := s.Connections[serverName]
	if !exists {
		return &ConnectionNotFoundError{ServerName: serverName}
	}

	conn.Client = client
	conn.Status = StatusSessionConnected
	conn.TokenKey = tokenKey
	conn.ConnectedAt = time.Now()
	s.LastActivity = time.Now()

	logging.Debug("SessionRegistry", "Upgraded connection for session=%s server=%s",
		logging.TruncateSessionID(s.SessionID), serverName)

	return nil
}

// CloseAllConnections closes all MCP client connections for this session.
func (s *SessionState) CloseAllConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for serverName, conn := range s.Connections {
		if conn.Client != nil {
			if err := conn.Client.Close(); err != nil {
			logging.Warn("SessionRegistry", "Error closing client for session=%s server=%s: %v",
				logging.TruncateSessionID(s.SessionID), serverName, err)
			}
		}
	}

	s.Connections = make(map[string]*SessionConnection)
}

// GetConnectedServers returns the names of all servers the session is connected to.
func (s *SessionState) GetConnectedServers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var servers []string
	for name, conn := range s.Connections {
		if conn.Status == StatusSessionConnected {
			servers = append(servers, name)
		}
	}
	return servers
}

// GetPendingAuthServers returns the names of all servers awaiting authentication.
func (s *SessionState) GetPendingAuthServers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var servers []string
	for name, conn := range s.Connections {
		if conn.Status == StatusSessionPendingAuth {
			servers = append(servers, name)
		}
	}
	return servers
}

// UpdateTools updates the cached tools for a session connection.
func (sc *SessionConnection) UpdateTools(tools []mcp.Tool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.Tools = tools
}

// UpdateResources updates the cached resources for a session connection.
func (sc *SessionConnection) UpdateResources(resources []mcp.Resource) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.Resources = resources
}

// UpdatePrompts updates the cached prompts for a session connection.
func (sc *SessionConnection) UpdatePrompts(prompts []mcp.Prompt) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.Prompts = prompts
}

// GetTools returns the cached tools for a session connection.
func (sc *SessionConnection) GetTools() []mcp.Tool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.Tools
}

// GetResources returns the cached resources for a session connection.
func (sc *SessionConnection) GetResources() []mcp.Resource {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.Resources
}

// GetPrompts returns the cached prompts for a session connection.
func (sc *SessionConnection) GetPrompts() []mcp.Prompt {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.Prompts
}

// SessionNotFoundError is returned when a session is not found.
type SessionNotFoundError struct {
	SessionID string
}

func (e *SessionNotFoundError) Error() string {
	return "session not found: " + logging.TruncateSessionID(e.SessionID)
}

// ConnectionNotFoundError is returned when a connection is not found.
type ConnectionNotFoundError struct {
	ServerName string
}

func (e *ConnectionNotFoundError) Error() string {
	return "connection not found: " + e.ServerName
}

