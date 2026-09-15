package mock

import "fmt"

// Profile names a real authorization server whose behaviour the mock
// reproduces as one bundle instead of one flag per quirk. A scenario written
// against a named server selects its profile; a flag set beside the profile
// overrides the bundle for that flag alone.
type Profile string

const (
	// ProfileGitHub is GitHub's OAuth: no RFC 8414 / OIDC discovery document,
	// no Client ID Metadata Documents and no RFC 7591 registration -- only a
	// client registered out of band; `scope` omitted from the token response;
	// access tokens without an expiry (`expires_in` absent). The resources it
	// protects answer a bare 401 without RFC 9728 metadata and its grants are
	// the person's (grantScope subject), so muster is pinned to it with
	// explicit endpoints.
	ProfileGitHub Profile = "github"

	// ProfileDex is Dex: a discovery document, `scope` omitted from the token
	// response (RFC 6749 §5.1 allows that when the granted scope is the
	// requested one), an id_token with every token, RFC 7591 registration.
	ProfileDex Profile = "dex"

	// ProfilePro is an authorization server built on the MCP TypeScript SDK
	// (pro): a discovery document; RFC 7591 registration whose response
	// carries no registration_client_uri, so there is no RFC 7592 read to
	// check a registration with; a client it does not know answered directly
	// at /authorize with invalid_client instead of a redirect; registrations
	// held in memory and gone when the server restarts.
	ProfilePro Profile = "pro"
)

// Profiles lists every profile the mock knows.
var Profiles = []Profile{ProfileGitHub, ProfileDex, ProfilePro}

// ParseProfile returns the profile of that name. The empty name is the
// well-behaved default and is returned as "" without an error.
func ParseProfile(name string) (Profile, error) {
	switch p := Profile(name); p {
	case "", ProfileGitHub, ProfileDex, ProfilePro:
		return p, nil
	default:
		return "", fmt.Errorf("unknown authorization-server profile %q (known: %s, %s, %s)",
			name, ProfileGitHub, ProfileDex, ProfilePro)
	}
}

// ProfileBundle is what a profile switches on. The first group are flags of
// the mock authorization server (OAuthServerConfig); the second are the
// defaults for the protected resources that verify tokens from it and for the
// MCPServer definition that points muster at it. Whatever a bundle leaves
// false keeps the mock's default: a well-behaved authorization server with a
// discovery document, `scope` and `expires_in` in every token response, RFC
// 7592 management for its registrations, and resources that publish RFC 9728
// metadata.
type ProfileBundle struct {
	// OmitDiscovery serves no RFC 8414 / OIDC discovery document.
	OmitDiscovery bool
	// SupportsDCR advertises and serves RFC 7591 registration.
	SupportsDCR bool
	// RequireRegisteredClient makes the token endpoint refuse client_ids it
	// neither registered nor was configured with.
	RequireRegisteredClient bool
	// OmitTokenScope leaves `scope` out of token responses.
	OmitTokenScope bool
	// OmitTokenExpiry leaves `expires_in` out of token responses.
	OmitTokenExpiry bool
	// OmitRegistrationClientURI leaves registration_client_uri and
	// registration_access_token out of registration responses.
	OmitRegistrationClientURI bool
	// ForgetRegistrationsOnRestart drops every registration on Restart.
	ForgetRegistrationsOnRestart bool

	// ResourceOmitsMetadata: the protected resources answer a bare 401 and
	// serve no /.well-known/oauth-protected-resource document.
	ResourceOmitsMetadata bool
	// GrantScopeSubject: grants are filed under the person (grantScope
	// subject), not the login session.
	GrantScopeSubject bool
	// PinWithEndpoints: muster is pinned to the issuer with explicit
	// authorization and token endpoints, since nothing can be discovered.
	PinWithEndpoints bool
}

// Bundle returns what the profile switches on; the empty profile switches on
// nothing.
func (p Profile) Bundle() ProfileBundle {
	switch p {
	case ProfileGitHub:
		return ProfileBundle{
			OmitDiscovery:         true,
			OmitTokenScope:        true,
			OmitTokenExpiry:       true,
			ResourceOmitsMetadata: true,
			GrantScopeSubject:     true,
			PinWithEndpoints:      true,
		}
	case ProfileDex:
		return ProfileBundle{
			SupportsDCR:    true,
			OmitTokenScope: true,
		}
	case ProfilePro:
		return ProfileBundle{
			SupportsDCR:                  true,
			RequireRegisteredClient:      true,
			OmitRegistrationClientURI:    true,
			ForgetRegistrationsOnRestart: true,
		}
	default:
		return ProfileBundle{}
	}
}

// Apply switches the bundle's authorization-server flags on in cfg; flags the
// bundle leaves alone keep their value. A flag the bundle switches on cannot
// be switched off here -- a caller that needs a variation resolves the
// Bundle against its explicit values instead, as the scenario harness does.
func (p Profile) Apply(cfg *OAuthServerConfig) {
	b := p.Bundle()
	cfg.OmitDiscovery = cfg.OmitDiscovery || b.OmitDiscovery
	cfg.SupportsDCR = cfg.SupportsDCR || b.SupportsDCR
	cfg.RequireRegisteredClient = cfg.RequireRegisteredClient || b.RequireRegisteredClient
	cfg.OmitTokenScope = cfg.OmitTokenScope || b.OmitTokenScope
	cfg.OmitTokenExpiry = cfg.OmitTokenExpiry || b.OmitTokenExpiry
	cfg.OmitRegistrationClientURI = cfg.OmitRegistrationClientURI || b.OmitRegistrationClientURI
	cfg.ForgetRegistrationsOnRestart = cfg.ForgetRegistrationsOnRestart || b.ForgetRegistrationsOnRestart
}
