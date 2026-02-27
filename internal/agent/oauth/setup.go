package oauth

import (
	"fmt"

	"github.com/mark3labs/mcp-go/client/transport"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// SetupOAuthConfig creates an AgentTokenStore and returns the OAuthConfig for
// use with mcp-go's WithHTTPOAuth / WithOAuth transport options.
// This is the standard way to configure agent/CLI clients for OAuth authentication.
//
// The returned AgentTokenStore wraps the file-based token store at ~/.config/muster/tokens/,
// so tokens stored by `muster auth login` are automatically picked up by the transport.
func SetupOAuthConfig(serverURL string) (*transport.OAuthConfig, *AgentTokenStore, error) {
	return SetupOAuthConfigWithDir(serverURL, "")
}

// SetupOAuthConfigWithDir creates an AgentTokenStore with a custom storage directory.
// If tokenStorageDir is empty, defaults to ~/.config/muster/tokens/.
func SetupOAuthConfigWithDir(serverURL, tokenStorageDir string) (*transport.OAuthConfig, *AgentTokenStore, error) {
	if tokenStorageDir == "" {
		var err error
		tokenStorageDir, err = pkgoauth.DefaultTokenDir()
		if err != nil {
			return nil, nil, err
		}
	}

	tokenStore, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tokenStorageDir,
		FileMode:   true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create token store: %w", err)
	}

	normalizedURL := pkgoauth.NormalizeServerURL(serverURL)
	agentStore := NewAgentTokenStore(normalizedURL, tokenStore)

	config := &transport.OAuthConfig{
		ClientID:    DefaultAgentClientID,
		TokenStore:  agentStore,
		Scopes:      agentOAuthScopes,
		PKCEEnabled: true,
	}

	return config, agentStore, nil
}
