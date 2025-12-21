// Package oauth implements OAuth 2.1 proxy functionality for remote MCP server authentication.
//
// This package provides the server-side OAuth client and proxy implementation that allows
// the Muster Server to authenticate with remote MCP servers on behalf of users without
// exposing sensitive tokens to the Muster Agent.
//
// # Architecture
//
// The OAuth proxy follows a three-legged OAuth 2.1 Authorization Code flow:
//
//  1. User requests a tool that requires authentication
//  2. Muster Server detects 401 Unauthorized from the remote MCP server
//  3. Muster Server generates an authorization URL and returns "Auth Required" challenge
//  4. User authenticates via their browser with the Identity Provider
//  5. Browser redirects to Muster Server's callback endpoint with authorization code
//  6. Muster Server exchanges code for tokens and stores them securely
//  7. User retries the original request, which now succeeds with the stored token
//
// # Components
//
//   - TokenStore: In-memory storage for OAuth tokens, indexed by session and issuer
//   - StateStore: Manages OAuth state parameters for CSRF protection
//   - Client: Handles OAuth flows, code exchange, and token refresh
//   - Handler: HTTP handler for the /oauth/callback endpoint
//   - Manager: Coordinates OAuth flows and integrates with the aggregator
//
// # Security
//
// ## Token Storage
//
// Tokens are stored in-memory only and are never persisted to disk. This means:
//   - Tokens are lost when the Muster Server restarts
//   - Users must re-authenticate after a restart
//   - No encryption-at-rest is needed since tokens exist only in process memory
//
// For persistent token storage in future versions, encryption would be required.
//
// ## Session Isolation
//
// Each MCP connection (SSE or Streamable HTTP) receives a unique UUID-based session ID
// from the mcp-go library. Tokens are stored with a composite key of (SessionID, Issuer, Scope),
// ensuring complete isolation between users. User A cannot access User B's tokens.
//
// For stdio transport (single-user CLI), a default session ID is used. This is acceptable
// since stdio is inherently single-user (one process = one user).
//
// ## TLS/HTTPS Requirements (CRITICAL)
//
// Production deployments MUST use HTTPS for all OAuth-related endpoints:
//
//   - Muster Server's public URL (oauth.publicUrl configuration): The OAuth callback
//     endpoint receives authorization codes. Without HTTPS, attackers could intercept
//     these codes and exchange them for tokens.
//
//   - OAuth Issuer URLs: All communication with Identity Providers (metadata discovery,
//     token exchange, token refresh) must be over HTTPS. The issuer's TLS certificate
//     provides integrity and authenticity guarantees for OAuth metadata.
//
//   - Remote MCP Server URLs: When MCP servers require OAuth authentication, their
//     endpoints should use HTTPS to protect the bearer tokens in Authorization headers.
//
// Without TLS, the following attacks become possible:
//   - Authorization code interception during OAuth callback
//   - Token theft via man-in-the-middle attacks
//   - Metadata manipulation to redirect token exchanges to malicious endpoints
//   - Bearer token theft from Authorization headers
//
// ## Rate Limiting Recommendations
//
// The OAuth callback endpoint (/oauth/callback by default) should be protected by
// rate limiting at the infrastructure level (ingress controller, load balancer, or
// API gateway). Recommended limits:
//
//   - Per-IP rate limit: 10-20 requests per minute
//   - Global rate limit: 100-500 requests per minute (depending on expected user base)
//
// Rate limiting protects against:
//   - Denial of service attacks on the OAuth callback endpoint
//   - Brute-force attempts to guess authorization codes or state parameters
//   - Resource exhaustion from excessive token exchange requests
//
// Example Kubernetes Ingress annotation (nginx):
//
//	nginx.ingress.kubernetes.io/limit-rps: "10"
//	nginx.ingress.kubernetes.io/limit-connections: "5"
//
// ## Logging Security
//
// Session IDs are truncated in log output to prevent full session identifiers from
// appearing in logs. Only the first 8 characters are logged (e.g., "abc12345...").
// Access tokens and refresh tokens are never logged.
//
// Token refresh operations are logged at INFO level for operational monitoring,
// including duration metrics for performance tracking.
//
// # SSO Support
//
// The package supports Single Sign-On (SSO) through token reuse. When a user
// authenticates with an Identity Provider, the token is stored indexed by
// (SessionID, Issuer, Scope). Subsequent requests to different MCP servers
// sharing the same IdP configuration can reuse the existing token.
package oauth
