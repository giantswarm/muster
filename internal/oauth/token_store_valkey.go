package oauth

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/giantswarm/mcp-oauth/security"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

const (
	// valkeyTokenFieldSep separates issuer and scope in hash field names.
	// The issuer (a URL) comes first, so SplitN(field, sep, 2) is safe as long
	// as issuer URLs don't contain this character. Pipe was chosen because it is
	// not valid in URL authority/path segments per RFC 3986.
	valkeyTokenFieldSep = "|"

	countTimeout = 10 * time.Second
)

// valkeyTokenEntry is the JSON-serialized value stored in each hash field.
type valkeyTokenEntry struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	Issuer       string    `json:"issuer,omitempty"`
	UserID       string    `json:"user_id"`
}

// ValkeyTokenStore stores OAuth tokens in Valkey hashes with optional
// AES-256-GCM encryption at rest.
//
// Data model:
//
//	Session key:  {keyPrefix}oauth:token:{sessionID}
//	  Fields:     {issuer}|{scope} -> [encrypted] JSON(valkeyTokenEntry)
//	  TTL:        session-level, reset on every Store
//
//	User index:   {keyPrefix}oauth:token:user:{userID}
//	  Members:    sessionIDs (for DeleteByUser reverse lookup)
//	  TTL:        same as session key
type ValkeyTokenStore struct {
	valkeyEncryption
	client    valkey.Client
	ttl       time.Duration
	keyPrefix string
}

// NewValkeyTokenStore creates a Valkey-backed OAuth token store.
// keyPrefix is prepended to all Valkey keys (default "muster:" if empty).
// encryptor enables AES-256-GCM encryption at rest; pass nil to disable.
func NewValkeyTokenStore(client valkey.Client, ttl time.Duration, keyPrefix string, encryptor *security.Encryptor) *ValkeyTokenStore {
	if keyPrefix == "" {
		keyPrefix = "muster:"
	}
	return &ValkeyTokenStore{
		valkeyEncryption: valkeyEncryption{encryptor: encryptor},
		client:           client,
		ttl:              ttl,
		keyPrefix:        keyPrefix,
	}
}

func (s *ValkeyTokenStore) sessionKey(sessionID string) string {
	return s.keyPrefix + "oauth:token:" + sessionID
}

func (s *ValkeyTokenStore) userKey(userID string) string {
	return s.keyPrefix + "oauth:token:user:" + userID
}

func fieldName(key TokenKey) string {
	return key.Issuer + valkeyTokenFieldSep + key.Scope
}

func parseFieldName(field string) (issuer, scope string) {
	parts := strings.SplitN(field, valkeyTokenFieldSep, 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return field, ""
}

func tokenToEntry(token *pkgoauth.Token, userID string) *valkeyTokenEntry {
	return &valkeyTokenEntry{
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		ExpiresIn:    token.ExpiresIn,
		ExpiresAt:    token.ExpiresAt,
		Scope:        token.Scope,
		IDToken:      token.IDToken,
		Issuer:       token.Issuer,
		UserID:       userID,
	}
}

func entryToToken(e *valkeyTokenEntry) *pkgoauth.Token {
	return &pkgoauth.Token{
		AccessToken:  e.AccessToken,
		TokenType:    e.TokenType,
		RefreshToken: e.RefreshToken,
		ExpiresIn:    e.ExpiresIn,
		ExpiresAt:    e.ExpiresAt,
		Scope:        e.Scope,
		IDToken:      e.IDToken,
		Issuer:       e.Issuer,
	}
}

func (s *ValkeyTokenStore) Store(key TokenKey, token *pkgoauth.Token, userID string) {
	ctx := context.Background()

	token.SetExpiresAtFromExpiresIn()
	entry := tokenToEntry(token, userID)
	jsonData, err := json.Marshal(entry) //nolint:gosec
	if err != nil {
		logging.Warn("OAuth", "ValkeyTokenStore: failed to marshal token: %v", err)
		return
	}

	value, err := s.encryptValue(jsonData)
	if err != nil {
		logging.Warn("OAuth", "ValkeyTokenStore: failed to encrypt token: %v", err)
		return
	}

	sessionKey := s.sessionKey(key.SessionID)
	field := fieldName(key)
	ttlSec := int64(s.ttl.Seconds())

	cmds := make(valkey.Commands, 0, 4)
	cmds = append(cmds, s.client.B().Hset().Key(sessionKey).FieldValue().FieldValue(field, value).Build())
	cmds = append(cmds, s.client.B().Expire().Key(sessionKey).Seconds(ttlSec).Build())

	if userID != "" {
		uKey := s.userKey(userID)
		cmds = append(cmds, s.client.B().Sadd().Key(uKey).Member(key.SessionID).Build())
		cmds = append(cmds, s.client.B().Expire().Key(uKey).Seconds(ttlSec).Build())
	}

	for _, resp := range s.client.DoMulti(ctx, cmds...) {
		if err := resp.Error(); err != nil {
			logging.Warn("OAuth", "ValkeyTokenStore: Store failed: %v", err)
			return
		}
	}

	logging.Debug("OAuth", "ValkeyTokenStore: stored token for session=%s issuer=%s scope=%s",
		logging.TruncateIdentifier(key.SessionID), key.Issuer, key.Scope)
}

func (s *ValkeyTokenStore) Get(key TokenKey) *pkgoauth.Token {
	ctx := context.Background()

	cmd := s.client.B().Hget().Key(s.sessionKey(key.SessionID)).Field(fieldName(key)).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		if valkey.IsValkeyNil(err) {
			return nil
		}
		logging.Warn("OAuth", "ValkeyTokenStore: Get failed: %v", err)
		return nil
	}

	stored, err := result.ToString()
	if err != nil {
		return nil
	}

	plaintext, err := s.decryptValue(stored)
	if err != nil {
		logging.Warn("OAuth", "ValkeyTokenStore: decryption failed: %v", err)
		return nil
	}

	var entry valkeyTokenEntry
	if err := json.Unmarshal(plaintext, &entry); err != nil {
		logging.Warn("OAuth", "ValkeyTokenStore: unmarshal failed: %v", err)
		return nil
	}

	token := entryToToken(&entry)
	if token.IsExpiredWithMargin(tokenExpiryMargin) {
		return nil
	}
	return token
}

