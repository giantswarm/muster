package oauth

import (
	"sync"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// ClientCredentialStorer is the interface for issuer-keyed OAuth client
// credentials obtained via Dynamic Client Registration (RFC 7591).
// Implementations must be safe for concurrent use.
//
// Per SEP-2352 credentials are bound to the issuer they were registered
// with; there is deliberately no way to look up credentials without naming
// the issuer.
type ClientCredentialStorer interface {
	// Get returns the credentials registered with the given issuer, or nil
	// when none are stored or the stored secret has expired.
	Get(issuer string) *pkgoauth.ClientCredentials

	// Store saves credentials for the issuer, replacing any previous entry.
	Store(issuer string, creds *pkgoauth.ClientCredentials)

	// Delete removes the credentials for the issuer (e.g. after the AS
	// stopped accepting them, so the next flow re-registers).
	Delete(issuer string)

	// Stop releases resources (background goroutines, connections, etc.).
	Stop()
}

// ClientCredentialStore provides thread-safe in-memory storage for
// DCR-issued client credentials, keyed by issuer. Registrations represent
// muster itself (not a user or session), so there is one entry per
// authorization server. Losing the store on restart is harmless: the next
// auth flow simply registers again.
type ClientCredentialStore struct {
	mu    sync.RWMutex
	creds map[string]*pkgoauth.ClientCredentials
}

// NewClientCredentialStore creates a new in-memory client credential store.
func NewClientCredentialStore() *ClientCredentialStore {
	return &ClientCredentialStore{
		creds: make(map[string]*pkgoauth.ClientCredentials),
	}
}

// Get returns the credentials for the issuer, or nil if absent or expired.
func (s *ClientCredentialStore) Get(issuer string) *pkgoauth.ClientCredentials {
	s.mu.RLock()
	creds := s.creds[issuer]
	s.mu.RUnlock()

	if creds == nil {
		return nil
	}
	if creds.IsExpired() {
		s.Delete(issuer)
		return nil
	}
	return creds
}

// Store saves credentials for the issuer, replacing any previous entry.
func (s *ClientCredentialStore) Store(issuer string, creds *pkgoauth.ClientCredentials) {
	if creds == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creds[issuer] = creds
}

// Delete removes the credentials for the issuer.
func (s *ClientCredentialStore) Delete(issuer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.creds, issuer)
}

// Stop is a no-op for the in-memory store.
func (s *ClientCredentialStore) Stop() {}

// Ensure ClientCredentialStore implements ClientCredentialStorer.
var _ ClientCredentialStorer = (*ClientCredentialStore)(nil)
