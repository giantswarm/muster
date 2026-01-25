package api

import "net/http"

// TeleportClientHandler defines the interface for providing HTTP clients
// configured with Teleport Machine ID certificates.
//
// This handler enables access to MCP servers on private installations that are
// only reachable via Teleport Application Access. The handler manages TLS
// certificate loading, hot-reloading, and HTTP client lifecycle.
//
// Implementations should:
//   - Load TLS certificates from the specified identity directory
//   - Monitor certificates for changes and reload automatically
//   - Provide HTTP clients configured with mutual TLS
//
// Thread-safe: All methods must be safe for concurrent use.
type TeleportClientHandler interface {
	// GetHTTPClientForIdentity returns an HTTP client configured with Teleport
	// certificates from the specified identity directory.
	//
	// The identity directory should contain:
	//   - tls.crt: Client certificate
	//   - tls.key: Client private key
	//   - ca.crt: Teleport CA certificate
	//
	// The returned client uses mutual TLS and trusts the Teleport CA.
	//
	// Args:
	//   - identityDir: Path to the directory containing Teleport identity files
	//
	// Returns:
	//   - *http.Client: HTTP client configured with Teleport certificates
	//   - error: Error if certificates cannot be loaded or are invalid
	GetHTTPClientForIdentity(identityDir string) (*http.Client, error)

	// GetHTTPTransportForIdentity returns an HTTP transport configured with
	// Teleport certificates. This is useful when you need to customize the
	// HTTP client further or share the transport across multiple clients.
	//
	// Args:
	//   - identityDir: Path to the directory containing Teleport identity files
	//
	// Returns:
	//   - *http.Transport: HTTP transport configured with Teleport certificates
	//   - error: Error if certificates cannot be loaded or are invalid
	GetHTTPTransportForIdentity(identityDir string) (*http.Transport, error)
}

// TeleportAuth configures Teleport authentication for an MCP server.
// This enables access to MCP servers on private installations via Teleport
// Application Access using Machine ID certificates.
type TeleportAuth struct {
	// IdentityDir is the directory containing Teleport identity files.
	// In filesystem mode, this is the tbot output directory.
	// In Kubernetes mode, this is where the identity secret is mounted.
	// Example: /var/run/tbot/identity
	IdentityDir string `yaml:"identityDir,omitempty" json:"identityDir,omitempty"`

	// IdentitySecretName is the name of the Kubernetes Secret containing
	// tbot identity files. Used when running in Kubernetes mode.
	// The secret should contain: tls.crt, tls.key, ca.crt
	// Example: tbot-identity-output
	IdentitySecretName string `yaml:"identitySecretName,omitempty" json:"identitySecretName,omitempty"`

	// IdentitySecretNamespace is the Kubernetes namespace where the identity
	// secret is located. Defaults to the MCPServer's namespace if not specified.
	IdentitySecretNamespace string `yaml:"identitySecretNamespace,omitempty" json:"identitySecretNamespace,omitempty"`

	// AppName is the Teleport application name for routing.
	// This is used to identify which Teleport-protected application to connect to.
	// Example: mcp-kubernetes
	AppName string `yaml:"appName,omitempty" json:"appName,omitempty"`
}

// AuthTypeTeleport is the auth type value for Teleport authentication.
const AuthTypeTeleport = "teleport"
