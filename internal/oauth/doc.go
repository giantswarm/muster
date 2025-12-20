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
//   - SessionManager: Manages user sessions and links them to token storage
//   - OAuthClient: Handles OAuth flows, code exchange, and token refresh
//   - CallbackHandler: HTTP handler for the /oauth/callback endpoint
//
// # Security
//
// Tokens are stored in memory on the server and never sent to the Muster Agent.
// Sessions are linked via a session ID header (X-Muster-Session-ID) that the Agent
// generates on startup.
//
// # SSO Support
//
// The package supports Single Sign-On (SSO) through token reuse. When a user
// authenticates with an Identity Provider, the token is stored indexed by
// (SessionID, Issuer, Scope). Subsequent requests to different MCP servers
// sharing the same IdP configuration can reuse the existing token.
package oauth
