package oauth

import (
	"context"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// finishedGrace bounds how long a flow's finished record is kept: as long
// as the flow's state lives, so a tool call that names the flow while its
// link can still be followed finds the record.
const finishedGrace = 10 * time.Minute

// finishedEntry is one flow whose callback ran to its end, with the session
// it was started for and its recording time.
type finishedEntry struct {
	sessionID string
	at        time.Time
}

// MarkFinished records that a flow's callback ran to its end (see
// StateStorer).
func (ss *StateStore) MarkFinished(encodedState, sessionID string) {
	nonce, err := decodeStateNonce(encodedState)
	if err != nil {
		logging.Warn("OAuth", "Failed to decode state for finished record: %v", err)
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.finished[nonce] = &finishedEntry{sessionID: sessionID, at: time.Now()}
}

// Finished returns the session of a flow whose callback ran to its end, or
// "" (see StateStorer).
func (ss *StateStore) Finished(encodedState string) string {
	nonce, err := decodeStateNonce(encodedState)
	if err != nil {
		return ""
	}
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	entry, exists := ss.finished[nonce]
	if !exists || time.Since(entry.at) > finishedGrace {
		return ""
	}
	return entry.sessionID
}

func (s *ValkeyStateStore) finishedKey(nonce string) string {
	return s.keyPrefix + "oauth:statefinished:" + nonce
}

// MarkFinished records that a flow's callback ran to its end (see
// StateStorer). The session ID is encrypted like the states themselves, and
// Valkey's TTL enforces finishedGrace across replicas.
func (s *ValkeyStateStore) MarkFinished(encodedState, sessionID string) {
	nonce, err := decodeStateNonce(encodedState)
	if err != nil {
		logging.Warn("OAuth", "ValkeyStateStore: failed to decode state for finished record: %v", err)
		return
	}
	value, err := s.encryptValue([]byte(sessionID))
	if err != nil {
		logging.Warn("OAuth", "ValkeyStateStore: encryption failed on finished record: %v", err)
		return
	}
	cmd := s.client.B().Set().Key(s.finishedKey(nonce)).Value(value).Ex(finishedGrace).Build()
	if err := s.client.Do(context.Background(), cmd).Error(); err != nil {
		logging.Warn("OAuth", "ValkeyStateStore: SET finished record failed: %v", err)
	}
}

// Finished returns the session of a flow whose callback ran to its end, or
// "" (see StateStorer).
func (s *ValkeyStateStore) Finished(encodedState string) string {
	nonce, err := decodeStateNonce(encodedState)
	if err != nil {
		return ""
	}
	result := s.client.Do(context.Background(), s.client.B().Get().Key(s.finishedKey(nonce)).Build())
	if err := result.Error(); err != nil {
		if !valkey.IsValkeyNil(err) {
			logging.Warn("OAuth", "ValkeyStateStore: GET finished record failed: %v", err)
		}
		return ""
	}
	stored, err := result.ToString()
	if err != nil {
		return ""
	}
	plaintext, err := s.decryptValue(stored)
	if err != nil {
		logging.Warn("OAuth", "ValkeyStateStore: decryption failed on finished record: %v", err)
		return ""
	}
	return string(plaintext)
}
