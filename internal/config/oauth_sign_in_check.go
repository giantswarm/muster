package config

import (
	"fmt"
	"net/url"
)

// ValidateSignInCheckOrigin refuses an OAuth proxy public URL that is not on
// the origin of muster's OAuth server. The start endpoint's sign-in check
// returns the browser to <publicURL>/oauth/proxy/start, and the OAuth server
// sends an identity sign-in back only to its own origin: with the two apart,
// every sign-in to a server would fail.
func ValidateSignInCheckOrigin(publicURL, issuerURL string) error {
	public, err := url.Parse(publicURL)
	if err != nil {
		return fmt.Errorf("oauth.mcpClient.publicUrl %q is not a valid URL: %w", publicURL, err)
	}
	issuer, err := url.Parse(issuerURL)
	if err != nil {
		return fmt.Errorf("oauth.server.baseUrl %q is not a valid URL: %w", issuerURL, err)
	}
	if public.Scheme != issuer.Scheme || public.Host != issuer.Host {
		return fmt.Errorf("oauth.mcpClient.publicUrl %q must be on the origin of oauth.server.baseUrl %q: the OAuth proxy start endpoint checks who signs in through muster's OAuth server",
			publicURL, issuerURL)
	}
	return nil
}
