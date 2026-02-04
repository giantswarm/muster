package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
)

// StateStore provides thread-safe storage for OAuth state parameters.
// State parameters are used to link OAuth callbacks to original requests
// and provide CSRF protection.
//
// IMPORTANT: StateStore starts a background goroutine for cleanup. Callers MUST
// call Stop() when done to prevent goroutine leaks. Typically this is done via
// defer after creating the store, or in a shutdown hook for long-lived stores.
type StateStore struct {
	mu     sync.RWMutex
	states map[string]*OAuthState

	// Expiration configuration
	stateExpiry time.Duration
	stopCleanup chan struct{}
}

// NewStateStore creates a new state store with default expiration.
func NewStateStore() *StateStore {
	ss := &StateStore{
		states:      make(map[string]*OAuthState),
		stateExpiry: 10 * time.Minute, // State expires after 10 minutes
		stopCleanup: make(chan struct{}),
	}

	// Start background cleanup
	go ss.cleanupLoop()

	return ss
}

// GenerateState creates a new OAuth state parameter and stores it.
// Returns the encoded state string to include in the authorization URL.
// The nonce is embedded within the encoded state and used for server-side lookup.
//
// Args:
//   - sessionID: The user's session ID
//   - serverName: The MCP server name requiring authentication
//   - issuer: The OAuth issuer URL
//   - codeVerifier: The PKCE code verifier for this flow
func (ss *StateStore) GenerateState(sessionID, serverName, issuer, codeVerifier string) (encodedState string, err error) {
	// Generate a cryptographically random nonce
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", err
	}

	nonce := base64.URLEncoding.EncodeToString(nonceBytes)
	state := &OAuthState{
		SessionID:    sessionID,
		ServerName:   serverName,
		Nonce:        nonce,
		CreatedAt:    time.Now(),
		Issuer:       issuer,
		CodeVerifier: codeVerifier,
	}

	// Encode the state as JSON then base64 (CodeVerifier is excluded via json:"-")
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return "", err
	}

	encodedState = base64.URLEncoding.EncodeToString(stateJSON)

	// Store the full state (including CodeVerifier) indexed by the nonce
	ss.mu.Lock()
	ss.states[nonce] = state
	ss.mu.Unlock()

	logging.Debug("OAuth", "Generated state for session=%s server=%s issuer=%s", logging.TruncateSessionID(sessionID), serverName, issuer)
	return encodedState, nil
}

// ValidateState validates an OAuth state parameter from a callback.
// Returns the original state data if valid, nil if invalid or expired.
func (ss *StateStore) ValidateState(encodedState string) *OAuthState {
	// Decode the state
	stateJSON, err := base64.URLEncoding.DecodeString(encodedState)
	if err != nil {
		logging.Warn("OAuth", "Failed to decode state: %v", err)
		return nil
	}

	var state OAuthState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		logging.Warn("OAuth", "Failed to unmarshal state: %v", err)
		return nil
	}

	// Look up the stored state by nonce
	ss.mu.RLock()
	storedState, exists := ss.states[state.Nonce]
	ss.mu.RUnlock()

	if !exists {
		logging.Warn("OAuth", "State not found in store: nonce=%s", state.Nonce)
		return nil
	}

	// Check expiration
	if time.Since(storedState.CreatedAt) > ss.stateExpiry {
		logging.Warn("OAuth", "State expired: nonce=%s age=%v", state.Nonce, time.Since(storedState.CreatedAt))
		ss.Delete(state.Nonce)
		return nil
	}

	// State is valid - delete it to prevent replay
	ss.Delete(state.Nonce)

	return storedState
}

// Delete removes a state from the store.
func (ss *StateStore) Delete(nonce string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.states, nonce)
}

// Stop stops the background cleanup goroutine.
func (ss *StateStore) Stop() {
	close(ss.stopCleanup)
}

// cleanupLoop periodically removes expired states from the store.
func (ss *StateStore) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ss.cleanup()
		case <-ss.stopCleanup:
			return
		}
	}
}

// cleanup removes all expired states from the store.
func (ss *StateStore) cleanup() {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	count := 0
	for nonce, state := range ss.states {
		if time.Since(state.CreatedAt) > ss.stateExpiry {
			delete(ss.states, nonce)
			count++
		}
	}

	if count > 0 {
		logging.Debug("OAuth", "Cleaned up %d expired states", count)
	}
}
