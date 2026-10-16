package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"

	"github.com/giantswarm/muster/v5/internal/agent/oauth"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// defaultRevocationPath is where mcp-oauth serves RFC 7009 revocation below
// its issuer. It is used only for a server whose metadata does not advertise
// revocation_endpoint.
const defaultRevocationPath = "/oauth/revoke"

// revokeStoredToken revokes the refresh and the access token of a stored
// session at the revocation endpoint the authorization server advertises in
// its RFC 8414 metadata (RFC 7009). The refresh token goes first, so the
// session cannot be renewed even when the access token's revocation fails.
// Every failure is returned; none of them stops the other revocation.
func (a *AuthAdapter) revokeStoredToken(endpoint string, token *oauth.StoredToken) error {
	if token == nil || (token.RefreshToken == "" && token.AccessToken == "") {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), DefaultHTTPClientTimeout)
	defer cancel()

	revokeURL, err := resolveRevocationEndpoint(ctx, endpoint, token.IssuerURL)
	if err != nil {
		return err
	}

	var errs []error
	for _, t := range []struct{ value, hint string }{
		{token.RefreshToken, "refresh_token"},
		{token.AccessToken, "access_token"},
	} {
		if t.value == "" {
			continue
		}
		if err := revokeToken(revokeURL, token.ClientID, t.value, t.hint); err != nil {
			errs = append(errs, fmt.Errorf("revoke %s: %w", t.hint, err))
			continue
		}
		logging.Info("AuthAdapter", "Revoked %s for %s at %s", t.hint, endpoint, revokeURL)
	}
	return errors.Join(errs...)
}

// resolveRevocationEndpoint returns the revocation endpoint of the
// authorization server that issued the session's tokens. The issuer is the
// one stored with the token; a token stored without one is taken to be
// issued by the endpoint's origin. The endpoint is never used as a base for
// the path: an MCP endpoint (".../mcp") is not where the authorization
// server lives.
func resolveRevocationEndpoint(ctx context.Context, endpoint, issuer string) (string, error) {
	if issuer == "" {
		origin, err := originOf(endpoint)
		if err != nil {
			return "", err
		}
		issuer = origin
	}

	metadata, err := pkgoauth.NewClient(pkgoauth.WithHTTPClient(&http.Client{Timeout: DefaultHTTPClientTimeout})).
		DiscoverMetadata(ctx, issuer)
	if err != nil {
		return "", fmt.Errorf("discover the revocation endpoint of %s: %w", issuer, err)
	}
	if metadata.RevocationEndpoint != "" {
		return metadata.RevocationEndpoint, nil
	}

	// A server that predates advertising revocation_endpoint still serves it
	// at mcp-oauth's fixed path below the issuer.
	fallback := strings.TrimSuffix(issuer, "/") + defaultRevocationPath
	logging.Debug("AuthAdapter", "%s advertises no revocation_endpoint, using %s", issuer, fallback)
	return fallback, nil
}

// originOf returns the scheme and host of rawURL.
func originOf(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid endpoint %q", rawURL)
	}
	return u.Scheme + "://" + u.Host, nil
}

// revokeToken posts one RFC 7009 revocation request. A public client
// identifies itself with client_id in the form. RFC 7009 answers 200 for a
// revoked and for an unknown token alike; any other answer is a failure.
func revokeToken(revokeURL, clientID, token, tokenTypeHint string) error {
	form := url.Values{}
	form.Set("token", token)
	form.Set("token_type_hint", tokenTypeHint)
	if clientID != "" {
		form.Set("client_id", clientID)
	}

	req, err := http.NewRequest(http.MethodPost, revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: DefaultHTTPClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("server unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered status %d", revokeURL, resp.StatusCode)
	}
	return nil
}
