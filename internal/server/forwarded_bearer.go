package server

import (
	"errors"
	"net/http"

	"github.com/golang-jwt/jwt/v5"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"
	"github.com/giantswarm/mcp-oauth/security"
	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// authenticateForwardedBearer guards the endpoints that take the person's
// bearer. A JWT bearer issued by the operator's identity provider, as
// agentgateway, Backstage and agents forward it, is authenticated through the
// trust the operator configured for it:
//
//   - trustedAudiences: an ID token issued to one of the listed clients
//     (AcceptForwardedIDToken). The injector then forwards it as the
//     session's ID token, as for any forwarded ID token.
//   - trustedIssuers: a token of a listed issuer, checked against that
//     issuer's allowedAudiences and acceptedTypHeaders
//     (AcceptTrustedIssuerToken). The injector keeps it out of backend
//     credentials (serveTrustedIssuer).
//
// mcp-oauth's ValidateToken middleware no longer tries either path for a
// resource bearer: it accepts a trusted-issuer JWT only with muster's resource
// identifier in aud and an at+jwt typ, which a Dex ID token never carries.
// Every other bearer -- muster's opaque access tokens, a JWT neither path
// accepts, a sender-constrained JWT (cnf), whose DPoP proof only mcp-oauth's
// middleware checks -- goes to mcp-oauth's middleware unchanged, so its
// validation, rejection responses and audit events stay the single path for
// them.
func (s *OAuthHTTPServer) authenticateForwardedBearer(validated http.Handler, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := extractBearerToken(r)
		claims, ok := forwardedBearerClaims(bearer)
		if !ok || claims.Cnf != nil || s.ipRateLimited(r) {
			validated.ServeHTTP(w, r)
			return
		}

		acceptance, err := s.acceptForwardedBearer(r, bearer, claims.Act != nil)
		switch {
		case isUnavailable(err):
			// The issuer's keys could not be read, so the token was not
			// rejected: a 401 would make the caller discard a valid token.
			w.Header().Set("Retry-After", "1")
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		case err != nil:
			validated.ServeHTTP(w, r)
			return
		}

		userInfo := acceptance.UserInfo
		if userInfo == nil {
			userInfo = &providers.UserInfo{ID: acceptance.Subject}
		}
		if s.userRateLimited(userInfo.ID) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}

		ctx := oauthhandler.ContextWithUserInfo(r.Context(), userInfo)
		ctx = oauthhandler.ContextWithSessionID(ctx, acceptance.SessionID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// acceptForwardedBearer authenticates bearer through trustedAudiences, then
// trustedIssuers. The token's own aud decides the path: an ID token issued to
// a trusted client is identity muster forwards; any other token of a trusted
// issuer is a resource bearer that stays with muster. A delegated token (an
// act claim) is accepted only through trustedIssuers, whose delegation policy
// decides the acting party; the trustedAudiences path checks no actor.
func (s *OAuthHTTPServer) acceptForwardedBearer(r *http.Request, bearer string, delegated bool) (*oauthserver.ForwardedIDTokenAcceptance, error) {
	forwardedErr := errors.New("delegated token: trustedAudiences checks no actor")
	if !delegated {
		var acceptance *oauthserver.ForwardedIDTokenAcceptance
		acceptance, forwardedErr = acceptForwardedIDToken(s.oauthServer, r.Context(), bearer)
		if forwardedErr == nil {
			return acceptance, nil
		}
		if isUnavailable(forwardedErr) {
			return nil, forwardedErr
		}
	}
	acceptance, err := acceptTrustedIssuerToken(s.oauthServer, r.Context(), bearer)
	if err == nil {
		acceptance.UserInfo = trustedIssuerUserInfo(acceptance)
		return acceptance, nil
	}
	if s.debug {
		logging.Debug("OAuth", "Forwarded JWT bearer not accepted by trustedAudiences (%v) or trustedIssuers (%v)", forwardedErr, err)
	}
	return nil, err
}

// trustedIssuerUserInfo marks the acceptance's identity as a trusted-issuer
// one, which routes the request through serveTrustedIssuer.
func trustedIssuerUserInfo(acceptance *oauthserver.ForwardedIDTokenAcceptance) *providers.UserInfo {
	info := providers.UserInfo{ID: acceptance.Subject}
	if acceptance.UserInfo != nil {
		info = *acceptance.UserInfo
	}
	if info.Issuer == "" {
		info.Issuer = acceptance.Issuer
	}
	info.TokenSource = providers.TokenSourceTrustedIssuer
	return &info
}

// bearerBindingClaims are the claims that decide which path may accept a JWT
// bearer: cnf binds it to a DPoP key, act names an acting party.
type bearerBindingClaims struct {
	jwt.RegisteredClaims
	Cnf map[string]any `json:"cnf,omitempty"`
	Act map[string]any `json:"act,omitempty"`
}

// forwardedBearerClaims decodes a JWT bearer's binding claims without
// verifying it: they only narrow which path validates the token. ok is false
// for an empty or non-JWT bearer (muster's opaque access tokens).
func forwardedBearerClaims(bearer string) (bearerBindingClaims, bool) {
	var claims bearerBindingClaims
	if bearer == "" {
		return claims, false
	}
	if _, _, err := jwt.NewParser(jwt.WithPaddingAllowed()).ParseUnverified(bearer, &claims); err != nil {
		return claims, false
	}
	return claims, true
}

func isUnavailable(err error) bool {
	return errors.Is(err, oauthserver.ErrProviderUnavailable) || errors.Is(err, oauthserver.ErrStorageUnavailable)
}

// ipRateLimited reports whether the caller's IP is over mcp-oauth's IP limit.
// A limited request goes to mcp-oauth's middleware, which answers it with its
// own 429. A JWT neither trust path accepts draws on the limit here and again
// in mcp-oauth's middleware, which only tightens it for rejected bearers.
func (s *OAuthHTTPServer) ipRateLimited(r *http.Request) bool {
	if s.oauthServer == nil || s.oauthServer.RateLimiter == nil {
		return false
	}
	ip := security.GetClientIP(r, s.oauthServer.Config.TrustProxy, s.oauthServer.Config.TrustedProxyCount)
	return !s.oauthServer.RateLimiter.Allow(security.RateLimitBucket(ip))
}

// userRateLimited reports whether mcp-oauth's per-user limit refuses a
// bearer this middleware authenticated, which mcp-oauth's middleware never
// sees.
func (s *OAuthHTTPServer) userRateLimited(userID string) bool {
	return s.oauthServer != nil && s.oauthServer.UserRateLimiter != nil && !s.oauthServer.UserRateLimiter.Allow(userID)
}
