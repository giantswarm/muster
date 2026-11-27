package aggregator

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

func TestIsLoopbackListener(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost":   true,
		"127.0.0.1":   true,
		"127.0.0.2":   true,
		"::1":         true,
		"":            false,
		"0.0.0.0":     false,
		"::":          false,
		"10.0.0.5":    false,
		"muster.home": false,
	} {
		require.Equal(t, want, isLoopbackListener(host), host)
	}
}

func TestCreateHTTPMuxWarnsForUnauthenticatedListeners(t *testing.T) {
	for _, test := range []struct {
		host string
		warn bool
	}{
		{host: "localhost"},
		{host: "127.0.0.1"},
		{host: "::1"},
		{host: "", warn: true},
		{host: "0.0.0.0", warn: true},
		{host: "::", warn: true},
		{host: "10.0.0.5", warn: true},
	} {
		t.Run(test.host, func(t *testing.T) {
			logs := captureLog(t)
			t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, os.Stderr) })
			aggregator := &AggregatorServer{config: AggregatorConfig{Host: test.host}}
			handler, err := aggregator.createHTTPMux(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusAccepted)
			}))
			require.NoError(t, err)
			warning := "muster listens beyond loopback without inbound OAuth"
			if test.warn {
				require.Contains(t, logs.String(), warning)
				require.Contains(t, logs.String(), "Enable muster.oauth.server")
			} else {
				require.NotContains(t, logs.String(), warning)
			}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/mcp", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			require.Equal(t, http.StatusAccepted, response.Code)
		})
	}
}
