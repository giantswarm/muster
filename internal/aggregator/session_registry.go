package aggregator

import (
	"fmt"
	"sync"
	"time"

	"muster/internal/oauth"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// Session ID validation constants.
const (
	// MaxSessionIDLength is the maximum allowed length for session IDs.
	// This prevents memory exhaustion attacks using extremely long session IDs.
	MaxSessionIDLength = 256

	// DefaultMaxSessions is the default maximum number of concurrent sessions.
	// This provides DoS protection by limiting session creation.
	DefaultMaxSessions = 10000
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
//   - DoS protection via session limits and validation
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*SessionState // sessionID -> state

	// Configuration
	sessionTimeout time.Duration // Duration after which idle sessions are cleaned up
	maxSessions    int           // Maximum number of concurrent sessions (DoS protection)
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
	Client      MCPClient       // Session-specific MCP client (with user's token)
	TokenKey    *oauth.TokenKey // Reference to the token in the token store
	ConnectedAt time.Time       // When the connection was established

	// Cached capabilities for this session's connection
	// These may differ from other sessions if the server returns different
	// tools based on user permissions
	mu        sync.RWMutex
	Tools     []mcp.Tool
	Resources []mcp.Resource
	Prompts   []mcp.Prompt
}

// NewSessionRegistry creates a new session registry with the specified timeout
// and default session limits.
//
// The registry starts a background goroutine for periodic cleanup of idle sessions.
// Callers MUST call Stop() when done to prevent goroutine leaks.
//
// Args:
//   - sessionTimeout: Duration after which idle sessions are cleaned up (default: 30 minutes)
//
// Returns a new session registry ready for use.
func NewSessionRegistry(sessionTimeout time.Duration) *SessionRegistry {
	return NewSessionRegistryWithLimits(sessionTimeout, DefaultMaxSessions)
}

// NewSessionRegistryWithLimits creates a new session registry with custom limits.
//
// This constructor allows fine-grained control over session limits for
// different deployment scenarios (e.g., higher limits for enterprise deployments).
//
// Args:
//   - sessionTimeout: Duration after which idle sessions are cleaned up (default: 30 minutes)
//   - maxSessions: Maximum number of concurrent sessions (0 = unlimited, not recommended)
//
// Returns a new session registry ready for use.
func NewSessionRegistryWithLimits(sessionTimeout time.Duration, maxSessions int) *SessionRegistry {
	if sessionTimeout <= 0 {
		sessionTimeout = 30 * time.Minute
	}
	if maxSessions < 0 {
		maxSessions = DefaultMaxSessions
	}

	sr := &SessionRegistry{
		sessions:       make(map[string]*SessionState),
		sessionTimeout: sessionTimeout,
		maxSessions:    maxSessions,
		stopCleanup:    make(chan struct{}),
	}

	// Start background cleanup goroutine
	go sr.cleanupLoop()

	return sr
}

// ValidateSessionID checks if a session ID is valid.
//
// A valid session ID must be:
//   - Non-empty
//   - Not longer than MaxSessionIDLength (256 bytes)
//
// Returns an error describing the validation failure, or nil if valid.
func ValidateSessionID(sessionID string) error {
	if sessionID == "" {
		return &InvalidSessionIDError{Reason: "session ID cannot be empty"}
	}
	if len(sessionID) > MaxSessionIDLength {
		return &InvalidSessionIDError{Reason: fmt.Sprintf("session ID exceeds maximum length of %d", MaxSessionIDLength)}
	}
	return nil
}

// GetOrCreateSession returns the session state for a session ID, creating it if necessary.
//
// This method is the primary entry point for session management. It ensures that
// a session always exists when needed and updates the last activity timestamp.
//
// The method validates the session ID and enforces session limits:
//   - Empty or excessively long session IDs are rejected
//   - New sessions are rejected if the maximum session limit is reached
//
// Args:
//   - sessionID: Unique identifier for the session
//
// Returns the session state, or nil if validation fails or limits are exceeded.
// Callers should use GetOrCreateSessionWithError for detailed error information.
func (sr *SessionRegistry) GetOrCreateSession(sessionID string) *SessionState {
	session, _ := sr.GetOrCreateSessionWithError(sessionID)
	return session
}

// GetOrCreateSessionWithError returns the session state for a session ID, creating it if necessary.
//
// This method provides detailed error information for validation and limit failures.
//
// Args:
//   - sessionID: Unique identifier for the session
//
// Returns:
//   - The session state (nil on error)
//   - An error if validation fails or limits are exceeded
func (sr *SessionRegistry) GetOrCreateSessionWithError(sessionID string) (*SessionState, error) {
	// Validate session ID before acquiring lock
	if err := ValidateSessionID(sessionID); err != nil {
		logging.Warn("SessionRegistry", "Rejected invalid session ID: %v", err)
		return nil, err
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()

	session, exists := sr.sessions[sessionID]
	if !exists {
		// Check session limit before creating new session
		if sr.maxSessions > 0 && len(sr.sessions) >= sr.maxSessions {
			logging.Warn("SessionRegistry", "Session limit reached (%d), rejecting new session: %s",
				sr.maxSessions, logging.TruncateSessionID(sessionID))
			return nil, &SessionLimitExceededError{Limit: sr.maxSessions, Current: len(sr.sessions)}
		}

		session = &SessionState{
			SessionID:    sessionID,
			CreatedAt:    time.Now(),
			LastActivity: time.Now(),
			Connections:  make(map[string]*SessionConnection),
		}
		sr.sessions[sessionID] = session
		logging.Debug("SessionRegistry", "Created new session: %s (total: %d)",
			logging.TruncateSessionID(sessionID), len(sr.sessions))
	} else {
		session.UpdateActivity()
	}

	return session, nil
}

// GetSession returns the session state for a session ID.
//
// Args:
//   - sessionID: Unique identifier for the session
//
// Returns the session state and true if found, nil and false otherwise.
// Invalid session IDs return nil and false.
func (sr *SessionRegistry) GetSession(sessionID string) (*SessionState, bool) {
	// Validate session ID before lookup
	if err := ValidateSessionID(sessionID); err != nil {
		return nil, false
	}

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
		// Acquire write lock directly since we may need to close connections.
		// This avoids a race condition where LastActivity could be updated
		// between checking and closing.
		session.mu.Lock()
		lastActivity := session.LastActivity
		if now.Sub(lastActivity) > sr.sessionTimeout {
			// Close connections inline while holding the lock
			for serverName, conn := range session.Connections {
				if conn.Client != nil {
					if err := conn.Client.Close(); err != nil {
						logging.Warn("SessionRegistry", "Error closing client for session=%s server=%s: %v",
							logging.TruncateSessionID(session.SessionID), serverName, err)
					}
				}
			}
			session.Connections = make(map[string]*SessionConnection)
			session.mu.Unlock()
			delete(sr.sessions, sessionID)
			count++
			continue
		}
		session.mu.Unlock()
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
//
// This method validates the connection state before upgrading:
//   - The connection must exist
//   - The connection must be in pending_auth state (not already connected)
//
// Returns an error if validation fails.
func (s *SessionState) UpgradeConnection(serverName string, client MCPClient, tokenKey *oauth.TokenKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	conn, exists := s.Connections[serverName]
	if !exists {
		return &ConnectionNotFoundError{ServerName: serverName}
	}

	// Validate connection state - prevent double upgrades
	if conn.Status == StatusSessionConnected {
		return &ConnectionAlreadyEstablishedError{ServerName: serverName}
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

// ConnectionAlreadyEstablishedError is returned when attempting to upgrade
// a connection that is already in connected state.
type ConnectionAlreadyEstablishedError struct {
	ServerName string
}

func (e *ConnectionAlreadyEstablishedError) Error() string {
	return "connection already established: " + e.ServerName
}

// InvalidSessionIDError is returned when a session ID fails validation.
type InvalidSessionIDError struct {
	Reason string
}

func (e *InvalidSessionIDError) Error() string {
	return "invalid session ID: " + e.Reason
}

// SessionLimitExceededError is returned when the maximum session limit is reached.
type SessionLimitExceededError struct {
	Limit   int
	Current int
}

func (e *SessionLimitExceededError) Error() string {
	return fmt.Sprintf("session limit exceeded: %d/%d sessions", e.Current, e.Limit)
}
