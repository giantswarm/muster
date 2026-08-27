package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// newMetadataServer starts an authorization server publishing RFC 8414
// metadata with S256 PKCE, so the agent flow gets past its PKCE refusal.
func newMetadataServer(t *testing.T, issSupported bool) *httptest.Server {
	t.Helper()

	var metadataJSON []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pkgoauth.WellKnownAuthorizationServer {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(metadataJSON)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	metadataJSON, err := json.Marshal(pkgoauth.Metadata{
		Issuer:                        server.URL,
		AuthorizationEndpoint:         server.URL + "/authorize",
		TokenEndpoint:                 server.URL + "/token",
		CodeChallengeMethodsSupported: []string{"S256"},
		AuthorizationResponseIssParameterSupported: issSupported,
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	return server
}

// TestStartAuthFlow_SendsResource locks in RFC 8707 on the agent's
// authorization request: the muster server URL with the query and the fragment
// dropped, which is the value mcp-go derives for the refresh request.
func TestStartAuthFlow_SendsResource(t *testing.T) {
	server := newMetadataServer(t, false)

	client, err := NewClient(ClientConfig{
		TokenStoreConfig: TokenStoreConfig{StorageDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	authURL, err := client.StartAuthFlowWithOptions(t.Context(), "https://mcp.example.com/v1/mcp/", server.URL, nil)
	if err != nil {
		t.Fatalf("StartAuthFlowWithOptions: %v", err)
	}

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	want := "https://mcp.example.com/v1/mcp/"
	if got := parsed.Query().Get("resource"); got != want {
		t.Errorf("expected resource %q on the authorization URL, got %q", want, got)
	}
}

// TestWaitForCallback_RejectsMissingIss locks in RFC 9207: an authorization
// response without iss is refused when the server advertises the parameter.
func TestWaitForCallback_RejectsMissingIss(t *testing.T) {
	server := newMetadataServer(t, true)

	client, err := NewClient(ClientConfig{
		TokenStoreConfig: TokenStoreConfig{StorageDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.StartAuthFlowWithOptions(t.Context(), "https://mcp.example.com/v1/mcp", server.URL, nil); err != nil {
		t.Fatalf("StartAuthFlowWithOptions: %v", err)
	}

	flow := client.currentFlow
	go func() {
		callbackURL := flow.CallbackServer.GetRedirectURI() + "?code=auth-code&state=" + url.QueryEscape(flow.State)
		response, err := http.Get(callbackURL) //nolint:gosec // G107: URL built from the flow's own local callback server
		if err == nil {
			_ = response.Body.Close()
		}
	}()

	_, err = client.WaitForCallback(t.Context())
	if err == nil {
		t.Fatal("expected refusal when the response carries no iss")
	}
	if !strings.Contains(err.Error(), "authorization_response_iss_parameter_supported") {
		t.Errorf("expected the refusal to name the advertised iss support, got: %v", err)
	}
}