func (s *ValkeyTokenStore) GetByIssuer(sessionID, issuer string) *pkgoauth.Token {
	ctx := context.Background()

	cmd := s.client.B().Hgetall().Key(s.sessionKey(sessionID)).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		if valkey.IsValkeyNil(err) {
			return nil
		}
		logging.Warn("OAuth", "ValkeyTokenStore: GetByIssuer HGETALL failed: %v", err)
		return nil
	}

	m, err := result.AsStrMap()
	if err != nil || len(m) == 0 {
		return nil
	}

	prefix := issuer + valkeyTokenFieldSep
	var fallback *pkgoauth.Token
	for field, stored := range m {
		if !strings.HasPrefix(field, prefix) {
			continue
		}
		plaintext, err := s.decryptValue(stored)
		if err != nil {
			continue
		}
		var entry valkeyTokenEntry
		if err := json.Unmarshal(plaintext, &entry); err != nil {
			continue
		}
		token := entryToToken(&entry)
		if !token.IsExpiredWithMargin(tokenExpiryMargin) {
			if token.AccessToken != "" {
				return token
			}
			fallback = token
		}
	}
	return fallback
}

func (s *ValkeyTokenStore) GetAllForSession(sessionID string) map[TokenKey]*pkgoauth.Token {
	ctx := context.Background()

	cmd := s.client.B().Hgetall().Key(s.sessionKey(sessionID)).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		if !valkey.IsValkeyNil(err) {
			logging.Warn("OAuth", "ValkeyTokenStore: GetAllForSession failed: %v", err)
		}
		return nil
	}

	m, err := result.AsStrMap()
	if err != nil || len(m) == 0 {
		return nil
	}

	tokens := make(map[TokenKey]*pkgoauth.Token)
	for field, stored := range m {
		issuer, scope := parseFieldName(field)
		plaintext, err := s.decryptValue(stored)
		if err != nil {
			continue
		}
		var entry valkeyTokenEntry
		if err := json.Unmarshal(plaintext, &entry); err != nil {
			continue
		}
		token := entryToToken(&entry)
		if !token.IsExpiredWithMargin(tokenExpiryMargin) {
			tokens[TokenKey{SessionID: sessionID, Issuer: issuer, Scope: scope}] = token
		}
	}
	return tokens
}

func (s *ValkeyTokenStore) Delete(key TokenKey) {
	ctx := context.Background()

	cmd := s.client.B().Hdel().Key(s.sessionKey(key.SessionID)).Field(fieldName(key)).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		logging.Warn("OAuth", "ValkeyTokenStore: Delete failed: %v", err)
	}
}

func (s *ValkeyTokenStore) DeleteByUser(userID string) {
	ctx := context.Background()
	uKey := s.userKey(userID)

	cmd := s.client.B().Smembers().Key(uKey).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		if !valkey.IsValkeyNil(err) {
			logging.Warn("OAuth", "ValkeyTokenStore: DeleteByUser SMEMBERS failed: %v", err)
		}
		return
	}

	sessionIDs, err := result.AsStrSlice()
	if err != nil || len(sessionIDs) == 0 {
		return
	}

	cmds := make(valkey.Commands, 0, len(sessionIDs)+1)
	for _, sid := range sessionIDs {
		cmds = append(cmds, s.client.B().Del().Key(s.sessionKey(sid)).Build())
	}
	cmds = append(cmds, s.client.B().Del().Key(uKey).Build())

	for _, resp := range s.client.DoMulti(ctx, cmds...) {
		if err := resp.Error(); err != nil {
			logging.Warn("OAuth", "ValkeyTokenStore: DeleteByUser DEL failed: %v", err)
		}
	}

	logging.Debug("OAuth", "ValkeyTokenStore: deleted %d sessions for user=%s",
		len(sessionIDs), logging.TruncateIdentifier(userID))
}

