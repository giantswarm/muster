package aggregator

import (
	"fmt"

	"github.com/giantswarm/muster/pkg/logging"
)

// MusterSessionIdManager implements mcpserver.SessionIdManager to bridge
// muster's SessionRegistry into mcp-go's session lifecycle.
//
// This replaces the custom X-Muster-Session-ID header with mcp-go's native
// Mcp-Session-Id session mechanism, while preserving muster's session registry
// features (identity binding, OAuth token storage, proactive SSO).
//
// Generate() creates a new session in the registry (subject bound later via httpContextFunc).
// Validate() checks whether the session exists or has been terminated.
// Terminate() removes the session from the registry.
type MusterSessionIdManager struct {
	registry *SessionRegistry
}

// NewMusterSessionIdManager creates a new session ID manager backed by the given registry.
func NewMusterSessionIdManager(registry *SessionRegistry) *MusterSessionIdManager {
	return &MusterSessionIdManager{registry: registry}
}

// Generate creates a new session in the registry and returns its ID.
// The session is created without a subject; identity binding happens later
// in httpContextFunc when the authenticated user's subject is available.
func (m *MusterSessionIdManager) Generate() string {
	session, err := m.registry.CreateSessionForSubject("")
	if err != nil {
		logging.Warn("SessionIdManager", "Failed to create session: %v", err)
		// Return a generated ID anyway so the protocol doesn't break.
		// The session won't be in the registry, but subsequent requests
		// will get "session not found" errors which is better than a crash.
		id, genErr := GenerateSessionID()
		if genErr != nil {
			return fmt.Sprintf("fallback-%d", 0) // extremely unlikely
		}
		return id
	}
	logging.Debug("SessionIdManager", "Generated new session: %s", logging.TruncateSessionID(session.SessionID))
	return session.SessionID
}

// Validate checks if a session ID is valid and whether it has been terminated.
// Returns (false, nil) if the session exists and is active.
// Returns (true, nil) if the session ID format is valid but the session doesn't exist (terminated).
// Returns (false, err) if the session ID format is invalid.
func (m *MusterSessionIdManager) Validate(sessionID string) (isTerminated bool, err error) {
	if err := ValidateSessionID(sessionID); err != nil {
		return false, fmt.Errorf("invalid session ID: %w", err)
	}

	_, exists := m.registry.GetSession(sessionID)
	if !exists {
		// Session not in registry = terminated (or never existed, same effect)
		return true, nil
	}

	return false, nil
}

// Terminate removes a session from the registry, closing all its connections.
// Returns (false, nil) on success.
// Returns (false, err) if the session ID is invalid.
func (m *MusterSessionIdManager) Terminate(sessionID string) (isNotAllowed bool, err error) {
	if err := ValidateSessionID(sessionID); err != nil {
		return false, fmt.Errorf("invalid session ID: %w", err)
	}

	m.registry.DeleteSession(sessionID)
	logging.Info("SessionIdManager", "Terminated session: %s", logging.TruncateSessionID(sessionID))
	return false, nil
}
