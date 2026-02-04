package teleport

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
)

// ClientProvider implements HTTPClientProvider and provides HTTP clients
// configured with Teleport Machine ID certificates for accessing private
// installations via Teleport Application Access.
type ClientProvider struct {
	mu sync.RWMutex

	// config holds the Teleport configuration
	config TeleportConfig

	// tlsConfig is the current TLS configuration
	tlsConfig *tls.Config

	// status tracks the current certificate status
	status CertStatus

	// watcher monitors certificates for changes (may be nil if watching is disabled)
	watcher *CertWatcher

	// httpClient is the cached HTTP client (recreated when certs change)
	httpClient *http.Client

	// callbacks are called when certificates are reloaded
	callbacks []CertReloadCallback
}

// NewClientProvider creates a new Teleport client provider with the given configuration.
// It loads the initial certificates and optionally starts watching for changes.
//
// The config.IdentityDir must contain the certificate files (or config.IdentitySecretName
// must be set for Kubernetes mode). If certificate files are not immediately available,
// the provider will be created in an unloaded state and will attempt to load certificates
// when GetHTTPClient is called.
func NewClientProvider(config TeleportConfig) (*ClientProvider, error) {
	// Apply defaults
	if config.CertFile == "" {
		config.CertFile = DefaultCertFile
	}
	if config.KeyFile == "" {
		config.KeyFile = DefaultKeyFile
	}
	if config.CAFile == "" {
		config.CAFile = DefaultCAFile
	}
	if config.WatchInterval == 0 {
		config.WatchInterval = DefaultWatchInterval
	}

	provider := &ClientProvider{
		config:    config,
		callbacks: make([]CertReloadCallback, 0),
		status: CertStatus{
			CertPath: filepath.Join(config.IdentityDir, config.CertFile),
			KeyPath:  filepath.Join(config.IdentityDir, config.KeyFile),
			CAPath:   filepath.Join(config.IdentityDir, config.CAFile),
		},
	}

	// Try to load certificates initially
	if err := provider.loadCertificates(); err != nil {
		logging.Warn("TeleportClient", "Initial certificate load failed: %v (will retry on first request)", err)
		// Don't fail - certs might become available later
	}

	return provider, nil
}

// NewClientProviderWithWatching creates a new Teleport client provider that watches
// for certificate changes and automatically reloads them.
func NewClientProviderWithWatching(config TeleportConfig) (*ClientProvider, error) {
	provider, err := NewClientProvider(config)
	if err != nil {
		return nil, err
	}

	// Start watching for certificate changes
	if err := provider.StartWatching(); err != nil {
		return nil, fmt.Errorf("failed to start certificate watching: %w", err)
	}

	return provider, nil
}

// CertificateData holds certificate data loaded from memory.
// This is used for in-memory certificate loading from Kubernetes secrets,
// avoiding the need to write sensitive data to temporary files.
type CertificateData struct {
	// CertPEM is the client certificate in PEM format.
	CertPEM []byte
	// KeyPEM is the client private key in PEM format.
	KeyPEM []byte
	// CAPEM is the CA certificate in PEM format.
	CAPEM []byte
}

// NewClientProviderFromMemory creates a new Teleport client provider with certificates
// loaded directly from memory. This avoids writing sensitive private key data to disk.
//
// This is the preferred method for loading certificates from Kubernetes secrets,
// as it eliminates the security risk of temporary files containing private keys.
func NewClientProviderFromMemory(certData CertificateData) (*ClientProvider, error) {
	provider := &ClientProvider{
		callbacks: make([]CertReloadCallback, 0),
		status: CertStatus{
			CertPath: "(in-memory)",
			KeyPath:  "(in-memory)",
			CAPath:   "(in-memory)",
		},
	}

	// Load certificates from memory
	if err := provider.loadCertificatesFromMemory(certData); err != nil {
		return nil, fmt.Errorf("failed to load certificates from memory: %w", err)
	}

	return provider, nil
}

