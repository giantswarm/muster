package teleport

import (
	"fmt"
	"net/http"
	"sync"

	"muster/internal/api"
	"muster/pkg/logging"
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

	// providers maps identity directory paths to their ClientProviders.
	// This allows sharing providers across MCP servers using the same identity.
	providers map[string]*ClientProvider

	// defaultConfig holds default configuration values.
	defaultConfig TeleportConfig
}

// NewAdapter creates a new Teleport API adapter.
func NewAdapter() *Adapter {
	return &Adapter{
		providers: make(map[string]*ClientProvider),
	}
}

// NewAdapterWithDefaults creates a new adapter with default configuration.
func NewAdapterWithDefaults(defaultConfig TeleportConfig) *Adapter {
	return &Adapter{
		providers:     make(map[string]*ClientProvider),
		defaultConfig: defaultConfig,
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
	// Try to get existing provider with read lock
	a.mu.RLock()
	provider, exists := a.providers[identityDir]
	a.mu.RUnlock()

	if exists {
		return provider, nil
	}

	// Need to create a new provider
	a.mu.Lock()
	defer a.mu.Unlock()

	// Double-check after acquiring write lock
	if provider, exists = a.providers[identityDir]; exists {
		return provider, nil
	}

	// Create new provider with watching enabled
	config := TeleportConfig{
		IdentityDir:   identityDir,
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

	provider, err := NewClientProviderWithWatching(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Teleport client provider: %w", err)
	}

	a.providers[identityDir] = provider
	logging.Info("TeleportAdapter", "Created Teleport client provider for identity: %s", identityDir)

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

// Close stops all providers and releases resources.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	var lastErr error
	for dir, provider := range a.providers {
		if err := provider.Close(); err != nil {
			logging.Warn("TeleportAdapter", "Error closing provider for %s: %v", dir, err)
			lastErr = err
		}
	}

	a.providers = make(map[string]*ClientProvider)
	logging.Info("TeleportAdapter", "Closed all Teleport client providers")

	return lastErr
}
