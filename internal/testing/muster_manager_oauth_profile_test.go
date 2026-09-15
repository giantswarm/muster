package testing

import (
	"testing"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/testing/mock"
)

func boolPtr(b bool) *bool { return &b }

func TestApplyMockOAuthBundle_ExplicitFlagsOverrideTheProfile(t *testing.T) {
	cfg := MockOAuthServerConfig{Name: "idp", Profile: "dex", SupportsDCR: boolPtr(false), OmitTokenExpiry: boolPtr(true)}
	profile, bundle, err := mockOAuthProfile(cfg)
	if err != nil || profile != mock.ProfileDex {
		t.Fatalf("mockOAuthProfile = %q, %v", profile, err)
	}
	var serverConfig mock.OAuthServerConfig
	applyMockOAuthBundle(&serverConfig, cfg, bundle)
	if serverConfig.SupportsDCR {
		t.Error("supports_dcr: false must switch the bundle's DCR off")
	}
	if !serverConfig.OmitTokenScope {
		t.Error("the bundle's omit_token_scope must stay on when the scenario says nothing")
	}
	if !serverConfig.OmitTokenExpiry {
		t.Error("omit_token_expiry: true must switch on what the bundle leaves alone")
	}
	if serverConfig.OmitDiscovery || serverConfig.RequireRegisteredClient {
		t.Error("flags neither bundled nor set must stay off")
	}
}

func TestMockOAuthProfile_UnknownNameIsRefused(t *testing.T) {
	if _, _, err := mockOAuthProfile(MockOAuthServerConfig{Name: "idp", Profile: "okta"}); err == nil {
		t.Fatal("an unknown profile must fail the scenario with a message")
	}
	if _, bundle, err := mockOAuthProfile(MockOAuthServerConfig{Name: "idp"}); err != nil || bundle != (mock.ProfileBundle{}) {
		t.Fatalf("no profile must bundle nothing: %+v, %v", bundle, err)
	}
}

func TestResolveAuthorizationServerPin(t *testing.T) {
	github := mock.ProfileGitHub.Bundle()
	block := func(kv ...interface{}) map[string]interface{} {
		m := map[string]interface{}{"mock_oauth_server_ref": "github-as"}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}

	// A github server pins itself with endpoints and subject-scoped grants.
	if got := resolveAuthorizationServerPin(block(), github); got != (authorizationServerPin{Pin: true, GrantScope: api.GrantScopeSubject, EndpointsRef: "github-as"}) {
		t.Errorf("github defaults: %+v", got)
	}
	// Explicit keys override the bundle one by one.
	if got := resolveAuthorizationServerPin(block("grant_scope", "session", "pin_endpoints_ref", "other-as"), github); got != (authorizationServerPin{Pin: true, GrantScope: "session", EndpointsRef: "other-as"}) {
		t.Errorf("github with overrides: %+v", got)
	}
	// Without a profile the old rules hold: nothing implies the pin ...
	if got := resolveAuthorizationServerPin(block(), mock.ProfileBundle{}); got.Pin {
		t.Errorf("no profile, no keys: %+v", got)
	}
	// ... a grant scope or endpoints reference does ...
	if got := resolveAuthorizationServerPin(block("grant_scope", "subject"), mock.ProfileBundle{}); !got.Pin || got.GrantScope != "subject" || got.EndpointsRef != "" {
		t.Errorf("grant_scope alone: %+v", got)
	}
	// ... and so does the explicit pin without either.
	if got := resolveAuthorizationServerPin(block("pin_authorization_server", true), mock.ProfileBundle{}); got != (authorizationServerPin{Pin: true}) {
		t.Errorf("pin_authorization_server alone: %+v", got)
	}
}

func TestReferencedProfileBundle(t *testing.T) {
	config := &MusterPreConfiguration{MockOAuthServers: []MockOAuthServerConfig{{Name: "plain"}, {Name: "gh", Profile: "github"}}}
	if b := referencedProfileBundle(config, "gh"); !b.ResourceOmitsMetadata {
		t.Errorf("gh is a github server: %+v", b)
	}
	if b := referencedProfileBundle(config, "plain"); b != (mock.ProfileBundle{}) {
		t.Errorf("plain has no profile: %+v", b)
	}
	if b := referencedProfileBundle(config, ""); b != (mock.ProfileBundle{}) {
		t.Errorf("no reference: %+v", b)
	}
	if b := referencedProfileBundle(nil, "gh"); b != (mock.ProfileBundle{}) {
		t.Errorf("no configuration: %+v", b)
	}
}