// loadCertificatesFromMemory loads TLS certificates from in-memory PEM data.
func (p *ClientProvider) loadCertificatesFromMemory(certData CertificateData) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.loadCertificatesFromMemoryLocked(certData)
}

// loadCertificatesFromMemoryLocked loads certificates from memory without acquiring the lock.
// Caller must hold the write lock.
func (p *ClientProvider) loadCertificatesFromMemoryLocked(certData CertificateData) error {
	// Validate input
	if len(certData.CertPEM) == 0 {
		return fmt.Errorf("certificate PEM data is empty")
	}
	if len(certData.KeyPEM) == 0 {
		return fmt.Errorf("private key PEM data is empty")
	}
	if len(certData.CAPEM) == 0 {
		return fmt.Errorf("CA certificate PEM data is empty")
	}

	// Load client certificate and key from PEM data
	cert, err := tls.X509KeyPair(certData.CertPEM, certData.KeyPEM)
	if err != nil {
		p.status.LastError = fmt.Errorf("failed to parse client certificate: %w", err)
		return p.status.LastError
	}

	// Parse certificate to get expiry
	if len(cert.Certificate) > 0 {
		if parsed, parseErr := x509.ParseCertificate(cert.Certificate[0]); parseErr == nil {
			p.status.ExpiresAt = &parsed.NotAfter
		}
	}

	// Create CA certificate pool
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(certData.CAPEM) {
		p.status.LastError = fmt.Errorf("failed to parse CA certificate")
		return p.status.LastError
	}

	// Create TLS config
	p.tlsConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}

	// Update status
	p.status.Loaded = true
	p.status.LastLoaded = time.Now()
	p.status.LastError = nil

	// Invalidate cached HTTP client (will be recreated on next GetHTTPClient call)
	p.httpClient = nil

	logging.Info("TeleportClient", "Loaded Teleport certificates from memory")

	return nil
}

// loadCertificates loads or reloads the TLS certificates from disk.
func (p *ClientProvider) loadCertificates() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.loadCertificatesLocked()
}

// loadCertificatesLocked loads certificates without acquiring the lock.
// Caller must hold the write lock.
func (p *ClientProvider) loadCertificatesLocked() error {
	certPath := p.status.CertPath
	keyPath := p.status.KeyPath
	caPath := p.status.CAPath

	// Load client certificate and key
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		p.status.LastError = fmt.Errorf("failed to load client certificate: %w", err)
		return p.status.LastError
	}

	// Parse certificate to get expiry
	if len(cert.Certificate) > 0 {
		if parsed, parseErr := x509.ParseCertificate(cert.Certificate[0]); parseErr == nil {
			p.status.ExpiresAt = &parsed.NotAfter
		}
	}

	// Load CA certificate
	caCert, err := os.ReadFile(caPath)
	if err != nil {
		p.status.LastError = fmt.Errorf("failed to read CA certificate: %w", err)
		return p.status.LastError
	}

	// Create CA certificate pool
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		p.status.LastError = fmt.Errorf("failed to parse CA certificate")
		return p.status.LastError
	}

	// Create TLS config
	p.tlsConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}

	// Update status
	p.status.Loaded = true
	p.status.LastLoaded = time.Now()
	p.status.LastError = nil

	// Invalidate cached HTTP client (will be recreated on next GetHTTPClient call)
	p.httpClient = nil

	logging.Info("TeleportClient", "Loaded Teleport certificates from %s", p.config.IdentityDir)

	return nil
}

