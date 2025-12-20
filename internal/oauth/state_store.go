package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"

	"muster/pkg/logging"
)

// StateStore provides thread-safe storage for OAuth state parameters.
// State parameters are used to link OAuth callbacks to original requests
// and provide CSRF protection.
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
func (ss *StateStore) GenerateState(sessionID, serverName string) (string, error) {
	// Generate a cryptographically random nonce
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	state := &OAuthState{
		SessionID:  sessionID,
		ServerName: serverName,
		Nonce:      base64.URLEncoding.EncodeToString(nonce),
		CreatedAt:  time.Now(),
	}

	// Encode the state as JSON then base64
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return "", err
	}

	encodedState := base64.URLEncoding.EncodeToString(stateJSON)

	// Store the state indexed by the nonce
	ss.mu.Lock()
	ss.states[state.Nonce] = state
	ss.mu.Unlock()

	logging.Debug("OAuth", "Generated state for session=%s server=%s", sessionID, serverName)
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
