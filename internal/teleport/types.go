package teleport

import (
	"crypto/tls"
	"net/http"
	"time"
)

// TeleportConfig holds configuration for Teleport authentication.
// This configuration is typically populated from MCPServer auth.teleport settings.
type TeleportConfig struct {
	// IdentityDir is the directory containing Teleport identity files.
	// In filesystem mode, this is the tbot output directory.
	// In Kubernetes mode, this is where the identity secret is mounted.
	IdentityDir string

	// IdentitySecretName is the name of the Kubernetes Secret containing
	// tbot identity files. Used when running in Kubernetes mode.
	IdentitySecretName string

	// Namespace is the Kubernetes namespace where the identity secret is located.
	// Defaults to "default" if not specified.
	Namespace string

	// AppName is the Teleport application name for routing.
	// This is used to identify which application the client is connecting to.
	AppName string

	// WatchInterval is how often to check for certificate changes.
	// Defaults to 30 seconds if not specified.
	WatchInterval time.Duration

	// CertFile is the path to the client certificate file (relative to IdentityDir).
	// Defaults to "tlscert" if not specified (matching tbot's application output).
	CertFile string

	// KeyFile is the path to the client private key file (relative to IdentityDir).
	// Defaults to "key" if not specified (matching tbot's application output).
	KeyFile string

	// CAFile is the path to the CA certificate file (relative to IdentityDir).
	// Defaults to "teleport-application-ca.pem" if not specified (matching tbot's application output).
	CAFile string
}

// DefaultCertFile is the default filename for the client certificate.
// This matches tbot's application output type which produces "tlscert".
const DefaultCertFile = "tlscert"

// DefaultKeyFile is the default filename for the client private key.
// This matches tbot's application output type which produces "key".
const DefaultKeyFile = "key"

// DefaultCAFile is the default filename for the CA certificate.
// This matches tbot's application output type which produces "teleport-application-ca.pem".
const DefaultCAFile = "teleport-application-ca.pem"

// DefaultWatchInterval is the default interval for checking certificate changes.
const DefaultWatchInterval = 30 * time.Second

// CertReloadCallback is a function called when certificates are reloaded.
// It receives the new TLS config and any error that occurred during reload.
type CertReloadCallback func(config *tls.Config, err error)

// HTTPClientProvider defines the interface for providing HTTP clients
// configured with Teleport authentication.
type HTTPClientProvider interface {
	// GetHTTPClient returns an HTTP client configured with Teleport certificates.
	// The client uses mutual TLS with the Teleport CA for trust.
	GetHTTPClient() (*http.Client, error)

	// GetHTTPTransport returns an HTTP transport configured with Teleport certificates.
	// This is useful when you need to customize the client further.
	GetHTTPTransport() (*http.Transport, error)

	// GetTLSConfig returns the current TLS configuration.
	// This can be used to verify certificate status or for custom integrations.
	GetTLSConfig() (*tls.Config, error)

	// Close releases resources and stops any background watchers.
	Close() error
}

// CertStatus represents the current status of the Teleport certificates.
type CertStatus struct {
	// Loaded indicates whether certificates are successfully loaded.
	Loaded bool

	// CertPath is the full path to the client certificate file.
	CertPath string

	// KeyPath is the full path to the client private key file.
	KeyPath string

	// CAPath is the full path to the CA certificate file.
	CAPath string

	// LastLoaded is when the certificates were last successfully loaded.
	LastLoaded time.Time

	// LastError is the most recent error, if any.
	LastError error

	// ExpiresAt is when the client certificate expires (if parseable).
	ExpiresAt *time.Time
}
