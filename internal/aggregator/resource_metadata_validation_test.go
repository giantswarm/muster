package aggregator

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

func TestDiscoverProtectedResourceMetadata_ResourceValidation(t *testing.T) {
	for _, mode := range []string{"challenge", "well-known"} {
		for _, test := range []struct {
			name        string
			resource    string
			wantError   bool
			wantWarning bool
		}{
			{name: "exact resource", resource: "/mcp"},
			{name: "same-origin alias", resource: "/", wantWarning: true},
			{name: "missing resource", wantWarning: true},
			{name: "cross-origin resource", resource: "https://foreign.example/mcp", wantWarning: true},
			{name: "relative resource", resource: "relative/mcp", wantError: true},
			{name: "cross-origin fragment", resource: "https://foreign.example/mcp#fragment", wantError: true},
			{name: "cross-origin credentials", resource: "https://user:password@foreign.example/mcp", wantError: true},
		} {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				var logs bytes.Buffer
				logging.InitForCLI(logging.LevelInfo, &logs)
				t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, os.Stderr) })
				var backend *httptest.Server
				var metadataRequests atomic.Int32
				backend = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if request.URL.Path == "/mcp" {
						if mode == "challenge" {
							writer.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, backend.URL+"/.well-known/oauth-protected-resource/mcp"))
						}
						writer.WriteHeader(http.StatusUnauthorized)
						return
					}
					resource := test.resource
					metadataRequests.Add(1)
					if resource == "/mcp" || resource == "/" {
						resource = backend.URL + resource
					}
					writer.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(writer, `{"resource":%q,"authorization_servers":["https://issuer.example"]}`, resource)
				}))
				t.Cleanup(backend.Close)
				metadata, err := discoverProtectedResourceMetadata(t.Context(), backend.URL+"/mcp", nil)
				if test.wantError {
					require.Error(t, err)
					require.Nil(t, metadata, "an invalid resource must not retain its authorization server")
					return
				}
				require.NoError(t, err)
				require.Equal(t, "https://issuer.example", metadata.Issuer)
				resource := test.resource
				if resource == "/mcp" || resource == "/" {
					resource = backend.URL + resource
				}
				require.Equal(t, resource, metadata.Resource)
				require.Equal(t, int32(1), metadataRequests.Load(), "valid discovery must not fall back to another metadata request")
				if test.wantWarning {
					require.Contains(t, logs.String(), "DEPRECATED:")
				} else {
					require.NotContains(t, logs.String(), "DEPRECATED:")
				}
			})
		}
	}
}
