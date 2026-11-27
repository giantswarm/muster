package mock

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestReferenceBackendAuthorization(t *testing.T) {
	for _, signed := range []bool{false, true} {
		name := "opaque"
		if signed {
			name = "signed JWT"
		}
		t.Run(name, func(t *testing.T) {
			authorizationServer := NewOAuthServer(OAuthServerConfig{SignTokens: true, TokenLifetime: time.Hour, AutoApprove: true})
			_, err := authorizationServer.Start(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, authorizationServer.Stop(t.Context())) })
			issuer := authorizationServer.GetIssuerURL()
			configuration := ProtectedMCPServerConfig{
				Name: "reference-backend", OAuthServer: authorizationServer, Issuer: issuer,
				Transport: HTTPTransportStreamableHTTP,
				Tools:     []ToolConfig{{Name: "operate", EchoAuthorization: true, Responses: []ToolResponse{{Response: map[string]any{"status": "updated"}}}}},
			}
			var accessToken string
			if signed {
				configuration.TrustJWKSURL = issuer + "/jwks"
				configuration.ExpectedAudience = "reference-backend"
				configuration.ExpectedIssuer = issuer
				accessToken, err = authorizationServer.MintSignedJWT(map[string]any{"iss": issuer, "sub": "backend-alice", "aud": "reference-backend", "exp": time.Now().Add(time.Hour).Unix()}, "at+jwt")
				require.NoError(t, err)
			} else {
				code := authorizationServer.GenerateAuthCodeWithSubject("backend-client", "https://caller.example/callback", "mcp:write", "state", "", "", "backend-alice", "")
				tokens, exchangeError := authorizationServer.SimulateCallback(code)
				require.NoError(t, exchangeError)
				accessToken = tokens.AccessToken
			}
			backend, err := NewProtectedMCPServer(configuration)
			require.NoError(t, err)
			_, err = backend.Start(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, backend.Stop(t.Context())) })
			consumer, err := client.NewStreamableHttpClient(backend.Endpoint(), transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + accessToken}))
			require.NoError(t, err)
			require.NoError(t, consumer.Start(t.Context()))
			t.Cleanup(func() { require.NoError(t, consumer.Close()) })
			initialization := mcp.InitializeRequest{}
			initialization.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
			initialization.Params.ClientInfo = mcp.Implementation{Name: "reference-proof", Version: "1"}
			_, err = consumer.Initialize(t.Context(), initialization)
			require.NoError(t, err)
			result, err := consumer.CallTool(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{
				Name: "operate", Meta: &mcp.Meta{AdditionalFields: map[string]any{"com.example/session": "untrusted-user-reference"}},
			}})
			require.NoError(t, err)
			require.False(t, result.IsError)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.Contains(t, string(encoded), "backend-alice")
			require.Contains(t, string(encoded), issuer)
			require.NotContains(t, string(encoded), accessToken)
			require.NotContains(t, string(encoded), "untrusted-user-reference")
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, backend.Endpoint(), strings.NewReader(`{}`))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer platform-token")
			response, err := http.DefaultClient.Do(request)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, http.StatusUnauthorized, response.StatusCode)
		})
	}
}
