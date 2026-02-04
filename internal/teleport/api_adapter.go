package teleport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// Compile-time interface compliance check.
var _ api.TeleportClientHandler = (*Adapter)(nil)

// Adapter implements api.TeleportClientHandler to provide Teleport HTTP client
// functionality through the API service locator pattern.
//
// The adapter manages a registry of ClientProviders, one per identity configuration,
// allowing multiple MCP servers to use different Teleport identities if needed.
type Adapter struct {
	mu sync.RWMutex

	// k8sClient is an optional Kubernetes client for loading secrets.
	// When nil, only filesystem-based identity directories are supported.
	k8sClient client.Client

	// providers maps identity directory paths to their ClientProviders.
	// This allows sharing providers across MCP servers using the same identity.
	providers map[string]*ClientProvider

	// secretProviders maps secret names to their ClientProviders.
	// Key format: "namespace/secretName"
	// These providers use in-memory certificate loading for security.
	secretProviders map[string]*ClientProvider

	// defaultConfig holds default configuration values.
	defaultConfig TeleportConfig
}

// NewAdapter creates a new Teleport API adapter.
func NewAdapter() *Adapter {
	return &Adapter{
		providers:       make(map[string]*ClientProvider),
		secretProviders: make(map[string]*ClientProvider),
	}
}

// NewAdapterWithClient creates a new adapter with a Kubernetes client.
// This enables loading certificates from Kubernetes secrets.
func NewAdapterWithClient(k8sClient client.Client) *Adapter {
	return &Adapter{
		k8sClient:       k8sClient,
		providers:       make(map[string]*ClientProvider),
		secretProviders: make(map[string]*ClientProvider),
	}
}

// Register registers the adapter with the API service locator.
func (a *Adapter) Register() {
	api.RegisterTeleportClient(a)
	logging.Info("TeleportAdapter", "Registered Teleport client handler")
}

// GetHTTPClientForIdentity returns an HTTP client configured with Teleport certificates
// for the specified identity directory.
//
// This method is thread-safe and will create a new ClientProvider if one doesn't
// already exist for the given identity directory.
func (a *Adapter) GetHTTPClientForIdentity(identityDir string) (*http.Client, error) {
	provider, err := a.getOrCreateProvider(identityDir)
	if err != nil {
		return nil, err
	}

	return provider.GetHTTPClient()
}

// GetHTTPTransportForIdentity returns an HTTP transport configured with Teleport certificates.
func (a *Adapter) GetHTTPTransportForIdentity(identityDir string) (*http.Transport, error) {
	provider, err := a.getOrCreateProvider(identityDir)
	if err != nil {
		return nil, err
	}

	return provider.GetHTTPTransport()
}

// GetClientProvider returns the ClientProvider for an identity directory.
// This allows direct access to the provider for advanced use cases.
func (a *Adapter) GetClientProvider(identityDir string) (*ClientProvider, error) {
	return a.getOrCreateProvider(identityDir)
}

// getOrCreateProvider returns an existing provider or creates a new one.
func (a *Adapter) getOrCreateProvider(identityDir string) (*ClientProvider, error) {
	// Validate and sanitize the identity directory path
	cleanedDir, err := ValidateIdentityDir(identityDir)
	if err != nil {
		return nil, fmt.Errorf("invalid identity directory: %w", err)
	}

	// Try to get existing provider with read lock
	a.mu.RLock()
	provider, exists := a.providers[cleanedDir]
	a.mu.RUnlock()

	if exists {
		return provider, nil
	}

	// Need to create a new provider
	a.mu.Lock()
	defer a.mu.Unlock()

	// Double-check after acquiring write lock
	if provider, exists = a.providers[cleanedDir]; exists {
		return provider, nil
	}

	// Create new provider with watching enabled
	config := TeleportConfig{
		IdentityDir:   cleanedDir,
		WatchInterval: a.defaultConfig.WatchInterval,
		CertFile:      a.defaultConfig.CertFile,
		KeyFile:       a.defaultConfig.KeyFile,
		CAFile:        a.defaultConfig.CAFile,
	}

	// Apply defaults if not set
	if config.WatchInterval == 0 {
		config.WatchInterval = DefaultWatchInterval
	}
	if config.CertFile == "" {
		config.CertFile = DefaultCertFile
	}
	if config.KeyFile == "" {
		config.KeyFile = DefaultKeyFile
	}
	if config.CAFile == "" {
		config.CAFile = DefaultCAFile
	}

	provider, err = NewClientProviderWithWatching(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Teleport client provider: %w", err)
	}

	a.providers[cleanedDir] = provider
	logging.Info("TeleportAdapter", "Created Teleport client provider for identity: %s", cleanedDir)

	return provider, nil
}

