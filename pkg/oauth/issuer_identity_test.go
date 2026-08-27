package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDiscoverMetadata_RejectsIssuerMismatch locks in RFC 8414 §3.3: a
// metadata document that names a different authorization server is refused,
// so nothing downstream can compare against an issuer the server picked.
func TestDiscoverMetadata_RejectsIssuerMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != WellKnownAuthorizationServer {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Metadata{
			Issuer:                "https://evil.example.com",
			AuthorizationEndpoint: "https://evil.example.com/authorize",
			TokenEndpoint:         "https://evil.example.com/token",
		})
	}))
	defer server.Close()

	client := NewClient(WithHTTPClient(server.Client()))
	_, err := client.DiscoverMetadata(t.Context(), server.URL)
	if err == nil {
		t.Fatal("expected the mismatched issuer to be refused")
	}
	if !strings.Contains(err.Error(), "issuer mismatch") {
		t.Errorf("expected the error to name the mismatch, got: %v", err)
	}
}

// TestDiscoverMetadata_RejectsMissingIssuer covers a document with no issuer
// at all: there is nothing to verify against, so it cannot be accepted.
func TestDiscoverMetadata_RejectsMissingIssuer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != WellKnownAuthorizationServer {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Metadata{
			AuthorizationEndpoint: "https://auth.example.com/authorize",
		})
	}))
	defer server.Close()

	client := NewClient(WithHTTPClient(server.Client()))
	if _, err := client.DiscoverMetadata(t.Context(), server.URL); err == nil {
		t.Fatal("expected a document without an issuer to be refused")
	}
}

// TestDiscoverMetadata_AcceptsTrailingSlashDifference pins that a trailing
// slash is not a difference: issuers are published both ways in the wild.
func TestDiscoverMetadata_AcceptsTrailingSlashDifference(t *testing.T) {
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != WellKnownAuthorizationServer {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Metadata{
			Issuer:                "http://" + server.Listener.Addr().String() + "/",
			AuthorizationEndpoint: "http://" + server.Listener.Addr().String() + "/authorize",
		})
	})
	server.Start()
	defer server.Close()

	client := NewClient(WithHTTPClient(server.Client()))
	if _, err := client.DiscoverMetadata(t.Context(), server.URL); err != nil {
		t.Fatalf("expected the trailing slash to be accepted, got: %v", err)
	}
}
