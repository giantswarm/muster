package mcpserver

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"muster/internal/api"
	"muster/pkg/logging"
)

// CredentialsAdapter implements the SecretCredentialsHandler interface
// for loading OAuth client credentials from Kubernetes secrets.
type CredentialsAdapter struct {
	client client.Client
}

// DefaultClientIDKey is the default key for client ID in the secret.
const DefaultClientIDKey = "client-id"

// DefaultClientSecretKey is the default key for client secret in the secret.
const DefaultClientSecretKey = "client-secret"

// NewCredentialsAdapter creates a new credentials adapter.
//
// Args:
//   - k8sClient: The Kubernetes client for reading secrets
//
// Returns:
//   - *CredentialsAdapter: The adapter instance
func NewCredentialsAdapter(k8sClient client.Client) *CredentialsAdapter {
	return &CredentialsAdapter{
		client: k8sClient,
	}
}

// Register registers the adapter with the API.
func (a *CredentialsAdapter) Register() {
	api.RegisterSecretCredentialsHandler(a)
}

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
//   - *api.ClientCredentials: The loaded credentials
//   - error: Error if the secret cannot be found or is missing required keys
func (a *CredentialsAdapter) LoadClientCredentials(
	ctx context.Context,
	secretRef *api.ClientCredentialsSecretRef,
	defaultNamespace string,
) (*api.ClientCredentials, error) {
	if secretRef == nil {
		return nil, fmt.Errorf("secret reference is nil")
	}
	if secretRef.Name == "" {
		return nil, fmt.Errorf("secret name is required")
	}

	// Determine namespace (use default if not specified)
	namespace := secretRef.Namespace
	if namespace == "" {
		namespace = defaultNamespace
	}
	if namespace == "" {
		namespace = "default"
	}

	// Determine keys (use defaults if not specified)
	clientIDKey := secretRef.ClientIDKey
	if clientIDKey == "" {
		clientIDKey = DefaultClientIDKey
	}
	clientSecretKey := secretRef.ClientSecretKey
	if clientSecretKey == "" {
		clientSecretKey = DefaultClientSecretKey
	}

	logging.Debug("SecretCredentials", "Loading client credentials from secret %s/%s (keys: %s, %s)",
		namespace, secretRef.Name, clientIDKey, clientSecretKey)

	// Security: Log a warning when accessing secrets across namespaces
	// This helps operators audit cross-namespace secret access in logs
	if secretRef.Namespace != "" && secretRef.Namespace != defaultNamespace {
		logging.Warn("SecretCredentials", "Cross-namespace secret access: reading secret %s/%s from MCPServer in namespace %s. "+
			"Ensure RBAC policies permit this access and review security implications.",
			namespace, secretRef.Name, defaultNamespace)
	}

	// Load the secret from Kubernetes
	secret := &corev1.Secret{}
	if err := a.client.Get(ctx, client.ObjectKey{
		Name:      secretRef.Name,
		Namespace: namespace,
	}, secret); err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretRef.Name, err)
	}

	// Extract client ID
	clientIDBytes, ok := secret.Data[clientIDKey]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s missing required key '%s'", namespace, secretRef.Name, clientIDKey)
	}
	clientID := string(clientIDBytes)
	if clientID == "" {
		return nil, fmt.Errorf("secret %s/%s has empty value for key '%s'", namespace, secretRef.Name, clientIDKey)
	}

	// Extract client secret
	clientSecretBytes, ok := secret.Data[clientSecretKey]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s missing required key '%s'", namespace, secretRef.Name, clientSecretKey)
	}
	clientSecret := string(clientSecretBytes)
	if clientSecret == "" {
		return nil, fmt.Errorf("secret %s/%s has empty value for key '%s'", namespace, secretRef.Name, clientSecretKey)
	}

	logging.Debug("SecretCredentials", "Successfully loaded client credentials from secret %s/%s (client_id=%s)",
		namespace, secretRef.Name, clientID)

	return &api.ClientCredentials{
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}, nil
}
