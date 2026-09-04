package oauth

import (
	"context"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// registrationCheckTimeout bounds the round trip that verifies a stored DCR
// registration before a sign-in link is issued with it. The caller is waiting
// interactively; an AS that does not answer in time is treated as
// inconclusive and the stored credentials are kept.
const registrationCheckTimeout = 5 * time.Second

// dcrClient is the resolved identification for stored RFC 7591 credentials.
func dcrClient(creds *pkgoauth.ClientCredentials) *resolvedClient {
	return &resolvedClient{ClientID: creds.ClientID, ClientSecret: creds.ClientSecret, Method: ClientIDMethodDCR}
}

// register performs the RFC 7591 registration with the issuer and stores the
// issued credentials. Callers hold registerMu. A rejected registration falls
// back to the CIMD URL rather than aborting — never worse than the pre-DCR
// behavior — and the challenge carries the rejection.
func (c *Client) register(ctx context.Context, issuer, registrationEndpoint string) *resolvedClient {
	registration, err := c.oauthClient.RegisterClient(ctx, registrationEndpoint, c.GetRegistrationMetadata())
	if err != nil {
		logging.Warn("OAuth", "Dynamic client registration with %s failed, falling back to CIMD URL as client_id: %v",
			issuer, err)
		return &resolvedClient{ClientID: c.clientID, Method: ClientIDMethodDCRFailed, RegistrationError: err}
	}

	creds := &pkgoauth.ClientCredentials{
		Issuer:                  issuer,
		ClientID:                registration.ClientID,
		ClientSecret:            registration.ClientSecret,
		RegistrationAccessToken: registration.RegistrationAccessToken,
		RegistrationClientURI:   registration.RegistrationClientURI,
		CreatedAt:               time.Now(),
	}
	if registration.ClientSecretExpiresAt > 0 {
		creds.ClientSecretExpiresAt = time.Unix(registration.ClientSecretExpiresAt, 0)
	}
	c.credStore.Store(issuer, creds)

	logging.Info("OAuth", "Registered muster as an OAuth client with %s via RFC 7591 (client_id=%s)",
		issuer, registration.ClientID)
	return dcrClient(creds)
}

// registrationGone reports whether the authorization server has stopped
// honoring stored DCR credentials, and why. It runs before a new flow is
// started with them, because the failure it guards against never reaches
// muster otherwise: an AS that has forgotten the client answers the
// authorization request in the user's browser with a direct 400 — it must
// not redirect to a redirect_uri it cannot verify — and the token endpoint
// is never reached.
//
// The RFC 7592 client read is used when the registration response provided
// the means for it, because it is the spec-defined liveness check. Servers
// that offer no management endpoint (the MCP TypeScript SDK's authorization
// server, for one) are asked at the authorization endpoint instead, see
// pkgoauth.Client.ProbeClientRegistration. Both checks are conservative:
// only a definitive answer drops credentials, everything else keeps them.
func (c *Client) registrationGone(ctx context.Context, issuer string, metadata *pkgoauth.Metadata, creds *pkgoauth.ClientCredentials) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, registrationCheckTimeout)
	defer cancel()

	if creds.RegistrationClientURI != "" && creds.RegistrationAccessToken != "" {
		status, err := c.oauthClient.ReadClientRegistration(ctx, creds.RegistrationClientURI, creds.RegistrationAccessToken)
		switch status {
		case pkgoauth.RegistrationGone:
			return true, "RFC 7592 registration read answered 401"
		case pkgoauth.RegistrationActive:
			return false, ""
		}
		logging.Debug("OAuth", "RFC 7592 registration read for %s (client_id=%s) was inconclusive, probing the authorization endpoint: %v",
			issuer, creds.ClientID, err)
	}

	status, err := c.oauthClient.ProbeClientRegistration(ctx, metadata.AuthorizationEndpoint, creds.ClientID, c.GetRedirectURI())
	switch status {
	case pkgoauth.RegistrationGone:
		return true, "authorization endpoint answered invalid_client"
	case pkgoauth.RegistrationActive:
		return false, ""
	}
	logging.Debug("OAuth", "Registration probe for %s (client_id=%s) was inconclusive, keeping the stored registration: %v",
		issuer, creds.ClientID, err)
	return false, ""
}