// GetProviderStatus returns the certificate status for an identity directory.
func (a *Adapter) GetProviderStatus(identityDir string) (*CertStatus, error) {
	a.mu.RLock()
	provider, exists := a.providers[identityDir]
	a.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no provider registered for identity: %s", identityDir)
	}

	status := provider.GetStatus()
	return &status, nil
}

// ListProviders returns a list of all registered identity directories.
func (a *Adapter) ListProviders() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	dirs := make([]string, 0, len(a.providers))
	for dir := range a.providers {
		dirs = append(dirs, dir)
	}
	return dirs
}

// ReloadProvider forces a certificate reload for the specified identity.
func (a *Adapter) ReloadProvider(identityDir string) error {
	a.mu.RLock()
	provider, exists := a.providers[identityDir]
	a.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no provider registered for identity: %s", identityDir)
	}

	return provider.Reload()
}

// RemoveProvider stops and removes a provider for the specified identity.
func (a *Adapter) RemoveProvider(identityDir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	provider, exists := a.providers[identityDir]
	if !exists {
		return nil
	}

	if err := provider.Close(); err != nil {
		logging.Warn("TeleportAdapter", "Error closing provider for %s: %v", identityDir, err)
	}

	delete(a.providers, identityDir)
	logging.Info("TeleportAdapter", "Removed Teleport client provider for identity: %s", identityDir)

	return nil
}

// GetHTTPClientForConfig returns an HTTP client based on TeleportClientConfig.
// This method supports both filesystem identity directories and Kubernetes secrets.
//
// When IdentitySecretName is specified, certificates are loaded from the Kubernetes secret.
// Otherwise, certificates are loaded from IdentityDir.
//
// If AppName is specified, the returned client will have a custom transport that
// sets the appropriate Host header for Teleport application routing.
//
// Security validations performed:
//   - AppName is validated to prevent header injection
//   - IdentityDir is validated to prevent path traversal
//   - Secret namespace is validated against allowed list
func (a *Adapter) GetHTTPClientForConfig(ctx context.Context, config api.TeleportClientConfig) (*http.Client, error) {
	// Validate mutual exclusivity: only one of identityDir or identitySecretName can be specified
	if config.IdentityDir != "" && config.IdentitySecretName != "" {
		return nil, fmt.Errorf("identityDir and identitySecretName are mutually exclusive; specify only one")
	}

	// Validate AppName to prevent header injection
	if err := ValidateAppName(config.AppName); err != nil {
		return nil, fmt.Errorf("invalid app name: %w", err)
	}

	var provider *ClientProvider
	var err error

	if config.IdentitySecretName != "" {
		// Validate secret name format
		if err := ValidateSecretName(config.IdentitySecretName); err != nil {
			return nil, fmt.Errorf("invalid secret name: %w", err)
		}
		// Load certificates from Kubernetes secret (uses in-memory loading)
		provider, err = a.getOrCreateSecretProvider(ctx, config.IdentitySecretName, config.IdentitySecretNamespace)
	} else if config.IdentityDir != "" {
		// Use filesystem-based identity directory (path validation done in getOrCreateProvider)
		provider, err = a.getOrCreateProvider(config.IdentityDir)
	} else {
		return nil, fmt.Errorf("either identityDir or identitySecretName must be specified")
	}

	if err != nil {
		return nil, err
	}

	// Get the base HTTP client
	httpClient, err := provider.GetHTTPClient()
	if err != nil {
		return nil, err
	}

	// If AppName is specified, wrap the transport to add the Host header
	if config.AppName != "" {
		httpClient = a.wrapClientWithAppName(httpClient, config.AppName)
	}

	return httpClient, nil
}

