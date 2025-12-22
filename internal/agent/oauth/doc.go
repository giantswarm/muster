// Package oauth implements OAuth 2.1 client authentication for the Muster Agent.
//
// This package implements ADR 005 (OAuth Protection for Muster Server), enabling
// the Agent to authenticate with OAuth-protected Muster Servers. When the Agent
// connects to a protected server and receives a 401 response, it enters a
// "pending auth" state and exposes a synthetic `authenticate_muster` tool.
//
// # Architecture
//
// The Agent OAuth client provides:
//   - 401 detection during SSE/Streamable-HTTP connections
//   - WWW-Authenticate header parsing to discover authorization servers
//   - PKCE-enhanced Authorization Code Flow for secure authentication
//   - Local HTTP callback listener for OAuth redirects
//   - XDG-compliant token storage with optional encryption
//   - Automatic token refresh and retry logic
//
// # Authentication Flow
//
//  1. Agent attempts to connect to protected Muster Server
//  2. Server responds with 401 and WWW-Authenticate header
//  3. Agent enters "pending auth" state, exposes synthetic tool
//  4. User calls `authenticate_muster` tool, receives auth URL
//  5. User authenticates via browser -> IdP -> callback
//  6. Agent exchanges code for tokens, stores them securely
//  7. Agent retries connection with Bearer token
//  8. On success, Agent replaces synthetic tool with real tools
//
// # Token Storage
//
// Tokens are stored in XDG-compliant location:
//
//	~/.config/muster/tokens/{server-hash}.json
//
// Token files are encrypted at rest using AES-256-GCM when an encryption
// key is provided. The key can be derived from system keychain or user password.
//
// # Usage
//
//	// Create OAuth client for agent
//	client := oauth.NewClient(oauth.ClientConfig{
//	    CallbackPort: 3000,
//	    StorageDir:   "~/.config/muster/tokens",
//	})
//
//	// Get or initiate auth for a server
//	token, err := client.GetToken(ctx, serverURL)
//	if err == oauth.ErrAuthRequired {
//	    // Need to run auth flow
//	    authURL, err := client.StartAuthFlow(ctx, serverURL, issuerURL)
//	    // Show authURL to user...
//	}
package oauth
