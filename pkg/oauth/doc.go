// Package oauth provides shared OAuth 2.1 types and utilities used by both
// the Muster Agent and Server.
//
// This package contains the common OAuth functionality that is shared between
// the agent-side OAuth implementation (internal/agent/oauth) and the server-side
// OAuth implementation (internal/oauth).
//
// # Core Components
//
//   - Token: OAuth token representation with expiry checking
//   - Metadata: OAuth/OIDC server metadata (RFC 8414)
//   - AuthChallenge: Parsed WWW-Authenticate header information
//   - PKCE: Proof Key for Code Exchange generation (RFC 7636)
//   - Client: OAuth client for metadata discovery and token operations
//
// # Usage
//
// Both the agent and server import this package for shared types and utilities,
// then wrap it with their specific storage backends and UI handling.
//
// Agent usage (file-based storage, browser opening):
//
//	import "muster/pkg/oauth"
//
//	challenge, err := oauth.ParseWWWAuthenticate(header)
//	verifier, challengeStr, err := oauth.GeneratePKCE()
//
// Server usage (in-memory storage, HTTP callbacks):
//
//	import "muster/pkg/oauth"
//
//	client := oauth.NewClient(httpClient, logger)
//	metadata, err := client.DiscoverMetadata(ctx, issuer)
package oauth