func (s *ValkeyTokenStore) DeleteBySession(sessionID string) {
	ctx := context.Background()
	sKey := s.sessionKey(sessionID)

	// Read all entries to extract userIDs for user-index cleanup.
	cmd := s.client.B().Hgetall().Key(sKey).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err == nil {
		if m, err := result.AsStrMap(); err == nil {
			userIDs := make(map[string]struct{})
			for _, stored := range m {
				plaintext, err := s.decryptValue(stored)
				if err != nil {
					continue
				}
				var entry valkeyTokenEntry
				if err := json.Unmarshal(plaintext, &entry); err != nil {
					continue
				}
				if entry.UserID != "" {
					userIDs[entry.UserID] = struct{}{}
				}
			}
			for uid := range userIDs {
				remCmd := s.client.B().Srem().Key(s.userKey(uid)).Member(sessionID).Build()
				if err := s.client.Do(ctx, remCmd).Error(); err != nil {
					logging.Warn("OAuth", "ValkeyTokenStore: DeleteBySession SREM failed for user=%s: %v",
						logging.TruncateIdentifier(uid), err)
				}
			}
		}
	}

	delCmd := s.client.B().Del().Key(sKey).Build()
	if err := s.client.Do(ctx, delCmd).Error(); err != nil {
		logging.Warn("OAuth", "ValkeyTokenStore: DeleteBySession failed: %v", err)
	}
}

func (s *ValkeyTokenStore) DeleteByIssuer(sessionID, issuer string) {
	ctx := context.Background()
	sKey := s.sessionKey(sessionID)

	cmd := s.client.B().Hgetall().Key(sKey).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		if !valkey.IsValkeyNil(err) {
			logging.Warn("OAuth", "ValkeyTokenStore: DeleteByIssuer HGETALL failed: %v", err)
		}
		return
	}

	m, err := result.AsStrMap()
	if err != nil || len(m) == 0 {
		return
	}

	prefix := issuer + valkeyTokenFieldSep
	var fieldsToDelete []string
	for field := range m {
		if strings.HasPrefix(field, prefix) {
			fieldsToDelete = append(fieldsToDelete, field)
		}
	}

	if len(fieldsToDelete) == 0 {
		return
	}

	delCmd := s.client.B().Hdel().Key(sKey).Field(fieldsToDelete...).Build()
	if err := s.client.Do(ctx, delCmd).Error(); err != nil {
		logging.Warn("OAuth", "ValkeyTokenStore: DeleteByIssuer HDEL failed: %v", err)
	}

	logging.Debug("OAuth", "ValkeyTokenStore: deleted %d tokens for session=%s issuer=%s",
		len(fieldsToDelete), logging.TruncateIdentifier(sessionID), issuer)
}

// Count returns the total number of tokens across all sessions.
// This operation is bounded by a 10-second timeout to prevent Valkey overload.
// Intended for diagnostics only; avoid calling on hot paths.
func (s *ValkeyTokenStore) Count() int {
	ctx, cancel := context.WithTimeout(context.Background(), countTimeout)
	defer cancel()

	tokenPrefix := s.keyPrefix + "oauth:token:"
	userPrefix := s.keyPrefix + "oauth:token:user:"
	var total int

	var cursor uint64
	for {
		cmd := s.client.B().Scan().Cursor(cursor).Match(tokenPrefix + "*").Count(100).Build()
		result := s.client.Do(ctx, cmd)
		if err := result.Error(); err != nil {
			logging.Warn("OAuth", "ValkeyTokenStore: Count SCAN failed: %v", err)
			return total
		}

		entry, err := result.AsScanEntry()
		if err != nil {
			return total
		}

		for _, key := range entry.Elements {
			if strings.HasPrefix(key, userPrefix) {
				continue
			}
			hlenCmd := s.client.B().Hlen().Key(key).Build()
			hlenResult := s.client.Do(ctx, hlenCmd)
			if n, err := hlenResult.AsInt64(); err == nil {
				total += int(n)
			}
		}

		cursor = entry.Cursor
		if cursor == 0 {
			break
		}
	}
	return total
}

// Stop is a no-op for the Valkey implementation (no background goroutines).
// The Valkey client is closed separately during server shutdown.
func (s *ValkeyTokenStore) Stop() {}

// Ensure ValkeyTokenStore implements TokenStorer at compile time.
var _ TokenStorer = (*ValkeyTokenStore)(nil)

// Ensure the in-memory TokenStore also implements TokenStorer.
var _ TokenStorer = (*TokenStore)(nil)

// DefaultTokenStoreTTL is the session-level TTL for OAuth token entries in Valkey.
// Matching the capability store TTL (30 days) so tokens survive inactivity.
// Tokens self-expire based on their ExpiresAt; this TTL is only for Valkey key
// garbage collection of abandoned sessions.
const DefaultTokenStoreTTL = 30 * 24 * time.Hour
