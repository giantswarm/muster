// Package server provides OIDC ID-token context helpers used by the
// aggregator and the broker's HTTP driving adapter to pass the
// authenticated user's ID token between request-handling layers.
//
// The helpers are deliberately decoupled from any specific OAuth
// implementation: the consumer (broker HTTP middleware) puts the ID token
// onto the context after token validation; the producer (downstream MCP
// transport, token exchanger) reads it for SSO forwarding or RFC 8693
// token exchange.
package server
