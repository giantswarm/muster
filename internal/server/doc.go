// Package server provides OAuth 2.1 protection for the Muster Server.
//
// This package implements ADR 005 (OAuth Protection for Muster Server), allowing
// the Muster Server to act as an OAuth Resource Server. When enabled, all MCP
// endpoints require valid access tokens from authenticated clients.
//
// # Architecture
//
// The server package integrates with the mcp-oauth library to provide:
//   - OAuth 2.1 server with mandatory PKCE
//   - Dynamic client registration (RFC 7591)
//   - Client ID Metadata Documents (CIMD) per MCP 2025-11-25 spec
//   - Token validation middleware for protecting MCP endpoints
//   - Multiple provider support (Dex OIDC, Google OAuth)
//   - Token storage backends (in-memory, Valkey/Redis)
//
// # Integration
//
// The OAuth server wraps the existing aggregator HTTP handler, adding
// authentication and authorization before requests reach MCP endpoints.
//
//	┌─────────────────────────────────────────────────────────────┐
//	│                      Muster Server                          │
//	│                                                             │
//	│  [ OAuth Middleware (Resource Server) ]                     │
//	│       Validates Token from Agent                            │
//	│               │                                             │
//	│               ▼                                             │
//	│  [ Aggregator / Tool Handler ]                              │
//	│               │                                             │
//	│               ▼                                             │
//	│  [ OAuth Proxy (Client) ]                                   │
//	│       Injects Token for Remote MCPs                         │
//	└─────────────────────────────────────────────────────────────┘
//
// # Usage
//
// To enable OAuth server protection, configure the aggregator with:
//
//	aggregator:
//	  oauthServer:
//	    enabled: true
//	    baseUrl: "https://muster.example.com"
//	    provider: "dex"
//	    dex:
//	      issuerUrl: "https://dex.example.com"
//	      clientId: "muster-server"
//	      clientSecret: "${DEX_CLIENT_SECRET}"
//
// # Endpoints
//
// When OAuth server is enabled, the following endpoints are exposed:
//
//   - /.well-known/oauth-authorization-server - Authorization Server Metadata (RFC 8414)
//   - /.well-known/oauth-protected-resource - Protected Resource Metadata (RFC 9728)
//   - /oauth/register - Dynamic Client Registration (RFC 7591)
//   - /oauth/authorize - OAuth Authorization
//   - /oauth/token - Token Endpoint
//   - /oauth/callback - OAuth Callback (from IdP)
//   - /oauth/revoke - Token Revocation (RFC 7009)
//   - /mcp - Protected MCP endpoint (requires Bearer token)
package server
