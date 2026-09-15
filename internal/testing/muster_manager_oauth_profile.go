package testing

import (
	"fmt"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/testing/mock"
)

// mockOAuthProfile returns the profile a mock OAuth server's configuration
// selects and what it bundles; the empty profile bundles nothing.
func mockOAuthProfile(cfg MockOAuthServerConfig) (mock.Profile, mock.ProfileBundle, error) {
	profile, err := mock.ParseProfile(cfg.Profile)
	if err != nil {
		return "", mock.ProfileBundle{}, fmt.Errorf("mock OAuth server %s: %w", cfg.Name, err)
	}
	return profile, profile.Bundle(), nil
}

// applyMockOAuthBundle fills the mock authorization server's flags from the
// profile's bundle; a flag the scenario set explicitly wins over the bundle
// for that flag, so `supports_dcr: false` beside `profile: dex` is a Dex
// without registration.
func applyMockOAuthBundle(serverConfig *mock.OAuthServerConfig, cfg MockOAuthServerConfig, bundle mock.ProfileBundle) {
	serverConfig.OmitDiscovery = boolOr(cfg.OmitDiscovery, bundle.OmitDiscovery)
	serverConfig.SupportsDCR = boolOr(cfg.SupportsDCR, bundle.SupportsDCR)
	serverConfig.RequireRegisteredClient = boolOr(cfg.RequireRegisteredClient, bundle.RequireRegisteredClient)
	serverConfig.OmitTokenScope = boolOr(cfg.OmitTokenScope, bundle.OmitTokenScope)
	serverConfig.OmitTokenExpiry = boolOr(cfg.OmitTokenExpiry, bundle.OmitTokenExpiry)
	serverConfig.OmitRegistrationClientURI = boolOr(cfg.OmitRegistrationClientURI, bundle.OmitRegistrationClientURI)
	serverConfig.ForgetRegistrationsOnRestart = boolOr(cfg.ForgetRegistrationsOnRestart, bundle.ForgetRegistrationsOnRestart)
}

// boolOr is the explicit value when the scenario set one, else the bundle's.
func boolOr(explicit *bool, bundled bool) bool {
	if explicit != nil {
		return *explicit
	}
	return bundled
}

// referencedProfileBundle is the bundle of the mock OAuth server an MCP
// server's oauth block references (mock_oauth_server_ref) -- empty when it
// names none, or the server has no profile. Profiles were validated when the
// servers started, so an unknown name cannot reach this point.
func referencedProfileBundle(config *MusterPreConfiguration, ref string) mock.ProfileBundle {
	if config == nil || ref == "" {
		return mock.ProfileBundle{}
	}
	for _, oauthCfg := range config.MockOAuthServers {
		if oauthCfg.Name == ref {
			return mock.Profile(oauthCfg.Profile).Bundle()
		}
	}
	return mock.ProfileBundle{}
}

// authorizationServerPin is what an MCP server's oauth block asks the harness
// to render as spec.auth.authorizationServer.
type authorizationServerPin struct {
	// Pin: render the authorization server at all.
	Pin bool
	// GrantScope is the pin's grantScope; empty for none.
	GrantScope string
	// EndpointsRef names the mock OAuth server whose /authorize and /token
	// the pin carries as explicit endpoints; empty for none.
	EndpointsRef string
}

// resolveAuthorizationServerPin reads oauth.grant_scope,
// oauth.pin_authorization_server and oauth.pin_endpoints_ref over the
// referenced server's profile: a github-profile server is pinned with its
// own endpoints and subject-scoped grants unless the block says otherwise.
// As before, a grant scope or an endpoints reference implies the pin.
func resolveAuthorizationServerPin(oauthConfig map[string]interface{}, bundle mock.ProfileBundle) authorizationServerPin {
	ref, _ := oauthConfig["mock_oauth_server_ref"].(string)
	pin := authorizationServerPin{Pin: bundle.PinWithEndpoints}

	if grantScope, ok := oauthConfig["grant_scope"].(string); ok {
		pin.GrantScope = grantScope
	} else if bundle.GrantScopeSubject {
		pin.GrantScope = api.GrantScopeSubject
	}
	if endpointsRef, ok := oauthConfig["pin_endpoints_ref"].(string); ok {
		pin.EndpointsRef = endpointsRef
	} else if bundle.PinWithEndpoints {
		pin.EndpointsRef = ref
	}
	if explicit, ok := oauthConfig["pin_authorization_server"].(bool); ok {
		pin.Pin = explicit
	}
	pin.Pin = pin.Pin || pin.GrantScope != "" || pin.EndpointsRef != ""
	return pin
}
