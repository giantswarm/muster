package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/giantswarm/mcp-oauth/security"
	"github.com/giantswarm/muster/pkg/logging"
	"github.com/valkey-io/valkey-go"
)

const (
	defaultStateExpiry = 10 * time.Minute
)

// valkeyStateEntry is the JSON-serialized value stored for each state.
type valkeyStateEntry struct {
	SessionID    string    `json:"sid"`
	UserID       string    `json:"uid"`
	ServerName   string    `json:"srv"`
	Nonce        string    `json:"n"`
	CreatedAt    time.Time `json:"ca"`
	Issuer       string    `json:"iss,omitempty"`
	CodeVerifier string    `json:"cv,omitempty"`
}

// ValkeyStateStore stores OAuth state parameters in Valkey with automatic
// expiration and optional AES-256-GCM encryption at rest.
// Each state is stored as a single key with a 10-minute TTL,
// matching the in-memory StateStore behaviour.
//
// Data model:
//
//	Key:   {keyPrefix}oauth:state:{nonce}
//	Value: [encrypted] JSON(valkeyStateEntry)
//	TTL:   10 minutes
type ValkeyStateStore struct {
	client      valkey.Client
	stateExpiry time.Duration
	keyPrefix   string
	encryptor   *security.Encryptor
}

// NewValkeyStateStore creates a Valkey-backed OAuth state store.
// keyPrefix is prepended to all Valkey keys (default "muster:" if empty).
// encryptor enables AES-256-GCM encryption at rest; pass nil to disable.
func NewValkeyStateStore(client valkey.Client, keyPrefix string, encryptor *security.Encryptor) *ValkeyStateStore {
	if keyPrefix == "" {
		keyPrefix = "muster:"
	}
	return &ValkeyStateStore{
		client:      client,
		stateExpiry: defaultStateExpiry,
		keyPrefix:   keyPrefix,
		encryptor:   encryptor,
	}
}

func (s *ValkeyStateStore) stateKey(nonce string) string {
	return s.keyPrefix + "oauth:state:" + nonce
}

func (s *ValkeyStateStore) encryptValue(data []byte) (string, error) {
	if s.encryptor == nil || !s.encryptor.IsEnabled() {
		return string(data), nil
	}
	return s.encryptor.Encrypt(string(data))
}

func (s *ValkeyStateStore) decryptValue(stored string) ([]byte, error) {
	if s.encryptor == nil || !s.encryptor.IsEnabled() {
		return []byte(stored), nil
	}
	plaintext, err := s.encryptor.Decrypt(stored)
	if err != nil {
		return nil, err
	}
	return []byte(plaintext), nil
}

func (s *ValkeyStateStore) GenerateState(sessionID, userID, serverName, issuer, codeVerifier string) (string, error) {
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
	encodedState := base64.URLEncoding.EncodeToString(stateJSON)

	entry := &valkeyStateEntry{
		SessionID:    sessionID,
		UserID:       userID,
		ServerName:   serverName,
		Nonce:        nonce,
		CreatedAt:    state.CreatedAt,
		Issuer:       issuer,
		CodeVerifier: codeVerifier,
	}

	entryData, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}

	value, err := s.encryptValue(entryData)
	if err != nil {
		return "", err
	}

	ctx := context.Background()
	cmd := s.client.B().Set().Key(s.stateKey(nonce)).Value(value).
		Ex(s.stateExpiry).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		return "", err
	}

	logging.Debug("OAuth", "ValkeyStateStore: generated state for session=%s server=%s issuer=%s",
		logging.TruncateIdentifier(sessionID), serverName, issuer)
	return encodedState, nil
}

func (s *ValkeyStateStore) ValidateState(encodedState string) *OAuthState {
	stateJSON, err := base64.URLEncoding.DecodeString(encodedState)
	if err != nil {
		logging.Warn("OAuth", "ValkeyStateStore: failed to decode state: %v", err)
		return nil
	}

	var state OAuthState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		logging.Warn("OAuth", "ValkeyStateStore: failed to unmarshal state: %v", err)
		return nil
	}

	ctx := context.Background()
	key := s.stateKey(state.Nonce)

	// GET + DEL atomically: retrieve and consume in one round-trip via GETDEL.
	cmd := s.client.B().Getdel().Key(key).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		if valkey.IsValkeyNil(err) {
			logging.Warn("OAuth", "ValkeyStateStore: state not found: nonce=%s", state.Nonce)
		} else {
			logging.Warn("OAuth", "ValkeyStateStore: GETDEL failed: %v", err)
		}
		return nil
	}

	stored, err := result.ToString()
	if err != nil {
		return nil
	}

	plaintext, err := s.decryptValue(stored)
	if err != nil {
		logging.Warn("OAuth", "ValkeyStateStore: decryption failed: %v", err)
		return nil
	}

	var entry valkeyStateEntry
	if err := json.Unmarshal(plaintext, &entry); err != nil {
		logging.Warn("OAuth", "ValkeyStateStore: failed to unmarshal stored state: %v", err)
		return nil
	}

	// Defense-in-depth expiry check. Valkey TTL already enforces the 10-minute
	// window, but we verify CreatedAt as well to guard against clock skew
	// between the application node that created the state and the node
	// validating it. Clocks should be NTP-synchronized; with significant skew
	// (>10 min) legitimate states may be rejected.
	if time.Since(entry.CreatedAt) > s.stateExpiry {
		logging.Warn("OAuth", "ValkeyStateStore: state expired: nonce=%s age=%v",
			state.Nonce, time.Since(entry.CreatedAt))
		return nil
	}

	return &OAuthState{
		SessionID:    entry.SessionID,
		UserID:       entry.UserID,
		ServerName:   entry.ServerName,
		Nonce:        entry.Nonce,
		CreatedAt:    entry.CreatedAt,
		Issuer:       entry.Issuer,
		CodeVerifier: entry.CodeVerifier,
	}
}

func (s *ValkeyStateStore) Delete(nonce string) {
	ctx := context.Background()
	cmd := s.client.B().Del().Key(s.stateKey(nonce)).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		logging.Warn("OAuth", "ValkeyStateStore: Delete failed: %v", err)
	}
}

// Stop is a no-op for the Valkey implementation (no background goroutines).
// The Valkey client is closed separately during server shutdown.
func (s *ValkeyStateStore) Stop() {}

// Ensure ValkeyStateStore implements StateStorer at compile time.
var _ StateStorer = (*ValkeyStateStore)(nil)

// Ensure the in-memory StateStore also implements StateStorer.
var _ StateStorer = (*StateStore)(nil)
