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
		status := http.StatusUnauthorized
		if errors.Is(err, oauthserver.ErrProviderUnavailable) || errors.Is(err, oauthserver.ErrStorageUnavailable) {
			status = http.StatusServiceUnavailable
			w.Header().Set("Retry-After", "1")
		} else {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	ctx := api.WithSessionID(r.Context(), identity.SessionID)
	ctx = api.WithSubject(ctx, identity.Subject)
	ctx = api.WithSignInIdentity(ctx, s.signInIdentity(userInfo))
	s.fireOnAuthenticated(ctx)
	next.ServeHTTP(w, r.WithContext(ctx))
}
