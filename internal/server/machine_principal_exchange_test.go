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
