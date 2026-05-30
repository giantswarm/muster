package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
)

const (
	grantTypeTokenExchange = oauthserver.GrantTypeTokenExchange
	tokenTypeBearer        = "Bearer"
)

// machinePrincipalExchangeMiddleware intercepts POST /oauth/token token-exchange
// requests, looks up the subject's machine principal claims, and injects them
// into the issued JWT via ExchangeSubjectToken. All other requests fall through
// to the wrapped handler.
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

	if err := r.ParseForm(); err != nil {
		m.next.ServeHTTP(w, r)
		return
	}

	if r.Form.Get("grant_type") != grantTypeTokenExchange {
		m.next.ServeHTTP(w, r)
		return
	}

	subjectToken := r.Form.Get("subject_token")
	subjectTokenType := r.Form.Get("subject_token_type")
	resource := r.Form.Get("resource")
	scope := r.Form.Get("scope")

	sub := extractJWTSub(subjectToken)
	mp, hasPrincipal := lookupMachinePrincipal(sub, m.machinePrincipals)

	var opts []oauthserver.ExchangeOptions
	if hasPrincipal {
		opts = append(opts, oauthserver.ExchangeOptions{
			Email:  mp.Email,
			Groups: mp.Groups,
		})
		logging.Info("MachinePrincipal", "token exchange: injecting machine principal claims for sub=%s email=%s", sub, mp.Email)
	}

	// dpopJKT is intentionally empty: M2M clients do not use DPoP in the MVP.
	result, err := m.oauthServer.ExchangeSubjectToken(r.Context(), subjectToken, subjectTokenType, resource, scope, "", opts...)
	if err != nil {
		writeExchangeError(w, err)
		return
	}

	writeExchangeResponse(w, result)
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