// GetHTTPClient returns an HTTP client configured with Teleport certificates.
// The client uses mutual TLS with the Teleport CA for trust.
func (p *ClientProvider) GetHTTPClient() (*http.Client, error) {
	p.mu.RLock()
	if p.httpClient != nil {
		client := p.httpClient
		p.mu.RUnlock()
		return client, nil
	}
	p.mu.RUnlock()

	// Need to create a new client
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if p.httpClient != nil {
		return p.httpClient, nil
	}

	// If certificates aren't loaded, try to load them
	if !p.status.Loaded {
		if err := p.loadCertificatesLocked(); err != nil {
			return nil, fmt.Errorf("certificates not available: %w", err)
		}
	}

	// Create transport with TLS config
	transport := &http.Transport{
		TLSClientConfig: p.tlsConfig,
		// Connection pool settings for better performance
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	p.httpClient = &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	return p.httpClient, nil
}

// GetHTTPTransport returns an HTTP transport configured with Teleport certificates.
// This is useful when you need to customize the client further.
func (p *ClientProvider) GetHTTPTransport() (*http.Transport, error) {
	tlsConfig, err := p.GetTLSConfig()
	if err != nil {
		return nil, err
	}

	return &http.Transport{
		TLSClientConfig: tlsConfig,
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
	}, nil
}

// GetTLSConfig returns the current TLS configuration.
func (p *ClientProvider) GetTLSConfig() (*tls.Config, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if !p.status.Loaded {
		return nil, fmt.Errorf("certificates not loaded")
	}

	// Return a clone to prevent external modification
	return p.tlsConfig.Clone(), nil
}

// GetStatus returns the current certificate status.
func (p *ClientProvider) GetStatus() CertStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.status
}

// Reload forces a reload of the certificates from disk.
func (p *ClientProvider) Reload() error {
	err := p.loadCertificates()

	// Notify callbacks
	p.mu.RLock()
	callbacks := make([]CertReloadCallback, len(p.callbacks))
	copy(callbacks, p.callbacks)
	tlsConfig := p.tlsConfig
	p.mu.RUnlock()

	for _, cb := range callbacks {
		cb(tlsConfig, err)
	}

	return err
}

// OnReload registers a callback that is invoked when certificates are reloaded.
func (p *ClientProvider) OnReload(callback CertReloadCallback) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.callbacks = append(p.callbacks, callback)
}

// StartWatching starts watching for certificate file changes.
// When changes are detected, certificates are automatically reloaded.
func (p *ClientProvider) StartWatching() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.watcher != nil {
		return nil // Already watching
	}

	watcher, err := NewCertWatcher(CertWatcherConfig{
		IdentityDir:   p.config.IdentityDir,
		CertFile:      p.config.CertFile,
		KeyFile:       p.config.KeyFile,
		CAFile:        p.config.CAFile,
		WatchInterval: p.config.WatchInterval,
		OnChange: func() {
			logging.Info("TeleportClient", "Certificate change detected, reloading")
			if reloadErr := p.Reload(); reloadErr != nil {
				logging.Error("TeleportClient", reloadErr, "Failed to reload certificates")
			}
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create certificate watcher: %w", err)
	}

	if err := watcher.Start(); err != nil {
		return fmt.Errorf("failed to start certificate watcher: %w", err)
	}

	p.watcher = watcher
	logging.Info("TeleportClient", "Started watching certificates in %s", p.config.IdentityDir)

	return nil
}

// StopWatching stops the certificate file watcher.
func (p *ClientProvider) StopWatching() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.watcher == nil {
		return nil
	}

	if err := p.watcher.Stop(); err != nil {
		return err
	}

	p.watcher = nil
	return nil
}

// Close releases resources and stops any background watchers.
func (p *ClientProvider) Close() error {
	return p.StopWatching()
}

// IsLoaded returns whether certificates are currently loaded and valid.
func (p *ClientProvider) IsLoaded() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.status.Loaded
}

// IsExpiringSoon returns whether the certificate will expire within the given duration.
func (p *ClientProvider) IsExpiringSoon(threshold time.Duration) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.status.ExpiresAt == nil {
		return false
	}

	return time.Until(*p.status.ExpiresAt) < threshold
}
