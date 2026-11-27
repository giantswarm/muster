package aggregator

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

func TestCreateHTTPMux_OriginWarnings(t *testing.T) {
	for _, path := range []string{"/mcp", "/sse", "/message"} {
		for _, test := range []struct {
			name        string
			origins     []string
			wantWarning bool
		}{
			{name: "no Origin"},
			{name: "same origin", origins: []string{"https://muster.example.com"}},
			{name: "explicit default port", origins: []string{"https://muster.example.com:443"}},
			{name: "allowed browser", origins: []string{"https://portal.example.com"}},
			{name: "foreign origin", origins: []string{"https://foreign.example"}, wantWarning: true},
			{name: "null origin", origins: []string{"null"}, wantWarning: true},
			{name: "empty origin", origins: []string{""}, wantWarning: true},
			{name: "multiple origins", origins: []string{"https://muster.example.com", "https://foreign.example"}, wantWarning: true},
			{name: "origin with path", origins: []string{"https://muster.example.com/mcp"}, wantWarning: true},
			{name: "origin with credentials", origins: []string{"https://username:password@muster.example.com"}, wantWarning: true},
			{name: "empty fragment", origins: []string{"https://muster.example.com#"}, wantWarning: true},
			{name: "empty port", origins: []string{"https://muster.example.com:"}, wantWarning: true},
			{name: "wildcard", origins: []string{"https://*.example.com"}, wantWarning: true},
			{name: "different port", origins: []string{"https://muster.example.com:8443"}, wantWarning: true},
		} {
			t.Run(path+"/"+test.name, func(t *testing.T) {
				var logs bytes.Buffer
				logging.InitForCLI(logging.LevelInfo, &logs)
				t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, os.Stderr) })
				aggregator := &AggregatorServer{config: AggregatorConfig{OAuthServer: OAuthServerConfig{Config: config.OAuthServerConfig{
					BaseURL:        "https://muster.example.com",
					AllowedOrigins: "https://portal.example.com",
				}}}}
				handler, err := aggregator.createHTTPMux(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writer.WriteHeader(http.StatusAccepted)
				}))
				require.NoError(t, err)
				request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://muster.example.com"+path, nil)
				for _, origin := range test.origins {
					request.Header.Add("Origin", origin)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				require.Equal(t, http.StatusAccepted, response.Code, "the overlap release only warns")
				if test.wantWarning {
					require.Contains(t, logs.String(), "DEPRECATED: Origin")
				} else {
					require.NotContains(t, logs.String(), "DEPRECATED: Origin")
				}
				require.NotContains(t, logs.String(), "password")
			})
		}
	}
}

func TestCreateHTTPMux_OriginWarningsBeforeAuthentication(t *testing.T) {
	issuer, caFile, _ := startSigningDex(t)
	var logs bytes.Buffer
	logging.InitForCLI(logging.LevelInfo, &logs)
	t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, os.Stderr) })
	t.Cleanup(func() { api.RegisterBrowserIdentity(nil); api.RegisterSessionSubjectResolver(nil) })
	aggregator := &AggregatorServer{
		ctx: t.Context(),
		config: AggregatorConfig{OAuthServer: OAuthServerConfig{Enabled: true, Config: config.OAuthServerConfig{
			Enabled: true, BaseURL: "https://muster.example.com", Provider: "dex", ExtraCAFile: caFile,
			Dex: config.DexConfig{IssuerURL: issuer, ClientID: "muster", ClientSecret: "fixture-client-secret"},
		}}},
	}
	handler, err := aggregator.createHTTPMux(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unauthenticated request reached the MCP handler")
	}))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, aggregator.oauthHTTPServer.Shutdown(context.WithoutCancel(t.Context()))) })
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions} {
		logs.Reset()
		request := httptest.NewRequestWithContext(t.Context(), method, "/mcp", nil)
		request.Header.Set("Origin", "https://foreign.example")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		require.Contains(t, logs.String(), "DEPRECATED: Origin", method)
		require.NotEqual(t, http.StatusForbidden, response.Code, "Origin is log-only")
	}
}
