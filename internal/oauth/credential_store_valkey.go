package oauth

import (
	"context"
	"encoding/json"

	"github.com/giantswarm/mcp-oauth/security"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// ValkeyClientCredentialStore stores DCR-issued client credentials in Valkey
// with optional AES-256-GCM encryption at rest. Sharing the store across
// replicas matters more here than for tokens: the authorization request and
// the callback's code exchange may land on different replicas, and both must
// resolve the same issuer-bound client_id.
//
// Data model:
//
//	Key:    {keyPrefix}oauth:client_creds
//	Fields: {issuer} -> [encrypted] JSON(pkgoauth.ClientCredentials)
//	TTL:    none — registrations represent muster itself and stay valid
//	        until the AS revokes them; expired secrets are dropped on read.
type ValkeyClientCredentialStore struct {
	valkeyEncryption
	client    valkey.Client
	keyPrefix string
}

// NewValkeyClientCredentialStore creates a Valkey-backed client credential
// store. keyPrefix is prepended to all Valkey keys (default "muster:" if
// empty). encryptor enables AES-256-GCM encryption at rest; pass nil to
// disable.
func NewValkeyClientCredentialStore(client valkey.Client, keyPrefix string, encryptor *security.Encryptor) *ValkeyClientCredentialStore {
	if keyPrefix == "" {
		keyPrefix = config.DefaultValkeyKeyPrefix
	}
	return &ValkeyClientCredentialStore{
		valkeyEncryption: valkeyEncryption{encryptor: encryptor},
		client:           client,
		keyPrefix:        keyPrefix,
	}
}

func (s *ValkeyClientCredentialStore) key() string {
	return s.keyPrefix + "oauth:client_creds"
}

// Get returns the credentials for the issuer, or nil if absent or expired.
func (s *ValkeyClientCredentialStore) Get(issuer string) *pkgoauth.ClientCredentials {
	ctx := context.Background()

	cmd := s.client.B().Hget().Key(s.key()).Field(issuer).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		if valkey.IsValkeyNil(err) {
			return nil
		}
		logging.Warn("OAuth", "ValkeyClientCredentialStore: Get failed: %v", err)
		return nil
	}

	stored, err := result.ToString()
	if err != nil {
		return nil
	}

	plaintext, err := s.decryptValue(stored)
	if err != nil {
		logging.Warn("OAuth", "ValkeyClientCredentialStore: decryption failed: %v", err)
		return nil
	}

	var creds pkgoauth.ClientCredentials
	if err := json.Unmarshal(plaintext, &creds); err != nil {
		logging.Warn("OAuth", "ValkeyClientCredentialStore: unmarshal failed: %v", err)
		return nil
	}

	if creds.IsExpired() {
		s.Delete(issuer)
		return nil
	}
	return &creds
}

// Store saves credentials for the issuer, replacing any previous entry.
func (s *ValkeyClientCredentialStore) Store(issuer string, creds *pkgoauth.ClientCredentials) {
	if creds == nil {
		return
	}
	ctx := context.Background()

	jsonData, err := json.Marshal(creds) //nolint:gosec
	if err != nil {
		logging.Warn("OAuth", "ValkeyClientCredentialStore: failed to marshal credentials: %v", err)
		return
	}

	value, err := s.encryptValue(jsonData)
	if err != nil {
		logging.Warn("OAuth", "ValkeyClientCredentialStore: failed to encrypt credentials: %v", err)
		return
	}

	cmd := s.client.B().Hset().Key(s.key()).FieldValue().FieldValue(issuer, value).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		logging.Warn("OAuth", "ValkeyClientCredentialStore: Store failed: %v", err)
		return
	}

	logging.Debug("OAuth", "ValkeyClientCredentialStore: stored client credentials for issuer=%s", issuer)
}

// Delete removes the credentials for the issuer.
func (s *ValkeyClientCredentialStore) Delete(issuer string) {
	ctx := context.Background()

	cmd := s.client.B().Hdel().Key(s.key()).Field(issuer).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		logging.Warn("OAuth", "ValkeyClientCredentialStore: Delete failed: %v", err)
	}
}

// Stop is a no-op; the Valkey client lifecycle is owned by the caller.
func (s *ValkeyClientCredentialStore) Stop() {}

// Ensure ValkeyClientCredentialStore implements ClientCredentialStorer.
var _ ClientCredentialStorer = (*ValkeyClientCredentialStore)(nil)
