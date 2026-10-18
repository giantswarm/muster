package oauth

import (
	"context"

	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// laterToken returns the one of current and candidate that expires last.
func laterToken(current, candidate *pkgoauth.Token) *pkgoauth.Token {
	if candidate == nil || (current != nil && !candidate.ExpiresAt.After(current.ExpiresAt)) {
		return current
	}
	return candidate
}

// GetLongestLivedForUser implements TokenStorer.
func (ts *TokenStore) GetLongestLivedForUser(userID, issuer, scope string) *pkgoauth.Token {
	if userID == "" {
		return nil
	}
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	var best *pkgoauth.Token
	for key, entry := range ts.tokens {
		if entry.userID != userID || key.Issuer != issuer || key.Scope != scope || entry.token.IsExpiredWithMargin(tokenExpiryMargin) {
			continue
		}
		best = laterToken(best, entry.token)
	}
	return best
}

// GetLongestLivedForUser implements TokenStorer: one read per session of the
// user's index.
func (s *ValkeyTokenStore) GetLongestLivedForUser(userID, issuer, scope string) *pkgoauth.Token {
	if userID == "" {
		return nil
	}
	result := s.client.Do(context.Background(), s.client.B().Smembers().Key(s.userKey(userID)).Build())
	if err := result.Error(); err != nil {
		if !valkey.IsValkeyNil(err) {
			logging.Warn("OAuth", "ValkeyTokenStore: GetLongestLivedForUser SMEMBERS failed: %v", err)
		}
		return nil
	}
	sessionIDs, err := result.AsStrSlice()
	if err != nil {
		return nil
	}
	var best *pkgoauth.Token
	for _, sessionID := range sessionIDs {
		best = laterToken(best, s.userToken(userID, TokenKey{SessionID: sessionID, Issuer: issuer, Scope: scope}))
	}
	return best
}

// userToken returns the live token under key when it is filed under userID:
// a session in the user's index whose entry another user's write replaced is
// not the user's.
func (s *ValkeyTokenStore) userToken(userID string, key TokenKey) *pkgoauth.Token {
	result := s.client.Do(context.Background(), s.client.B().Hget().Key(s.sessionKey(key.SessionID)).Field(fieldName(key)).Build())
	stored, err := result.ToString()
	if err != nil {
		if !valkey.IsValkeyNil(err) {
			logging.Warn("OAuth", "ValkeyTokenStore: GetLongestLivedForUser HGET failed: %v", err)
		}
		return nil
	}
	entry, err := s.decodeEntry(stored)
	if err != nil || entry.UserID != userID {
		return nil
	}
	token := entryToToken(entry)
	if token.IsExpiredWithMargin(tokenExpiryMargin) {
		return nil
	}
	return token
}
