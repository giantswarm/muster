package api

import "fmt"

// SubjectScoped reports whether the grants of the server's OAuth sign-in
// belong to the person rather than to one login session: spec.auth.grantScope
// or spec.auth.authorizationServer.grantScope is "subject". ValidateGrantScope
// refuses the two disagreeing, so either one saying "subject" decides.
func (a *MCPServerAuth) SubjectScoped() bool {
	if a == nil {
		return false
	}
	return a.GrantScope == GrantScopeSubject || a.AuthorizationServer.SubjectScoped()
}

// DiscoversSubjectScopedIssuer reports whether the server files its grants
// under the person for an authorization server it does not pin: auth type
// oauth, spec.auth.grantScope "subject" and no
// spec.auth.authorizationServer.issuer. muster learns that issuer from the
// server's RFC 9728 metadata and makes it subject-scoped once it knows it.
func (a *MCPServerAuth) DiscoversSubjectScopedIssuer() bool {
	if a == nil || a.Type != MCPServerAuthTypeOAuth || a.GrantScope != GrantScopeSubject {
		return false
	}
	if a.UsesSessionAuth() {
		return false
	}
	return a.AuthorizationServer == nil || a.AuthorizationServer.Issuer == ""
}

// ValidateGrantScope checks spec.auth.grantScope as the CRD's CEL rules do, for
// the paths that do not go through the API server (filesystem mode,
// core_mcpserver_validate): one of "session" and "subject"; "subject" only with
// auth type oauth and never next to forwardToken or tokenExchange (which obtain
// no grant of their own; "session", the default, is accepted on any server);
// and not disagreeing with spec.auth.authorizationServer.grantScope.
func ValidateGrantScope(auth *MCPServerAuth) error {
	if auth == nil || auth.GrantScope == "" {
		return nil
	}
	if auth.GrantScope != GrantScopeSession && auth.GrantScope != GrantScopeSubject {
		return fmt.Errorf("auth.grantScope must be %q or %q, not %q", GrantScopeSession, GrantScopeSubject, auth.GrantScope)
	}
	if auth.GrantScope == GrantScopeSubject && (auth.Type != MCPServerAuthTypeOAuth || auth.UsesSessionAuth()) {
		return fmt.Errorf("auth.grantScope subject files the grant of a per-server OAuth sign-in under the person, so it needs auth.type %q and neither auth.forwardToken nor auth.tokenExchange", MCPServerAuthTypeOAuth)
	}
	if as := auth.AuthorizationServer; as != nil && as.GrantScope != "" && as.GrantScope != auth.GrantScope {
		return fmt.Errorf("auth.grantScope %q and auth.authorizationServer.grantScope %q disagree: set one of them, or both to the same value", auth.GrantScope, as.GrantScope)
	}
	return nil
}

// ValidateAuthorizationServer checks the parts of spec.auth.authorizationServer
// the CRD's own rules express as CEL, for the paths that do not go through the
// API server (filesystem mode, core_mcpserver_validate): expectedIssuer makes
// sense only on a pin with explicit endpoints -- without them muster discovers
// the authorization server and compares against the issuer it publishes.
func ValidateAuthorizationServer(auth *MCPServerAuth) error {
	if auth == nil || auth.AuthorizationServer == nil {
		return nil
	}
	as := auth.AuthorizationServer
	if (as.AuthorizationEndpoint == "") != (as.TokenEndpoint == "") {
		return fmt.Errorf("auth.authorizationServer: authorizationEndpoint and tokenEndpoint must be set together")
	}
	if as.ExpectedIssuer != "" && !as.HasPinnedEndpoints() {
		return fmt.Errorf("auth.authorizationServer.expectedIssuer needs authorizationEndpoint and tokenEndpoint")
	}
	return nil
}
