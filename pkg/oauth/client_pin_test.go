package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestClient_PinMetadata_AnswersWithoutDiscovery(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.NotFound(w, nil)
	}))
	defer server.Close()

	client := NewClient()
	client.PinMetadata(server.URL+"/", &Metadata{
		AuthorizationEndpoint: server.URL + "/login/oauth/authorize",
		TokenEndpoint:         server.URL + "/login/oauth/access_token",
	})

	metadata, err := client.DiscoverMetadata(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("DiscoverMetadata with a pin: %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("pinned issuer must not be fetched, got %d requests", hits.Load())
	}
	if metadata.Issuer != server.URL {
		t.Errorf("issuer defaults to the pinned issuer, got %q", metadata.Issuer)
	}
	if metadata.AuthorizationEndpoint != server.URL+"/login/oauth/authorize" {
		t.Errorf("unexpected authorization endpoint %q", metadata.AuthorizationEndpoint)
	}
	if !metadata.SupportsS256PKCE() {
		t.Error("a pin without code_challenge_methods_supported asserts S256")
	}
}

func TestClient_PinMetadata_SurvivesCacheClearAndDiscovery(t *testing.T) {
	client := NewClient()
	issuer := "https://github.com/login/oauth"
	pinned := &Metadata{
		AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
		TokenEndpoint:         "https://github.com/login/oauth/access_token",
	}
	client.PinMetadata(issuer, pinned)

	// A discovery result for the same issuer does not replace the pin.
	client.cacheMetadata(issuer, &Metadata{Issuer: issuer, TokenEndpoint: "https://elsewhere/token"})
	metadata, err := client.DiscoverMetadata(context.Background(), issuer)
	if err != nil {
		t.Fatalf("DiscoverMetadata: %v", err)
	}
	if metadata.TokenEndpoint != pinned.TokenEndpoint {
		t.Errorf("pin overwritten by discovery: %q", metadata.TokenEndpoint)
	}

	client.ClearMetadataCache()
	metadata, err = client.DiscoverMetadata(context.Background(), issuer)
	if err != nil {
		t.Fatalf("DiscoverMetadata after ClearMetadataCache: %v", err)
	}
	if metadata.TokenEndpoint != pinned.TokenEndpoint {
		t.Errorf("pin lost on ClearMetadataCache: %q", metadata.TokenEndpoint)
	}

	// Pinning again replaces the earlier pin.
	client.PinMetadata(issuer, &Metadata{
		AuthorizationEndpoint: pinned.AuthorizationEndpoint,
		TokenEndpoint:         "https://github.example/token",
	})
	metadata, _ = client.DiscoverMetadata(context.Background(), issuer)
	if metadata.TokenEndpoint != "https://github.example/token" {
		t.Errorf("re-pin did not replace the pin: %q", metadata.TokenEndpoint)
	}
}

// A caller whose pins live under another key (a server outside muster's
// namespace) discovers what the authorization server publishes, never the pin
// of the plain issuer, and that discovery is cached apart from the pin.
func TestClient_DiscoverPublishedMetadata_IgnoresAndKeepsThePin(t *testing.T) {
	var hits atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != WellKnownAuthorizationServer {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"` + server.URL + `","authorization_endpoint":"` + server.URL + `/published/authorize","token_endpoint":"` + server.URL + `/published/token"}`))
	}))
	defer server.Close()

	client := NewClient()
	client.PinMetadata(server.URL, &Metadata{AuthorizationEndpoint: server.URL + "/pinned/authorize", TokenEndpoint: server.URL + "/pinned/token"})

	for range 2 {
		published, err := client.DiscoverPublishedMetadata(context.Background(), server.URL)
		if err != nil {
			t.Fatalf("DiscoverPublishedMetadata: %v", err)
		}
		if published.TokenEndpoint != server.URL+"/published/token" {
			t.Errorf("published token endpoint = %q, want the server's own", published.TokenEndpoint)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("metadata requests = %d, want one: the published document is cached next to the pin", hits.Load())
	}
	if pinned, _ := client.DiscoverMetadata(context.Background(), server.URL); pinned.TokenEndpoint != server.URL+"/pinned/token" {
		t.Errorf("the pin answers DiscoverMetadata, got %q", pinned.TokenEndpoint)
	}
}
