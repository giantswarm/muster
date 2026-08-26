package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// dcrTestServer is a minimal AS stub: RFC 8414 metadata plus an RFC 7591
// registration endpoint that records requests and issues configurable
// credentials.
type dcrTestServer struct {
	server *httptest.Server

	// Metadata switches
	advertiseCIMD bool
	advertiseDCR  bool

	// Registration behavior
	registrationStatus int // 0 → 201
	issueSecret        bool

	registrationCount atomic.Int64
	lastRegistration  atomic.Value // *pkgoauth.ClientMetadata
}

func newDCRTestServer(t *testing.T) *dcrTestServer {
	t.Helper()
	s := &dcrTestServer{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pkgoauth.WellKnownAuthorizationServer:
			metadata := map[string]any{
				"issuer":                                s.server.URL,
				"authorization_endpoint":                s.server.URL + "/authorize",
				"token_endpoint":                        s.server.URL + "/token",
				"code_challenge_methods_supported":      []string{"S256"},
				"token_endpoint_auth_methods_supported": []string{"none"},
			}
			if s.advertiseCIMD {
				metadata["client_id_metadata_document_supported"] = true
			}
			if s.advertiseDCR {
				metadata["registration_endpoint"] = s.server.URL + "/register"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(metadata)

		case "/register":
			s.registrationCount.Add(1)
			var req pkgoauth.ClientMetadata
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			s.lastRegistration.Store(&req)

			if s.registrationStatus != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(s.registrationStatus)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":             "invalid_client_metadata",
					"error_description": "rejected for testing",
				})
				return
			}

			resp := map[string]any{
				"client_id": "dcr-client-123",
			}
			if s.issueSecret {
				resp["client_secret"] = "dcr-secret-456"
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(resp)

		case "/token":
			_ = r.ParseForm()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at-" + r.Form.Get("client_id") + "|" + r.Form.Get("client_secret"),
				"token_type":   "Bearer",
				"expires_in":   3600,
			})

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

// upstreamClientID extracts the client_id from the upstream authorization URL
// stored with the state behind a muster-hosted start URL.
func upstreamClientID(t *testing.T, client *Client, startURL string) string {
	t.Helper()
	parsed, err := url.Parse(startURL)
	if err != nil {
		t.Fatalf("failed to parse start URL: %v", err)
	}
	state := client.stateStore.Update(parsed.Query().Get("state"), func(*OAuthState) {})
	if state == nil {
		t.Fatal("state should be stored")
	}
	upstream, err := url.Parse(state.AuthorizationURL)
	if err != nil {
		t.Fatalf("failed to parse upstream URL: %v", err)
	}
	return upstream.Query().Get("client_id")
}

func TestClient_GenerateAuthURL_PrefersCIMDWhenAdvertised(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseCIMD = true
	as.advertiseDCR = true // both advertised — CIMD must win, no registration

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	startURL, method, err := client.GenerateAuthURL(context.Background(), "session-1", "user-1", "server-1", as.server.URL, "openid")
	if err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}
	if method != ClientIDMethodCIMD {
		t.Errorf("expected method %q, got %q", ClientIDMethodCIMD, method)
	}
	if got := upstreamClientID(t, client, startURL); got != "https://muster.example.com/.well-known/oauth-client.json" {
		t.Errorf("expected CIMD URL as client_id, got %q", got)
	}
	if n := as.registrationCount.Load(); n != 0 {
		t.Errorf("expected no DCR registration when CIMD is advertised, got %d", n)
	}
}

func TestClient_GenerateAuthURL_FallsBackToDCR(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseDCR = true

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	startURL, method, err := client.GenerateAuthURL(context.Background(), "session-1", "user-1", "server-1", as.server.URL, "openid")
	if err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}
	if method != ClientIDMethodDCR {
		t.Errorf("expected method %q, got %q", ClientIDMethodDCR, method)
	}
	if got := upstreamClientID(t, client, startURL); got != "dcr-client-123" {
		t.Errorf("expected DCR-issued client_id, got %q", got)
	}

	// SEP-837: registration requests from the server-side proxy carry
	// application_type "web"; RFC 7591 forbids client_id on the request.
	reg, _ := as.lastRegistration.Load().(*pkgoauth.ClientMetadata)
	if reg == nil {
		t.Fatal("expected a registration request to have been recorded")
	}
	if reg.ApplicationType != "web" {
		t.Errorf("expected application_type web on the DCR request, got %q", reg.ApplicationType)
	}
	if reg.ClientID != "" {
		t.Errorf("DCR request must not carry client_id, got %q", reg.ClientID)
	}
	if reg.TokenEndpointAuthMethod != "none" {
		t.Errorf("expected token_endpoint_auth_method none, got %q", reg.TokenEndpointAuthMethod)
	}

	// A second flow for the same issuer reuses the stored credentials.
	_, method2, err := client.GenerateAuthURL(context.Background(), "session-2", "user-2", "server-1", as.server.URL, "openid")
	if err != nil {
		t.Fatalf("second GenerateAuthURL failed: %v", err)
	}
	if method2 != ClientIDMethodDCR {
		t.Errorf("expected method %q on reuse, got %q", ClientIDMethodDCR, method2)
	}
	if n := as.registrationCount.Load(); n != 1 {
		t.Errorf("expected exactly one DCR registration, got %d", n)
	}
}

