// Package brokerhttp is the broker domain's HTTP driving adapter.
//
// It exposes the OAuth 2.1 / OIDC authorization server, CIMD endpoints, and
// the resource-server token-validation middleware that protects the
// aggregator's MCP transport. The package wraps the broker domain
// (internal/broker) and the upstream mcp-oauth library; aggregator code only
// imports this package for the HTTP wiring and never reaches into the
// domain directly.
//
// The package is named brokerhttp rather than http to avoid shadowing
// net/http inside its own files.
package brokerhttp
