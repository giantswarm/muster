package server

import (
	"fmt"
	"net/http"
	"strings"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// isDelegated reports whether a validated bearer is a delegated token: it
// names an acting party in its act claim (RFC 8693 §4.1) and comes from a
// trusted issuer with a delegation block. A token carrying act from any other
// issuer keeps the on-behalf-of path, unchanged.
func (s *OAuthHTTPServer) isDelegated(userInfo *providers.UserInfo) bool {
	if userInfo == nil || userInfo.ActorSubject == "" {
		return false
	}
	for _, iss := range s.config.TrustedIssuers {
		if iss.Delegation != nil && iss.Issuer == userInfo.Issuer {
			return true
		}
	}
	return false
}

// namesActor reports whether a validated bearer carries an act claim (RFC 8693
// §4.1): some party acts for the person, from any issuer. isDelegated is the
// narrower case muster serves on its MCP endpoints.
func namesActor(userInfo *providers.UserInfo) bool {
	return userInfo != nil && (userInfo.ActorIssuer != "" || userInfo.ActorSubject != "" || len(userInfo.ActorChain) > 0)
}

// serveDelegated serves a request whose validated bearer is a delegated token:
// sub names the person and act the party acting for them. mcp-oauth has
// verified the token and its issuer's act policy (TrustedIssuer.Actors).
//
// The call runs as the person (the raw sub, never a subjectClaim mapping) in
// the session mcp-oauth derived from the token's principal and act chain. The
// toolset is the token's scope: its selectors bind the session's toolset and
// the X-Muster-Toolset header can only narrow it. A token without a selector
// is refused with 403 insufficient_scope (RFC 6750 §3.1). The bearer is never
// filed as a login or put where a backend gets it forwarded: backends get
// tokens exchanged for them (api.DelegatedCaller).
func (s *OAuthHTTPServer) serveDelegated(w http.ResponseWriter, r *http.Request, userInfo *providers.UserInfo, next http.Handler) {
	ctx := r.Context()
	bearer := extractBearerToken(r)
	sessionID, _ := oauthhandler.SessionIDFromContext(ctx)
	actorChain := make([]string, 0, len(userInfo.ActorChain))
	for _, actor := range userInfo.ActorChain {
		actorChain = append(actorChain, actor.Subject)
	}
	actorDetails := fmt.Sprintf("session=%s actor_iss=%s actors=[%s]",
		logging.TruncateIdentifier(sessionID), userInfo.ActorIssuer, strings.Join(actorChain, ","))
	audit := logging.AuditEvent{
		Action:  "delegated_bearer",
		Subject: logging.TruncateIdentifier(userInfo.ID),
		Target:  "muster",
		Details: actorDetails,
	}
	refuse := func(status int, challenge, reason string) {
		audit.Outcome, audit.Error = "failure", reason
		logging.Audit(audit)
		w.Header().Set("WWW-Authenticate", challenge)
		http.Error(w, http.StatusText(status)+": "+reason, status)
	}

	if userInfo.TokenSource != providers.TokenSourceTrustedIssuer {
		refuse(http.StatusUnauthorized, `Bearer error="invalid_token"`, "a token naming an actor is accepted only from a trusted issuer")
		return
	}
	selectors := toolset.FromScope(userInfo.Scopes)
	ts, err := toolset.ParseInline(selectors)
	if len(selectors) == 0 || err != nil {
		reason := "the token grants no toolset"
		if len(selectors) > 0 {
			reason = err.Error()
		}
		refuse(http.StatusForbidden, s.insufficientScopeChallenge(reason), reason)
		return
	}

	caller := api.DelegatedCaller{
		Token:       bearer,
		Issuer:      userInfo.Issuer,
		Subject:     userInfo.ID,
		ActorIssuer: userInfo.ActorIssuer,
		ActorChain:  actorChain,
		Toolset:     ts.Raw,
	}
	audit.Outcome = "success"
	audit.Details = fmt.Sprintf("%s toolset=%s", actorDetails, ts)
	logging.Audit(audit)

	ctx = api.WithSessionID(ctx, sessionID)
	ctx = api.WithSubject(ctx, userInfo.ID)
	ctx = api.WithSignInIdentity(ctx, api.SignInIdentity{Issuer: userInfo.Issuer, Subject: userInfo.ID})
	ctx = toolset.ContextWithBound(ctx, ts.Raw)
	ctx = api.WithDelegatedCaller(ctx, caller)
	s.fireOnAuthenticated(ctx)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// insufficientScopeChallenge is the WWW-Authenticate value of a 403 for a
// token that grants no usable toolset (MCP authorization, Scope Challenge
// Handling). It carries no scope parameter: the toolset an agent needs is its
// own, which muster cannot name.
func (s *OAuthHTTPServer) insufficientScopeChallenge(description string) string {
	return fmt.Sprintf(`Bearer error="insufficient_scope", error_description=%q, resource_metadata=%q`,
		description, strings.TrimSuffix(s.config.BaseURL, "/")+"/.well-known/oauth-protected-resource")
}