// getOrCreateSecretProvider returns an existing provider or creates a new one from a Kubernetes secret.
// This method uses in-memory certificate loading to avoid writing sensitive private keys to disk.
func (a *Adapter) getOrCreateSecretProvider(ctx context.Context, secretName, namespace string) (*ClientProvider, error) {
	if a.k8sClient == nil {
		return nil, fmt.Errorf("Kubernetes client not available for secret-based identity")
	}

	if namespace == "" {
		namespace = "default"
	}

	// Validate namespace against allowed list
	if err := ValidateNamespace(namespace); err != nil {
		return nil, fmt.Errorf("namespace validation failed: %w", err)
	}

	key := fmt.Sprintf("%s/%s", namespace, secretName)

	// Try to get existing provider with read lock
	a.mu.RLock()
	provider, exists := a.secretProviders[key]
	a.mu.RUnlock()

	if exists {
		return provider, nil
	}

	// Need to create a new provider
	a.mu.Lock()
	defer a.mu.Unlock()

	// Double-check after acquiring write lock
	if provider, exists = a.secretProviders[key]; exists {
		return provider, nil
	}

	// Load secret from Kubernetes
	secret := &corev1.Secret{}
	if err := a.k8sClient.Get(ctx, client.ObjectKey{
		Name:      secretName,
		Namespace: namespace,
	}, secret); err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretName, err)
	}

	// Extract certificate data from secret
	certPEM, ok := secret.Data[DefaultCertFile]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s missing %s", namespace, secretName, DefaultCertFile)
	}
	keyPEM, ok := secret.Data[DefaultKeyFile]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s missing %s", namespace, secretName, DefaultKeyFile)
	}
	caPEM, ok := secret.Data[DefaultCAFile]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s missing %s", namespace, secretName, DefaultCAFile)
	}

	// Create provider with in-memory certificate loading
	// This is more secure than writing to temp files as private keys never touch the filesystem
	provider, err := NewClientProviderFromMemory(CertificateData{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
		CAPEM:   caPEM,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Teleport client provider from secret: %w", err)
	}

	a.secretProviders[key] = provider
	logging.Info("TeleportAdapter", "Created Teleport client provider from secret: %s (in-memory)", key)

	return provider, nil
}

// wrapClientWithAppName wraps an HTTP client to add the Host header for Teleport app routing.
func (a *Adapter) wrapClientWithAppName(httpClient *http.Client, appName string) *http.Client {
	originalTransport := httpClient.Transport
	if originalTransport == nil {
		originalTransport = http.DefaultTransport
	}

	return &http.Client{
		Transport: &appNameTransport{
			base:    originalTransport,
			appName: appName,
		},
		Timeout: httpClient.Timeout,
	}
}

// appNameTransport wraps an http.RoundTripper to add the Teleport app Host header.
type appNameTransport struct {
	base    http.RoundTripper
	appName string
}

// RoundTrip implements http.RoundTripper.
func (t *appNameTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	reqCopy := req.Clone(req.Context())

	// Set the Host header for Teleport application routing
	// The app name is used as-is since Teleport uses the Host header
	// to route to the correct application
	if t.appName != "" {
		reqCopy.Host = t.appName
	}

	return t.base.RoundTrip(reqCopy)
}

// Close stops all providers and releases resources.
// If multiple errors occur during cleanup, they are aggregated using errors.Join.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	var errs []error

	// Close filesystem-based providers
	for dir, provider := range a.providers {
		if err := provider.Close(); err != nil {
			logging.Warn("TeleportAdapter", "Error closing provider for %s: %v", dir, err)
			errs = append(errs, fmt.Errorf("provider %s: %w", dir, err))
		}
	}

	// Close secret-based providers (these use in-memory certs, no temp files to clean)
	for key, provider := range a.secretProviders {
		if err := provider.Close(); err != nil {
			logging.Warn("TeleportAdapter", "Error closing secret provider for %s: %v", key, err)
			errs = append(errs, fmt.Errorf("secret provider %s: %w", key, err))
		}
	}

	a.providers = make(map[string]*ClientProvider)
	a.secretProviders = make(map[string]*ClientProvider)
	logging.Info("TeleportAdapter", "Closed all Teleport client providers")

	return errors.Join(errs...)
}