func TestClient_GenerateAuthURL_CIMDFallbackWhenRegistrationFails(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseDCR = true
	as.registrationStatus = http.StatusBadRequest

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	startURL, method, err := client.GenerateAuthURL(context.Background(), "session-1", "user-1", "server-1", as.server.URL, "openid")
	if err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}
	if method != ClientIDMethodCIMDFallback {
		t.Errorf("expected method %q, got %q", ClientIDMethodCIMDFallback, method)
	}
	if got := upstreamClientID(t, client, startURL); got != "https://muster.example.com/.well-known/oauth-client.json" {
		t.Errorf("expected CIMD URL as fallback client_id, got %q", got)
	}
}

func TestClient_ExchangeCode_UsesDCRCredentials(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseDCR = true
	as.issueSecret = true

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	// The auth flow registers via DCR and stores the credentials.
	_, _, err := client.GenerateAuthURL(context.Background(), "session-1", "user-1", "server-1", as.server.URL, "openid")
	if err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}

	// The code exchange must present the same registered credentials. The
	// stub token endpoint echoes client_id|client_secret in the access token.
	token, err := client.ExchangeCode(context.Background(), "code-1", "verifier-1", as.server.URL)
	if err != nil {
		t.Fatalf("ExchangeCode failed: %v", err)
	}
	if token.AccessToken != "at-dcr-client-123|dcr-secret-456" {
		t.Errorf("token exchange did not use the DCR credentials: %q", token.AccessToken)
	}
}

func TestClient_GetClientCredentialsForIssuer(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseDCR = true
	as.issueSecret = true

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	// Before any flow: no stored credentials, no registration triggered —
	// the CIMD URL is returned and the registration endpoint stays untouched.
	clientID, secret := client.GetClientCredentialsForIssuer(context.Background(), as.server.URL)
	if clientID != "https://muster.example.com/.well-known/oauth-client.json" || secret != "" {
		t.Errorf("expected CIMD URL with no secret before registration, got %q / %q", clientID, secret)
	}
	if n := as.registrationCount.Load(); n != 0 {
		t.Errorf("credential lookup must not register, got %d registrations", n)
	}

	// After a flow registered via DCR, the stored credentials are returned.
	if _, _, err := client.GenerateAuthURL(context.Background(), "session-1", "user-1", "server-1", as.server.URL, "openid"); err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}
	clientID, secret = client.GetClientCredentialsForIssuer(context.Background(), as.server.URL)
	if clientID != "dcr-client-123" || secret != "dcr-secret-456" {
		t.Errorf("expected DCR credentials, got %q / %q", clientID, secret)
	}
}

func TestClientCredentialStore_ExpiredSecretIsDropped(t *testing.T) {
	store := NewClientCredentialStore()
	defer store.Stop()

	store.Store("https://as.example.com", &pkgoauth.ClientCredentials{
		Issuer:                "https://as.example.com",
		ClientID:              "abc",
		ClientSecret:          "secret",
		ClientSecretExpiresAt: time.Now().Add(-time.Minute),
	})
	if got := store.Get("https://as.example.com"); got != nil {
		t.Errorf("expected expired credentials to be dropped, got %+v", got)
	}

	// Public-client credentials (no secret) never expire.
	store.Store("https://as2.example.com", &pkgoauth.ClientCredentials{
		Issuer:   "https://as2.example.com",
		ClientID: "public",
	})
	if got := store.Get("https://as2.example.com"); got == nil || got.ClientID != "public" {
		t.Errorf("expected public-client credentials to be returned, got %+v", got)
	}
}

func TestClient_ServesCIMDWithApplicationType(t *testing.T) {
	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	cimd := client.GetClientMetadata()
	if cimd.ApplicationType != "web" {
		t.Errorf("expected CIMD application_type web (SEP-837), got %q", cimd.ApplicationType)
	}
	if cimd.ClientID == "" {
		t.Error("CIMD must carry its own client_id")
	}
	if !strings.Contains(cimd.ClientID, ".well-known/oauth-client.json") {
		t.Errorf("CIMD client_id should be the CIMD URL, got %q", cimd.ClientID)
	}
}
