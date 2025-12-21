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
// ## TLS Requirements
//
// Production deployments MUST use HTTPS for:
//   - The Muster Server's public URL (for OAuth callbacks)
//   - All OAuth issuer URLs (for metadata and token endpoints)
//
// TLS provides the integrity and authenticity guarantees that protect OAuth flows
// from man-in-the-middle attacks.
//
// # SSO Support
//
// The package supports Single Sign-On (SSO) through token reuse. When a user
// authenticates with an Identity Provider, the token is stored indexed by
// (SessionID, Issuer, Scope). Subsequent requests to different MCP servers
// sharing the same IdP configuration can reuse the existing token.
package oauth
