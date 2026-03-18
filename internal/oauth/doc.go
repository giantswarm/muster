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
// By default, tokens are stored in-memory and are lost when the Muster Server
// restarts. When Valkey storage is configured (storage.type: valkey), tokens
// are persisted in Valkey, surviving pod restarts and enabling multi-pod
// deployments without sticky sessions.
//
// The TokenStorer and StateStorer interfaces allow swapping between in-memory
// (default) and Valkey-backed implementations via functional options on the
// Manager constructor.
//
// ## Encryption at Rest (Valkey)
//
// When using Valkey storage, token values (access tokens, refresh tokens, ID tokens,
// and OAuth state including code verifiers) are encrypted at rest using AES-256-GCM
// if an encryption key is configured (oauthServer.encryptionKey or encryptionKeyFile).
// The same key protects both the mcp-oauth server stores and the OAuth proxy stores.
//
// Production deployments using Valkey MUST:
//   - Configure an AES-256 encryption key (32 bytes, base64-encoded)
//   - Enable TLS for Valkey connections (storage.valkey.tlsEnabled: true)
//   - Use Valkey ACLs to restrict access to the muster key prefix
//   - Ensure the Valkey storage volume is encrypted (e.g., via StorageClass)
//
// Without encryption, a Valkey compromise (unauthorized access, RDB dump exfiltration,
// or replication snooping) exposes all stored tokens in plaintext.
//
// ## Session Isolation
//
// Tokens are stored with a composite key of (SessionID, Issuer, Scope), where SessionID
// is the mcp-oauth token family ID. Each login creates a new token family, so tokens are
// isolated per login session. Logout on device A does not affect device B's tokens.
// Each token entry also records the owning user ID (sub claim) for bulk operations
// like "sign out everywhere".
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
// The package supports Single Sign-On (SSO) through Token Forwarding and Token Exchange.
// When a user authenticates with muster, the token can be forwarded to downstream servers
// (Token Forwarding) or exchanged for a token valid on a remote IdP (Token Exchange).
package oauth
