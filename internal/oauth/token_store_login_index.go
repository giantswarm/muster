package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// LoginIDTokenDigest is the key a login ID token is indexed under: the token
// itself is never a key.
func LoginIDTokenDigest(idToken string) string {
	sum := sha256.Sum256([]byte(idToken))
	return hex.EncodeToString(sum[:])
}

// loginIndexEntry is the session that filed a login ID token, until the
// token's expiry.
type loginIndexEntry struct {
	sessionID string
	expiresAt time.Time
}

// IndexLoginIDToken implements TokenStorer.
func (ts *TokenStore) IndexLoginIDToken(digest, sessionID string, expiresAt time.Time, keep bool) {
	if !time.Now().Before(expiresAt) {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.logins == nil {
		ts.logins = map[string]loginIndexEntry{}
	}
	if current, ok := ts.logins[digest]; keep && ok && time.Now().Before(current.expiresAt) {
		return
	}
	ts.logins[digest] = loginIndexEntry{sessionID: sessionID, expiresAt: expiresAt}
}

// LoginIDTokenSession implements TokenStorer.
func (ts *TokenStore) LoginIDTokenSession(digest string) string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	entry, ok := ts.logins[digest]
	if !ok || !time.Now().Before(entry.expiresAt) {
		return ""
	}
	return entry.sessionID
}

// cleanupLogins drops the expired index records. Callers hold mu.
func (ts *TokenStore) cleanupLogins(now time.Time) {
	for digest, entry := range ts.logins {
		if !now.Before(entry.expiresAt) {
			delete(ts.logins, digest)
		}
	}
}

func (s *ValkeyTokenStore) loginIndexKey(digest string) string {
	return s.keyPrefix + "oauth:login:" + digest
}

// IndexLoginIDToken implements TokenStorer: one key per digest that expires
// with the token, so every replica and a restarted muster resolve it.
func (s *ValkeyTokenStore) IndexLoginIDToken(digest, sessionID string, expiresAt time.Time, keep bool) {
	if !time.Now().Before(expiresAt) {
		return
	}
	value, err := s.encryptValue([]byte(sessionID))
	if err != nil {
		logging.Warn("OAuth", "ValkeyTokenStore: failed to encrypt login index record: %v", err)
		return
	}
	set := s.client.B().Set().Key(s.loginIndexKey(digest)).Value(value)
	var cmd valkey.Completed
	if keep {
		cmd = set.Nx().Exat(expiresAt).Build()
	} else {
		cmd = set.Exat(expiresAt).Build()
	}
	if err := s.client.Do(context.Background(), cmd).Error(); err != nil && !valkey.IsValkeyNil(err) {
		logging.Warn("OAuth", "ValkeyTokenStore: IndexLoginIDToken SET failed: %v", err)
	}
}

// LoginIDTokenSession implements TokenStorer.
func (s *ValkeyTokenStore) LoginIDTokenSession(digest string) string {
	stored, err := s.client.Do(context.Background(), s.client.B().Get().Key(s.loginIndexKey(digest)).Build()).ToString()
	if err != nil {
		if !valkey.IsValkeyNil(err) {
			logging.Warn("OAuth", "ValkeyTokenStore: LoginIDTokenSession GET failed: %v", err)
		}
		return ""
	}
	sessionID, err := s.decryptValue(stored)
	if err != nil {
		return ""
	}
	return string(sessionID)
}