// dropRegistrationLocked removes the stored DCR credentials for the issuer if
// they still are the ones identified by clientID (a concurrent flow may have
// replaced them already). Callers hold registerMu. Returns whether anything
// was dropped.
func (c *Client) dropRegistrationLocked(issuer, clientID, reason string) bool {
	current := c.credStore.Get(issuer)
	if current == nil || current.ClientID != clientID {
		return false
	}
	c.credStore.Delete(issuer)
	logging.Info("OAuth", "Dropped stored RFC 7591 client registration with %s (client_id=%s): %s; muster registers again on the next sign-in",
		issuer, clientID, reason)
	return true
}

// dropRegistration is dropRegistrationLocked for callers that do not hold
// registerMu.
func (c *Client) dropRegistration(issuer, clientID, reason string) bool {
	c.registerMu.Lock()
	defer c.registerMu.Unlock()
	return c.dropRegistrationLocked(issuer, clientID, reason)
}

// ResetClientRegistration discards whatever DCR credentials are stored for
// the issuer, so the next flow registers muster again. This is the explicit
// escape hatch (core_auth_login with reset_client_registration) for an AS
// whose refusal the automatic checks cannot see. Returns whether credentials
// were stored.
func (c *Client) ResetClientRegistration(issuer string) bool {
	c.registerMu.Lock()
	defer c.registerMu.Unlock()

	current := c.credStore.Get(issuer)
	if current == nil {
		return false
	}
	return c.dropRegistrationLocked(issuer, current.ClientID, "reset requested via core_auth_login")
}

// ForgetRegistrationOnAuthorizationError drops the stored DCR credentials for
// the issuer when an authorization response reports invalid_client: the AS
// redirected the refusal of muster's client identification to the callback,
// so the registration it refers to is dead. Other error codes (access_denied,
// invalid_scope, ...) say nothing about the registration and change nothing.
func (c *Client) ForgetRegistrationOnAuthorizationError(issuer, errorCode string) {
	if errorCode != pkgoauth.ErrInvalidClient {
		return
	}

	c.registerMu.Lock()
	defer c.registerMu.Unlock()

	current := c.credStore.Get(issuer)
	if current == nil {
		return
	}
	c.dropRegistrationLocked(issuer, current.ClientID, "authorization response carried error=invalid_client")
}

// GetRegistrationMetadata returns the RFC 7591 client metadata muster sends
// on Dynamic Client Registration requests. It mirrors the CIMD content but
// omits client_id (forbidden on registration requests) and requests
// token_endpoint_auth_method "none" — muster is a public client protected by
// PKCE, and staying secret-free keeps the DCR path as close to the CIMD path
// as the AS allows. ASes that insist on issuing a secret still can; the
// response's client_secret is honored either way.
//
// scope is omitted as well: RFC 7591 makes it optional, and ASes reject
// registrations naming scopes they don't know (Miro answers
// invalid_client_metadata for the dex-oriented CIMD scopes). The
// authorization request carries the per-server scope discovered from the
// protected-resource metadata, so registration never needs one.
func (c *Client) GetRegistrationMetadata() *pkgoauth.ClientMetadata {
	metadata := c.GetClientMetadata()
	metadata.ClientID = ""
	metadata.Scope = ""
	return metadata
}

// GetClientCredentialsForIssuer returns the client_id and client_secret the
// OAuth flows use against the given issuer, without triggering a new DCR
// registration. This backs mcp-go's transport-level token refresh, which
// must present the same client identification the token was issued under.
func (c *Client) GetClientCredentialsForIssuer(ctx context.Context, issuer string) (clientID, clientSecret string) {
	if pin := c.issuerPin(issuer); pin != nil && pin.ClientID != "" {
		return pin.ClientID, pin.ClientSecret
	}
	metadata, err := c.oauthClient.DiscoverMetadata(ctx, issuer)
	if err != nil {
		// Metadata should be cached from the original flow; on a cold cache
		// with an unreachable AS the CIMD URL is the only sensible answer.
		logging.Debug("OAuth", "GetClientCredentialsForIssuer: metadata discovery for %s failed, using CIMD URL: %v", issuer, err)
		return c.clientID, ""
	}
	resolved := c.resolveClient(ctx, issuer, metadata, false)
	return resolved.ClientID, resolved.ClientSecret
}
