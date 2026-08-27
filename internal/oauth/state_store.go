package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
)

// StateStorer is the interface for OAuth state parameter storage.
// Implementations must be safe for concurrent use.
type StateStorer interface {
	// GenerateState creates a new OAuth state, stores it, and returns the
	// base64-encoded state string the start endpoint resolves. When
	// buildAuthorizationURL is non-nil it is called with the encoded state
	// and its result is stored as the flow's upstream authorization URL, so
	// state and URL land in a single write; an error aborts without storing.
	GenerateState(sessionID, userID, serverName, issuer, codeVerifier string,
		buildAuthorizationURL func(encodedState string) (string, error)) (encodedState string, err error)

	// ValidateState validates an encoded state from a callback. Returns the
	// original state if valid; nil if invalid, expired, or already consumed.
	// Valid states are consumed (single-use) to prevent replay attacks.
	ValidateState(encodedState string) *OAuthState

	// Update applies mutate to a stored state without consuming it and
	// returns a copy of the updated state; nil if the state is invalid,
	// expired, or absent. The TTL is unchanged.
	Update(encodedState string, mutate func(*OAuthState)) *OAuthState

	// MarkCompleted records that the flow behind an encoded state finished
	// successfully. Browsers and IdP redirect pages deliver the callback
	// navigation more than once; the record lets a duplicate delivery of an
	// already-consumed state render the flow's outcome again instead of an
	// error. The record expires after completionGrace.
	MarkCompleted(encodedState string, done *CompletedFlow)

	// Completed returns the recorded outcome for an encoded state whose flow
	// recently completed, or nil. The record is not consumed — every
	// duplicate within the grace window renders the same outcome.
	Completed(encodedState string) *CompletedFlow

	// Delete removes a state entry by nonce.
	Delete(nonce string)

	// Stop releases resources (background goroutines, connections, etc.).
	Stop()
}

// CompletedFlow is what a duplicate callback delivery needs to re-render a
// completed flow's outcome: the server name for the success page and the
// allowlist-validated post-login redirect target, if the flow recorded one.
// Deliberately no tokens, code verifier, or session identifiers.
type CompletedFlow struct {
	ServerName  string `json:"srv"`
	RedirectURI string `json:"ru,omitempty"`
}

// completionGrace bounds how long a completed flow's outcome is re-rendered
// for duplicate callback deliveries. Duplicates arrive within milliseconds
// (double navigation) — minutes is generous without keeping records around.
const completionGrace = 2 * time.Minute

// decodeStateNonce extracts the nonce from an encoded state parameter.
func decodeStateNonce(encodedState string) (string, error) {
	stateJSON, err := base64.URLEncoding.DecodeString(encodedState)
	if err != nil {
		return "", err
	}
	var state OAuthState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return "", err
	}
	return state.Nonce, nil
}

// StateStore provides thread-safe in-memory storage for OAuth state parameters.
// State parameters are used to link OAuth callbacks to original requests
// and provide CSRF protection.
//
// IMPORTANT: StateStore starts a background goroutine for cleanup. Callers MUST
// call Stop() when done to prevent goroutine leaks. Typically this is done via
// defer after creating the store, or in a shutdown hook for long-lived stores.
type StateStore struct {
	mu     sync.RWMutex
	states map[string]*OAuthState

	// completed records recently finished flows by nonce, so duplicate
	// callback deliveries can re-render the outcome (see MarkCompleted).
	completed map[string]*completedEntry

	// Expiration configuration
	stateExpiry time.Duration
	stopCleanup chan struct{}
}

// completedEntry is one recently completed flow with its recording time.
type completedEntry struct {
	flow *CompletedFlow
	at   time.Time
}

// NewStateStore creates a new state store with default expiration.
func NewStateStore() *StateStore {
	ss := &StateStore{
		states:      make(map[string]*OAuthState),
		completed:   make(map[string]*completedEntry),
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
//   - subject: The user's identity
//   - serverName: The MCP server name requiring authentication
//   - issuer: The OAuth issuer URL
//   - codeVerifier: The PKCE code verifier for this flow
func (ss *StateStore) GenerateState(sessionID, userID, serverName, issuer, codeVerifier string,
	buildAuthorizationURL func(encodedState string) (string, error)) (encodedState string, err error) {
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", err
	}

	nonce := base64.URLEncoding.EncodeToString(nonceBytes)
	state := &OAuthState{
		SessionID:    sessionID,
		UserID:       userID,
		ServerName:   serverName,
		Nonce:        nonce,
		CreatedAt:    time.Now(),
		Issuer:       issuer,
		CodeVerifier: codeVerifier,
	}

	stateJSON, err := json.Marshal(state)
	if err != nil {
		return "", err
	}

	encodedState = base64.URLEncoding.EncodeToString(stateJSON)

	if buildAuthorizationURL != nil {
		state.AuthorizationURL, err = buildAuthorizationURL(encodedState)
		if err != nil {
			return "", err
		}
	}

	ss.mu.Lock()
	ss.states[nonce] = state
	ss.mu.Unlock()

	logging.Debug("OAuth", "Generated state for session=%s server=%s issuer=%s", logging.TruncateIdentifier(sessionID), serverName, issuer)
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

// Update applies mutate to a stored state without consuming it.
// Returns a copy of the updated state, or nil if the state is invalid,
// expired, or absent.
func (ss *StateStore) Update(encodedState string, mutate func(*OAuthState)) *OAuthState {
	stateJSON, err := base64.URLEncoding.DecodeString(encodedState)
	if err != nil {
		logging.Warn("OAuth", "Failed to decode state for update: %v", err)
		return nil
	}

	var state OAuthState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		logging.Warn("OAuth", "Failed to unmarshal state for update: %v", err)
		return nil
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	storedState, exists := ss.states[state.Nonce]
	if !exists {
		logging.Warn("OAuth", "State not found for update: nonce=%s", state.Nonce)
		return nil
	}
	if time.Since(storedState.CreatedAt) > ss.stateExpiry {
		logging.Warn("OAuth", "State expired on update: nonce=%s", state.Nonce)
		delete(ss.states, state.Nonce)
		return nil
	}

	mutate(storedState)
	updated := *storedState
	return &updated
}

// MarkCompleted records a finished flow's outcome for duplicate callback
// deliveries (see StateStorer).
func (ss *StateStore) MarkCompleted(encodedState string, done *CompletedFlow) {
	nonce, err := decodeStateNonce(encodedState)
	if err != nil {
		logging.Warn("OAuth", "Failed to decode state for completion record: %v", err)
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.completed[nonce] = &completedEntry{flow: done, at: time.Now()}
}

// Completed returns the recorded outcome for a recently completed flow, or
// nil (see StateStorer).
func (ss *StateStore) Completed(encodedState string) *CompletedFlow {
	nonce, err := decodeStateNonce(encodedState)
	if err != nil {
		return nil
	}
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	entry, exists := ss.completed[nonce]
	if !exists || time.Since(entry.at) > completionGrace {
		return nil
	}
	return entry.flow
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
	for nonce, entry := range ss.completed {
		if time.Since(entry.at) > completionGrace {
			delete(ss.completed, nonce)
		}
	}

	if count > 0 {
		logging.Debug("OAuth", "Cleaned up %d expired states", count)
	}
}
