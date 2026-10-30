package mcpserver

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// CredentialsAdapter implements the SecretCredentialsHandler interface
// for loading OAuth client credentials from Kubernetes secrets.
type CredentialsAdapter struct {
	client client.Client
	// binding decides which Secrets an MCPServer may read (secret_binding.go).
	binding *secretBinding
	// policy, when set, refuses every read for an MCPServer while the
	// admission policy is not in force (credentials_policy.go).
	policy *secretReferencePolicy
}

// DefaultClientIDKey is the default key for client ID in the secret.
const DefaultClientIDKey = "client-id"

// DefaultClientSecretKey is the default key for client secret in the secret.
const DefaultClientSecretKey = "client-secret"

// NewCredentialsAdapter creates a new credentials adapter.
//
// Args:
//   - k8sClient: The Kubernetes client for reading secrets
//   - opts: secret binding options (WithProtectedSecrets, WithSecretReferencePolicy)
//
// Returns:
//   - *CredentialsAdapter: The adapter instance
func NewCredentialsAdapter(k8sClient client.Client, opts ...CredentialsOption) *CredentialsAdapter {
	a := &CredentialsAdapter{
		client:  k8sClient,
		binding: newSecretBinding(),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Register registers the adapter with the API.
func (a *CredentialsAdapter) Register() {
	api.RegisterSecretCredentialsHandler(a)
}

// LoadClientCredentialsForConfig loads OAuth client credentials from a Secret
// muster's configuration names. No binding is checked.
func (a *CredentialsAdapter) LoadClientCredentialsForConfig(
	ctx context.Context,
	secretRef *api.ClientCredentialsSecretRef,
	defaultNamespace string,
) (*api.ClientCredentials, error) {
	return a.loadClientCredentials(ctx, secretRef, defaultNamespace, nil)
}

// LoadMCPServerClientCredentials loads OAuth client credentials from a Secret
// an MCPServer's spec names, if the Secret may be read on its behalf.
func (a *CredentialsAdapter) LoadMCPServerClientCredentials(
	ctx context.Context,
	owner api.SecretOwner,
	field string,
	secretRef *api.ClientCredentialsSecretRef,
) (*api.ClientCredentials, error) {
	return a.loadClientCredentials(ctx, secretRef, owner.Namespace, &secretRequest{owner: owner, field: field})
}

// loadClientCredentials reads the client ID and secret of a Secret. A non-nil
// req is a read on behalf of an MCPServer and is checked against the binding
// rules before the values are returned.
//
// The secret should contain:
//   - clientIdKey: The OAuth client ID (defaults to "client-id")
//   - clientSecretKey: The OAuth client secret (defaults to "client-secret")
func (a *CredentialsAdapter) loadClientCredentials(
	ctx context.Context,
	secretRef *api.ClientCredentialsSecretRef,
	defaultNamespace string,
	req *secretRequest,
) (*api.ClientCredentials, error) {
	if secretRef == nil {
		return nil, fmt.Errorf("secret reference is nil")
	}
	if secretRef.Name == "" {
		return nil, fmt.Errorf("secret name is required")
	}

	ownNamespace, namespace := resolveSecretNamespace(secretRef, defaultNamespace)

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
	if namespace != ownNamespace {
		logging.Warn("SecretCredentials", "Cross-namespace secret access: reading secret %s/%s from MCPServer in namespace %s. "+
			"Ensure RBAC policies permit this access and review security implications.",
			namespace, secretRef.Name, ownNamespace)
	}

	secret, err := a.getSecret(ctx, namespace, secretRef.Name, req)
	if err != nil {
		return nil, err
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

// resolveSecretNamespace returns the namespace a secret reference is read
// relative to and the namespace it resolves to.
//
// ownNamespace is the referencing resource's own namespace (the MCPServer's
// metadata.namespace, or a broker's configured default), falling back to
// "default" when the caller has none. namespace is where the Secret is read
// from: the reference's explicit namespace, else ownNamespace. The read is
// cross-namespace exactly when the two differ — a reference that names its
// own namespace explicitly is not.
func resolveSecretNamespace(secretRef *api.ClientCredentialsSecretRef, defaultNamespace string) (ownNamespace, namespace string) {
	ownNamespace = defaultNamespace
	if ownNamespace == "" {
		ownNamespace = metav1.NamespaceDefault
	}
	namespace = secretRef.Namespace
	if namespace == "" {
		namespace = ownNamespace
	}
	return ownNamespace, namespace
}

// LoadMCPServerSecretKey loads raw bytes for a single named key from a Secret
// an MCPServer's spec names, if the Secret may be read on its behalf.
func (a *CredentialsAdapter) LoadMCPServerSecretKey(
	ctx context.Context,
	owner api.SecretOwner,
	field string,
	secretRef *api.ClientCredentialsSecretRef,
	key string,
) ([]byte, error) {
	defaultNamespace := owner.Namespace
	if secretRef == nil {
		return nil, fmt.Errorf("secret reference is nil")
	}
	if secretRef.Name == "" {
		return nil, fmt.Errorf("secret name is required")
	}

	ownNamespace, namespace := resolveSecretNamespace(secretRef, defaultNamespace)

	logging.Debug("SecretCredentials", "Loading secret key %q from secret %s/%s", key, namespace, secretRef.Name)

	if namespace != ownNamespace {
		logging.Warn("SecretCredentials", "Cross-namespace secret access: reading secret %s/%s from namespace %s. "+
			"Ensure RBAC policies permit this access and review security implications.",
			namespace, secretRef.Name, ownNamespace)
	}

	secret, err := a.getSecret(ctx, namespace, secretRef.Name, &secretRequest{owner: owner, field: field})
	if err != nil {
		return nil, err
	}

	data, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s missing required key %q", namespace, secretRef.Name, key)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("secret %s/%s has empty value for key %q", namespace, secretRef.Name, key)
	}

	return data, nil
}

// getSecret reads a Secret. A non-nil req is a read on behalf of an MCPServer:
// a protected Secret, or any Secret while the admission policy is not in
// force, is refused before it is read, one of a reserved type, or one with a
// generated client name the MCPServer does not own, after it.
func (a *CredentialsAdapter) getSecret(ctx context.Context, namespace, name string, req *secretRequest) (*corev1.Secret, error) {
	if req != nil {
		if err := a.binding.checkProtected(req, namespace, name); err != nil {
			return nil, err
		}
		if a.policy != nil {
			if err := a.policy.check(ctx, a.client); err != nil {
				return nil, fmt.Errorf("MCPServer %s may not read Secret %s/%s named in %s: %w",
					req.owner, namespace, name, req.field, err)
			}
		}
	}
	secret := &corev1.Secret{}
	if err := a.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret); err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, name, err)
	}
	if req != nil {
		if err := a.binding.checkReserved(req, namespace, secret); err != nil {
			return nil, err
		}
		if err := checkGeneratedOwner(req, namespace, secret); err != nil {
			a.binding.warnOnce(req, namespace, name, err.Error(), "%v", err)
			return nil, err
		}
	}
	return secret, nil
}
