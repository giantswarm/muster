package api

import (
	"context"
	"sync"

	"muster/pkg/logging"
)

// ClientCredentials contains OAuth client credentials loaded from a secret.
type ClientCredentials struct {
	// ClientID is the OAuth client ID.
	ClientID string
	// ClientSecret is the OAuth client secret.
	ClientSecret string
}

// SecretCredentialsHandler defines the interface for loading OAuth client
// credentials from Kubernetes secrets. This is used for token exchange
// authentication with remote Dex instances.
//
// Implementations should:
//   - Load credentials from the specified Kubernetes secret
//   - Validate that required keys exist in the secret
//   - Handle missing secrets or keys gracefully with clear error messages
//
// Thread-safe: All methods must be safe for concurrent use.
type SecretCredentialsHandler interface {
	// LoadClientCredentials loads OAuth client credentials from a Kubernetes secret.
	//
	// The secret should contain:
	//   - clientIdKey: The OAuth client ID (defaults to "client-id")
	//   - clientSecretKey: The OAuth client secret (defaults to "client-secret")
	//
	// Args:
	//   - ctx: Context for Kubernetes API calls
	//   - secretRef: Reference to the secret containing credentials
	//   - defaultNamespace: Namespace to use if not specified in secretRef
	//
	// Returns:
	//   - *ClientCredentials: The loaded credentials
	//   - error: Error if the secret cannot be found or is missing required keys
	LoadClientCredentials(ctx context.Context, secretRef *ClientCredentialsSecretRef, defaultNamespace string) (*ClientCredentials, error)
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
