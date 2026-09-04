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
