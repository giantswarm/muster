package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/mcp-oauth/security"

	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
)

const (
	grantTypeTokenExchange = oauthserver.GrantTypeTokenExchange
	tokenTypeBearer        = "Bearer"

	// defaultMaxRequestBodySize mirrors mcp-oauth's default token-endpoint body
	// cap (1 MiB). Used as a fallback when the wrapped server config is absent.
	defaultMaxRequestBodySize int64 = 1 << 20
)

// machinePrincipalExchangeMiddleware intercepts POST /oauth/token token-exchange
// requests whose subject matches a configured machine principal, injecting the
// principal's synthetic identity claims into the issued JWT via
// ExchangeSubjectToken.
//
// Every other request — including token exchanges whose subject is NOT a
// machine principal — falls through to the wrapped library handler unchanged.
// This is deliberate: the library's token endpoint applies IP rate limiting, a
// request-body cap, DPoP validation, RFC 8707 resource binding, audit logging,
// metrics, and security headers. We only divert the narrow set of requests that
// need synthetic-claim injection, because the library exposes no hook to inject
// those claims into its own exchange flow (it never passes ExchangeOptions).
// See the follow-up to push a claims resolver into mcp-oauth and delete this
// middleware entirely.
type machinePrincipalExchangeMiddleware struct {
	next              http.Handler
	oauthServer       *oauthserver.Server
	machinePrincipals map[string]config.MachinePrincipalConfig
}

func (m *machinePrincipalExchangeMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != oauthserver.EndpointPathToken {
		m.next.ServeHTTP(w, r)
		return
	}

	// Cap the body before parsing so an oversized request cannot be read
	// unbounded here, matching the library token handler's MaxBytesReader. The
	// wrapped handler re-applies its own cap for fall-through requests.
	r.Body = http.MaxBytesReader(w, r.Body, m.maxRequestBodySize())
	if err := r.ParseForm(); err != nil {
		m.next.ServeHTTP(w, r)
		return
	}

	if r.Form.Get("grant_type") != grantTypeTokenExchange {
		m.next.ServeHTTP(w, r)
		return
	}

	sub := extractJWTSub(r.Form.Get("subject_token"))
	mp, hasPrincipal := lookupMachinePrincipal(sub, m.machinePrincipals)
	if !hasPrincipal {
		// Not a configured machine principal: defer to the library handler so the
		// exchange runs through the full, hardened RFC 8693 path rather than this
		// reduced one.
		m.next.ServeHTTP(w, r)
		return
	}

	// Machine-principal path. Apply the security headers and request validation
	// the library handler performs but ExchangeSubjectToken does not.
	security.SetSecurityHeaders(w, m.oauthServer.Config.Issuer)

	resource := r.Form.Get("resource")
	if resource == "" {
		// RFC 8707: resource is required so the issued token carries a bound aud.
		writeJSONError(w, "invalid_request", "resource is required (RFC 8707)", http.StatusBadRequest)
		return
	}

	logging.Info("MachinePrincipal", "token exchange: injecting machine principal claims for sub=%s email=%s", sub, truncateEmail(mp.Email))

	// dpopJKT is intentionally empty: M2M clients do not use DPoP in the MVP.
	result, err := m.oauthServer.ExchangeSubjectToken(
		r.Context(),
		r.Form.Get("subject_token"),
		r.Form.Get("subject_token_type"),
		resource,
		r.Form.Get("scope"),
		"",
		oauthserver.ExchangeOptions{Email: mp.Email, Groups: mp.Groups},
	)
	if err != nil {
		writeExchangeError(w, err)
		return
	}

	writeExchangeResponse(w, result)
}

// maxRequestBodySize returns the configured token-endpoint body cap, falling
// back to the default when no server config is available (e.g. in unit tests).
func (m *machinePrincipalExchangeMiddleware) maxRequestBodySize() int64 {
	if m.oauthServer != nil && m.oauthServer.Config != nil && m.oauthServer.Config.MaxRequestBodySize > 0 {
		return m.oauthServer.Config.MaxRequestBodySize
	}
	return defaultMaxRequestBodySize
}

// extractJWTSub decodes the JWT payload (without signature verification) and
// returns the sub claim. Returns empty string on any parse error.
func extractJWTSub(rawJWT string) string {
	parts := strings.SplitN(rawJWT, ".", 3)
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Sub
}

func writeExchangeResponse(w http.ResponseWriter, result *oauthserver.TokenExchangeResult) {
	expiresIn := max(int64(time.Until(result.ExpiresAt).Seconds()), 0)
	resp := map[string]any{
		"access_token":      result.AccessToken,
		"issued_token_type": result.IssuedTokenType,
		"token_type":        tokenTypeBearer,
		"expires_in":        expiresIn,
	}
	if result.Scope != "" {
		resp["scope"] = result.Scope
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logging.Error("MachinePrincipal", err, "failed to write token exchange response")
	}
}

func writeExchangeError(w http.ResponseWriter, err error) {
	var unsupported *oauthserver.TokenExchangeUnsupportedTypeError
	switch {
	case isTokenExchangeUnsupportedTypeError(err, &unsupported):
		writeJSONError(w, "unsupported_grant_type",
			"no validator registered for subject_token_type "+unsupported.TokenType(),
			http.StatusBadRequest)
	default:
		writeJSONError(w, "invalid_grant", "subject token invalid or rejected", http.StatusBadRequest)
	}
}

func isTokenExchangeUnsupportedTypeError(err error, target **oauthserver.TokenExchangeUnsupportedTypeError) bool {
	return errors.As(err, target)
}

func writeJSONError(w http.ResponseWriter, code, description string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}
