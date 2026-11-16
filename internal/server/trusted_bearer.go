package server

import (
	"errors"
	"net/http"

	"github.com/giantswarm/mcp-oauth/providers"
	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/muster/v5/internal/api"
)

// serveTrustedIssuer resolves the verified identity without storing the
// resource access token as a provider credential.
func (s *OAuthHTTPServer) serveTrustedIssuer(w http.ResponseWriter, r *http.Request, userInfo *providers.UserInfo, next http.Handler) {
	identity, err := acceptTrustedIssuerToken(s.oauthServer, r.Context(), extractBearerToken(r))
	if err != nil {
		if isUnavailable(err) {
			writeUnavailable(w)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	ctx := api.WithSessionID(r.Context(), identity.SessionID)
	ctx = api.WithSubject(ctx, identity.Subject)
	ctx = api.WithSignInIdentity(ctx, s.signInIdentity(userInfo))
	s.fireOnAuthenticated(ctx)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// isUnavailable reports whether err means the identity provider or the token
// store did not answer: the bearer was neither validated nor rejected.
func isUnavailable(err error) bool {
	return errors.Is(err, oauthserver.ErrProviderUnavailable) || errors.Is(err, oauthserver.ErrStorageUnavailable)
}

// writeUnavailable answers a request whose bearer could not be checked as 503
// with Retry-After and no WWW-Authenticate challenge (RFC 9110 §15.6.4), so the
// client retries the same request instead of discarding its token.
func writeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
}
