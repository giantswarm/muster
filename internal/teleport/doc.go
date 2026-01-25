// Package teleport provides a Teleport Client Provider for accessing MCP servers
// on private installations that are only reachable via Teleport Application Access.
//
// # Overview
//
// Private installations are deployed in networks without public endpoints and can
// only be accessed through Teleport Application Access. This package provides the
// HTTP client infrastructure that uses Teleport Machine ID certificates (tbot)
// to authenticate and connect to these private MCP servers.
//
// The core principle is: OAuth for identity, Teleport for access.
// User identity remains managed through OAuth/Dex, while Teleport provides
// the network-level access to private endpoints.
//
// # Architecture
//
// The package follows the API service locator pattern used throughout muster.
// It provides:
//
//   - ClientProvider: Creates HTTP clients configured with Teleport TLS certificates
//   - CertWatcher: Monitors certificate files for changes and triggers reload
//   - API Adapter: Integrates with the muster API service locator
//
// # Certificate Management
//
// Teleport Machine ID (tbot) outputs identity files that are used to authenticate:
//
//   - tls.crt: Client certificate
//   - tls.key: Client private key
//   - ca.crt: Teleport CA certificate for trust
//
// In Kubernetes mode, these files are mounted from a Secret. In filesystem mode,
// they can be read directly from the tbot output directory.
//
// The CertWatcher component monitors these files for changes (when tbot renews
// certificates) and gracefully refreshes the HTTP client without connection
// interruption.
//
// # Usage
//
// MCPServers can be configured to use Teleport authentication:
//
//	apiVersion: muster.giantswarm.io/v1alpha1
//	kind: MCPServer
//	metadata:
//	  name: remote-mcp-kubernetes
//	spec:
//	  type: streamable-http
//	  url: https://mcp-kubernetes.teleport.example.com/mcp
//	  auth:
//	    type: teleport
//	    teleport:
//	      identitySecretName: tbot-identity-output
//	      appName: mcp-kubernetes
//
// # Integration Points
//
//   - MCPServer Controller: Detects auth.type: teleport and configures the HTTP client
//   - API Package: TeleportClientProvider registered as a handler
//   - Aggregator: Uses Teleport-configured HTTP client for protected servers
//
// # Security Considerations
//
//   - Teleport identity files contain sensitive private keys
//   - In Kubernetes, files are mounted from Secrets with appropriate RBAC
//   - Certificate rotation is managed by tbot; this package only reloads
//   - OAuth tokens continue to provide user identity for authorization
package teleport
