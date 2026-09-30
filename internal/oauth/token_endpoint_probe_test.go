package oauth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// TestProbeTokenEndpoint: the probe reaches the token endpoint the way a
// caller's exchange does -- the client credentials, the HTTP client -- with a
// subject token no issuer signed. An authorization server that is up rejects
// it with an OAuth error, which classifies as no failure of the server's;
// a gateway's error page classifies as the endpoint not answering.
func TestProbeTokenEndpoint(t *testing.T) {
	for name, tc := range map[string]struct {
		status      int
		contentType string
		body        string
		want        api.TokenExchangeFailureClass
	}{
		"authorization server rejects the subject token": {
			status: http.StatusUnauthorized, contentType: "application/json",
			body: `{"error":"invalid_request","error_description":"Unable to verify subject token."}`,
			want: api.TokenExchangeFailureNone,
		},
		"authorization server rejects the client": {
			status: http.StatusUnauthorized, contentType: "application/json",
			body: `{"error":"invalid_client","error_description":"Invalid client credentials."}`,
			want: api.TokenExchangeFailureCredentials,
		},
		"gateway answers 503 with a page": {
			status: http.StatusServiceUnavailable, contentType: "text/html",
			body: `<html><head><title>503 Service Temporarily Unavailable</title></head></html>`,
			want: api.TokenExchangeFailureEndpoint,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var form map[string]string
			var clientID string
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, r.ParseForm())
				form = map[string]string{
					"subject_token": r.PostForm.Get("subject_token"),
					"connector_id":  r.PostForm.Get("connector_id"),
				}
				clientID, _, _ = r.BasicAuth()
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{AllowPrivateIP: true, HTTPClient: srv.Client()})
			err := exchanger.Probe(t.Context(), &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: srv.URL + "/token",
				ConnectorID:      "remote-oidc",
				ClientID:         "muster-token-exchange",
				ClientSecret:     "not-logged",
			})

			require.Error(t, err)
			assert.Equal(t, tc.want, api.ClassifyTokenExchangeError(err), "%v", err)
			assert.Equal(t, probeSubjectToken, form["subject_token"])
			assert.Equal(t, "remote-oidc", form["connector_id"])
			assert.Equal(t, "muster-token-exchange", clientID)
			assert.Zero(t, exchanger.GetCacheStats().CurrentEntries, "a probe caches nothing")
		})
	}
}

// TestProbeTokenEndpointNotAnswering: nothing listens on the endpoint.
func TestProbeTokenEndpointNotAnswering(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	endpoint := srv.URL + "/token"
	client := srv.Client()
	srv.Close()

	exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{AllowPrivateIP: true, HTTPClient: client})
	err := exchanger.Probe(t.Context(), &api.TokenExchangeConfig{Enabled: true, DexTokenEndpoint: endpoint, ConnectorID: "remote-oidc"})
	require.Error(t, err)
	assert.Equal(t, api.TokenExchangeFailureEndpoint, api.ClassifyTokenExchangeError(err), "%v", err)
}
