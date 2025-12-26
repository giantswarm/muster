// Package oauth implements OAuth 2.1 client authentication for the Muster Agent.
//
// This package provides secure token storage and SSO capabilities for the Agent
// when connecting to OAuth-protected Muster Servers and remote MCP servers.
//
// # Architecture
//
// The Agent OAuth client provides:
//   - XDG-compliant token storage with issuer-based lookup for SSO
//   - Token retrieval by issuer URL for cross-server SSO
//   - PKCE-enhanced Authorization Code Flow support
//   - Local HTTP callback listener for OAuth redirects
//   - Automatic token refresh and retry logic
//
// # Token Storage
//
// Tokens are stored in XDG-compliant location:
//
//	~/.config/muster/tokens/{server-hash}.json
//
// Each token file contains both the server URL and issuer URL, enabling:
//   - Direct lookup by server URL (GetToken)
//   - Issuer-based lookup for SSO (GetByIssuer)
//
// # SSO Support
//
// The TokenStore supports Single Sign-On through issuer-based lookup.
// When a remote MCP server requires authentication with an issuer the
// Agent has already authenticated to, the existing token can be reused
// via GetByIssuer().
//
// # Usage
//
//	// Create token store
//	store, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
//	    StorageDir: "~/.config/muster/tokens",
//	    FileMode:   true,
//	})
//
//	// Get token by server URL
//	token := store.GetToken(serverURL)
//
//	// Get token by issuer for SSO
//	token := store.GetByIssuer(issuerURL)
package oauth
