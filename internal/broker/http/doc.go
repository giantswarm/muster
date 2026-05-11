// Package brokerhttp is the broker domain's HTTP driving adapter.
// It exposes the OAuth 2.1 / OIDC authorization server, CIMD endpoints,
// and the resource-server token-validation middleware that protects the
// aggregator's MCP transport. Aggregator code imports this package for
// the HTTP wiring; reaching into the broker domain directly is forbidden
// by the import-boundary rule.
package brokerhttp
