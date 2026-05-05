package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// TestStartAuthFlowWithOptions_RefusesWithoutS256PKCE locks in the
// MCP 2025-11-25 §"Authorization Code Protection" requirement: the agent
// flow refuses to start when the AS does not advertise S256 PKCE.
func TestStartAuthFlowWithOptions_RefusesWithoutS256PKCE(t *testing.T) {
	metadata := pkgoauth.Metadata{
		Issuer:                "https://auth.example.com",
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		TokenEndpoint:         "https://auth.example.com/token",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pkgoauth.WellKnownAuthorizationServer {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(metadata)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	c, err := NewClient(ClientConfig{
		TokenStoreConfig: TokenStoreConfig{StorageDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = c.StartAuthFlowWithOptions(context.Background(), "https://mcp.example/v1/mcp", server.URL, nil)
	if err == nil {
		t.Fatal("expected refusal when AS does not advertise S256 PKCE")
	}
	if !strings.Contains(err.Error(), "S256 PKCE") {
		t.Errorf("error must mention S256 PKCE, got: %v", err)
	}
}
