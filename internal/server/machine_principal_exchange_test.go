package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/config"
)

// sentinelHandler records whether it was invoked, standing in for the wrapped
// `next` handler so tests can assert fall-through behavior.
type sentinelHandler struct {
	called bool
}

func (s *sentinelHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.called = true
	w.WriteHeader(http.StatusTeapot)
}

// newExchangeMiddleware builds the middleware with a nil oauthServer. This is
// safe for the fall-through paths under test: ServeHTTP returns before
// dereferencing m.oauthServer in every case exercised here.
func newExchangeMiddleware(next http.Handler) *machinePrincipalExchangeMiddleware {
	return &machinePrincipalExchangeMiddleware{
		next:              next,
		oauthServer:       nil,
		machinePrincipals: map[string]config.MachinePrincipalConfig{},
	}
}

func TestServeHTTPFallThrough(t *testing.T) {
	tokenExchangeForm := url.Values{"grant_type": {grantTypeTokenExchange}}.Encode()

	tests := []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
	}{
		{
			name:   "non-POST request to token path falls through",
			method: http.MethodGet,
			path:   oauthserver.EndpointPathToken,
		},
		{
			name:        "POST to non-token path falls through",
			method:      http.MethodPost,
			path:        "/some/other/path",
			contentType: "application/x-www-form-urlencoded",
			body:        tokenExchangeForm,
		},
		{
			name:        "POST to token path with non-exchange grant_type falls through",
			method:      http.MethodPost,
			path:        oauthserver.EndpointPathToken,
			contentType: "application/x-www-form-urlencoded",
			body:        url.Values{"grant_type": {"authorization_code"}}.Encode(),
		},
		{
			name:        "POST to token path with exchange grant_type but malformed body falls through",
			method:      http.MethodPost,
			path:        oauthserver.EndpointPathToken,
			contentType: "application/x-www-form-urlencoded",
			// %zz is an invalid percent-escape, so ParseForm returns an error.
			body: "grant_type=" + grantTypeTokenExchange + "&subject_token=%zz",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sentinel := &sentinelHandler{}
			m := newExchangeMiddleware(sentinel)

			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			rec := httptest.NewRecorder()

			m.ServeHTTP(rec, req)

			require.True(t, sentinel.called, "expected request to fall through to next handler")
			require.Equal(t, http.StatusTeapot, rec.Code, "expected response written by next handler")
		})
	}
}

// jwtWithSub is a header.payload.sig token whose payload is
// {"sub":"system:serviceaccount:ns:sa"}. Only the payload is decoded by
// extractJWTSub; the signature is never verified at this layer.
const jwtWithSub = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQ6bnM6c2EiLCJpc3MiOiJodHRwczovL2V4YW1wbGUuY29tIn0.sig"

// jwtNoSub is a token whose payload has no sub claim, so extractJWTSub returns "".
const jwtNoSub = "eyJhbGciOiJSUzI1NiJ9.eyJpc3MiOiJodHRwczovL2V4YW1wbGUuY29tIn0.sig"

// TestServeHTTPNonMatchingSubjectFallsThrough verifies that a token-exchange
// request whose subject is NOT a configured machine principal is handed to the
// wrapped library handler rather than diverted to the reduced exchange path.
func TestServeHTTPNonMatchingSubjectFallsThrough(t *testing.T) {
	sentinel := &sentinelHandler{}
	m := &machinePrincipalExchangeMiddleware{
		next:        sentinel,
		oauthServer: nil, // fall-through happens before any oauthServer deref
		machinePrincipals: map[string]config.MachinePrincipalConfig{
			"system:serviceaccount:ns:sa": {Email: "sa@machine.example.com"},
		},
	}

	form := url.Values{
		"grant_type":         {grantTypeTokenExchange},
		"subject_token":      {jwtNoSub}, // sub is empty → not in the principals map
		"subject_token_type": {"urn:ietf:params:oauth:token-type:jwt"},
		"resource":           {"https://muster.example.com"},
	}.Encode()

	req := httptest.NewRequest(http.MethodPost, oauthserver.EndpointPathToken, strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	m.ServeHTTP(rec, req)

	require.True(t, sentinel.called, "non-principal exchange must fall through to the library handler")
	require.Equal(t, http.StatusTeapot, rec.Code)
}

// TestServeHTTPMatchedPrincipalRequiresResource verifies the matched-principal
// path enforces RFC 8707 (resource required) and emits security headers, matching
// the library token handler's contract.
func TestServeHTTPMatchedPrincipalRequiresResource(t *testing.T) {
	sentinel := &sentinelHandler{}
	m := &machinePrincipalExchangeMiddleware{
		next:        sentinel,
		oauthServer: &oauthserver.Server{Config: &oauthserver.Config{Issuer: "https://muster.example.com"}},
		machinePrincipals: map[string]config.MachinePrincipalConfig{
			"system:serviceaccount:ns:sa": {Email: "sa@machine.example.com"},
		},
	}

	form := url.Values{
		"grant_type":         {grantTypeTokenExchange},
		"subject_token":      {jwtWithSub},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:jwt"},
		// resource intentionally omitted
	}.Encode()

	req := httptest.NewRequest(http.MethodPost, oauthserver.EndpointPathToken, strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	m.ServeHTTP(rec, req)

	require.False(t, sentinel.called, "matched principal must not fall through")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t,
		`{"error":"invalid_request","error_description":"resource is required (RFC 8707)"}`,
		rec.Body.String())
	require.Equal(t, "no-store, no-cache, must-revalidate, private", rec.Header().Get("Cache-Control"),
		"security headers must be set on machine-principal responses")
}

func TestWriteExchangeErrorGeneric(t *testing.T) {
	rec := httptest.NewRecorder()

	writeExchangeError(rec, errors.New("boom"))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t,
		`{"error":"invalid_grant","error_description":"subject token invalid or rejected"}`,
		rec.Body.String())
}

func TestWriteJSONError(t *testing.T) {
	rec := httptest.NewRecorder()

	writeJSONError(rec, "invalid_request", "missing parameter", http.StatusUnauthorized)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t,
		`{"error":"invalid_request","error_description":"missing parameter"}`,
		rec.Body.String())
}
