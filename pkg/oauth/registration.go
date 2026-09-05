package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxErrorBody bounds how much of an error response body is read when only
// the OAuth error object matters.
const maxErrorBody = 64 << 10

// TokenEndpointError is an error response from a token endpoint (RFC 6749
// §5.2). Code and Description are empty when the response body carried no
// OAuth error object; StatusCode is always set.
type TokenEndpointError struct {
	StatusCode  int
	Code        string
	Description string
}

// Error renders the same text the untyped error used to carry, so log lines
// and messages built from it are unchanged.
func (e *TokenEndpointError) Error() string {
	switch {
	case e.Code != "" && e.Description != "":
		return fmt.Sprintf("token request failed: %s - %s", e.Code, e.Description)
	case e.Code != "":
		return fmt.Sprintf("token request failed: %s", e.Code)
	default:
		return fmt.Sprintf("token request failed with status %d", e.StatusCode)
	}
}

// IsInvalidClientError reports whether err is a token endpoint response with
// the error code invalid_client: the authorization server no longer accepts
// the client identification that was presented, as opposed to rejecting the
// grant (invalid_grant) or the request shape (invalid_request).
func IsInvalidClientError(err error) bool {
	var tokenErr *TokenEndpointError
	return errors.As(err, &tokenErr) && tokenErr.Code == ErrInvalidClient
}

// ErrBadRefreshToken is GitHub's error code for a refresh token that is
// expired, revoked or already rotated away (RFC 6749 servers answer
// invalid_grant for the same condition).
const ErrBadRefreshToken = "bad_refresh_token"

// IsRefreshTokenRejected reports whether the token endpoint refused a refresh
// because the refresh token itself is no longer good: RFC 6749 invalid_grant,
// or GitHub's bad_refresh_token. Anything else -- a network failure, a 5xx, an
// invalid_client -- says nothing about the grant and is not a rejection.
func IsRefreshTokenRejected(err error) bool {
	var tokenErr *TokenEndpointError
	if !errors.As(err, &tokenErr) {
		return false
	}
	return tokenErr.Code == ErrInvalidGrant || tokenErr.Code == ErrBadRefreshToken
}

// RegistrationStatus is the outcome of asking an authorization server whether
// it still recognizes a client registration.
type RegistrationStatus int

const (
	// RegistrationUnknown means the check could not tell: the request failed,
	// or the server answered in a way that names neither outcome. Callers
	// keep the credentials they have.
	RegistrationUnknown RegistrationStatus = iota

	// RegistrationActive means the server confirmed it still knows the client.
	RegistrationActive

	// RegistrationGone means the server no longer knows the client. Credentials
	// tied to it will be refused; the client has to register again.
	RegistrationGone
)

// String returns the status name for log lines.
func (s RegistrationStatus) String() string {
	switch s {
	case RegistrationActive:
		return "active"
	case RegistrationGone:
		return "gone"
	default:
		return "unknown"
	}
}

// ReadClientRegistration performs the RFC 7592 §2.1 client read request:
// GET registration_client_uri with the registration_access_token as bearer
// credential. A 200 confirms the registration; RFC 7592 §2.3 requires a 401
// when the token is invalid or the client no longer exists, which is the
// signal that the credentials are dead. Every other answer (403, 404, 5xx,
// transport errors) is inconclusive, because it can as well mean the server
// stopped serving the endpoint.
func (c *Client) ReadClientRegistration(ctx context.Context, registrationClientURI, registrationAccessToken string) (RegistrationStatus, error) {
	if registrationClientURI == "" || registrationAccessToken == "" {
		return RegistrationUnknown, fmt.Errorf("registration_client_uri and registration_access_token are both required for an RFC 7592 read")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registrationClientURI, nil)
	if err != nil {
		return RegistrationUnknown, fmt.Errorf("failed to create registration read request: %w", err)
	}
	req.Header.Set(HeaderAuthorization, SchemeBearer+" "+registrationAccessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return RegistrationUnknown, fmt.Errorf("registration read request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))

	switch resp.StatusCode {
	case http.StatusOK:
		return RegistrationActive, nil
	case http.StatusUnauthorized:
		return RegistrationGone, nil
	default:
		return RegistrationUnknown, fmt.Errorf("registration read answered status %d", resp.StatusCode)
	}
}

// ProbeClientRegistration asks the authorization endpoint whether it still
// recognizes the client_id / redirect_uri pair, for authorization servers
// that offer no RFC 7592 endpoint.
//
// The probe is an authorization request that carries only client_id and
// redirect_uri. It cannot start an authorization — response_type is missing —
// but RFC 6749 §4.1.2.1 fixes how that failure is reported, and the shape
// reveals what the client needs to know: a server that recognizes client_id
// and redirect_uri redirects the error to that redirect_uri, while a server
// that does not MUST NOT redirect and answers the user agent directly. The MCP
// TypeScript SDK, for one, answers an unknown client_id with a direct 400
// carrying {"error":"invalid_client"}. So a redirect to the client's own
// redirect_uri means the registration is alive; a 4xx that names
// invalid_client means it is gone; anything else is inconclusive.
//
// Redirects are not followed and the response body is not acted on, so the
// probe leaves no authorization state behind at the server.
func (c *Client) ProbeClientRegistration(ctx context.Context, authorizationEndpoint, clientID, redirectURI string) (RegistrationStatus, error) {
	if authorizationEndpoint == "" {
		return RegistrationUnknown, fmt.Errorf("no authorization endpoint to probe")
	}
	probeURL, err := url.Parse(authorizationEndpoint)
	if err != nil {
		return RegistrationUnknown, fmt.Errorf("invalid authorization endpoint: %w", err)
	}
	query := probeURL.Query()
	query.Set(FormFieldClientID, clientID)
	query.Set(FormFieldRedirectURI, redirectURI)
	probeURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL.String(), nil)
	if err != nil {
		return RegistrationUnknown, fmt.Errorf("failed to create registration probe request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	// The redirect target is the answer; following it would deliver a bogus
	// error response to the redirect URI.
	noRedirect := *c.httpClient
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := noRedirect.Do(req)
	if err != nil {
		return RegistrationUnknown, fmt.Errorf("registration probe request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		if redirectsTo(resp, redirectURI) {
			return RegistrationActive, nil
		}
		return RegistrationUnknown, fmt.Errorf("authorization endpoint redirected to %q instead of the registered redirect_uri", resp.Header.Get("Location"))
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && namesInvalidClient(body):
		return RegistrationGone, nil
	default:
		return RegistrationUnknown, fmt.Errorf("authorization endpoint answered status %d without naming the client", resp.StatusCode)
	}
}

// redirectsTo reports whether the response's Location points at the given
// redirect URI (same scheme, host and path; the query carries the error).
func redirectsTo(resp *http.Response, redirectURI string) bool {
	location, err := resp.Location()
	if err != nil {
		return false
	}
	registered, err := url.Parse(redirectURI)
	if err != nil {
		return false
	}
	return strings.EqualFold(location.Scheme, registered.Scheme) &&
		strings.EqualFold(location.Host, registered.Host) &&
		location.Path == registered.Path
}

// namesInvalidClient reports whether an error response body identifies the
// OAuth error invalid_client — as the error member of a JSON error object,
// or, for servers that answer the user agent with a page, as a literal.
func namesInvalidClient(body []byte) bool {
	var oauthErr struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &oauthErr); err == nil && oauthErr.Error != "" {
		return oauthErr.Error == ErrInvalidClient
	}
	return bytes.Contains(body, []byte(ErrInvalidClient))
}
