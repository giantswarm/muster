package api

import (
	"context"
	"sync"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// ClientCredentials contains OAuth client credentials loaded from a secret.
type ClientCredentials struct {
	// ClientID is the OAuth client ID.
	ClientID string
	// ClientSecret is the OAuth client secret.
	ClientSecret string
}

// SecretCredentialsHandler defines the interface for loading OAuth client
// credentials and other values from Kubernetes secrets.
//
// A read on behalf of an MCPServer -- a Secret its spec names -- goes through
// the methods that take a SecretOwner: they read only a Secret bound to that
// MCPServer (MCPServerSecretLabel or an ownerReference) and never one of
// muster's own (secretBinding). LoadClientCredentialsForConfig is for Secrets
// muster's own configuration names and must never be called with a reference
// taken from an MCPServer.
//
// Thread-safe: All methods must be safe for concurrent use.
type SecretCredentialsHandler interface {
	// LoadClientCredentialsForConfig loads OAuth client credentials from a
	// Secret muster's configuration names (broker targets and clients,
	// provider Apps). No binding is checked.
	//
	// The secret should contain clientIdKey (default "client-id") and
	// clientSecretKey (default "client-secret"). defaultNamespace is used
	// when secretRef names none.
	LoadClientCredentialsForConfig(ctx context.Context, secretRef *ClientCredentialsSecretRef, defaultNamespace string) (*ClientCredentials, error)

	// LoadMCPServerClientCredentials loads OAuth client credentials from a
	// Secret the MCPServer owner names in field (SecretField*). The Secret
	// must be bound to owner (or secretBinding.mode is warn) and must not be
	// protected; otherwise nothing is returned. A reference without a
	// namespace resolves to owner.Namespace.
	LoadMCPServerClientCredentials(ctx context.Context, owner SecretOwner, field string, secretRef *ClientCredentialsSecretRef) (*ClientCredentials, error)

	// LoadMCPServerSecretKey loads raw bytes for a single named key from a
	// Secret the MCPServer owner names in field, with the binding rules of
	// LoadMCPServerClientCredentials. Callers are responsible for not
	// logging the returned value. ClientIDKey/ClientSecretKey of secretRef
	// are ignored.
	LoadMCPServerSecretKey(ctx context.Context, owner SecretOwner, field string, secretRef *ClientCredentialsSecretRef, key string) ([]byte, error)
}

// secretCredentialsHandler stores the registered handler implementation.
var secretCredentialsHandler SecretCredentialsHandler
var secretCredentialsMutex sync.RWMutex

// RegisterSecretCredentialsHandler registers the secret credentials handler implementation.
// This handler loads OAuth client credentials from Kubernetes secrets.
//
// The registration is thread-safe and should be called during system initialization.
// Only one handler can be registered at a time; subsequent registrations will
// replace the previous handler.
//
// Args:
//   - h: SecretCredentialsHandler implementation
//
// Thread-safe: Yes, protected by secretCredentialsMutex.
func RegisterSecretCredentialsHandler(h SecretCredentialsHandler) {
	secretCredentialsMutex.Lock()
	defer secretCredentialsMutex.Unlock()
	logging.Debug("API", "Registering secret credentials handler: %v", h != nil)
	secretCredentialsHandler = h
}

// GetSecretCredentialsHandler returns the registered secret credentials handler.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - SecretCredentialsHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by secretCredentialsMutex read lock.
func GetSecretCredentialsHandler() SecretCredentialsHandler {
	secretCredentialsMutex.RLock()
	defer secretCredentialsMutex.RUnlock()
	return secretCredentialsHandler
}
