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
	registrationStatus int  // 0 → 201
	rejectScope        bool // reject requests carrying a scope member (Miro-style)
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

			if s.rejectScope && req.Scope != "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":             "invalid_client_metadata",
					"error_description": "Requested scopes are not valid: " + req.Scope,
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

// upstreamAuthQuery extracts the query of the upstream authorization URL
// stored with the state behind a muster-hosted start URL.
func upstreamAuthQuery(t *testing.T, client *Client, startURL string) url.Values {
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
	return upstream.Query()
}

// upstreamClientID extracts the client_id from the upstream authorization URL
// stored with the state behind a muster-hosted start URL.
func upstreamClientID(t *testing.T, client *Client, startURL string) string {
	t.Helper()
	return upstreamAuthQuery(t, client, startURL).Get("client_id")
}

func TestClient_GenerateAuthURL_PrefersCIMDWhenAdvertised(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseCIMD = true
	as.advertiseDCR = true // both advertised — CIMD must win, no registration

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	startURL, resolved, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID:  "session-1",
		UserID:     "user-1",
		ServerName: "server-1",
		Issuer:     as.server.URL,
		Scope:      "openid",
	})
	if err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}
	if resolved.Method != ClientIDMethodCIMD {
		t.Errorf("expected method %q, got %q", ClientIDMethodCIMD, resolved.Method)
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

	startURL, resolved, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID:  "session-1",
		UserID:     "user-1",
		ServerName: "server-1",
		Issuer:     as.server.URL,
		Scope:      "openid",
	})
	if err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}
	if resolved.Method != ClientIDMethodDCR {
		t.Errorf("expected method %q, got %q", ClientIDMethodDCR, resolved.Method)
	}
	if got := upstreamClientID(t, client, startURL); got != "dcr-client-123" {
		t.Errorf("expected DCR-issued client_id, got %q", got)
	}

	// SEP-837: registration requests from the server-side proxy carry
	// application_type "web"; RFC 7591 forbids client_id on the request, and
	// scope is omitted — ASes like Miro's reject scopes they don't know.
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
	if reg.Scope != "" {
		t.Errorf("DCR request must not carry scope, got %q", reg.Scope)
	}
	if reg.TokenEndpointAuthMethod != "none" {
		t.Errorf("expected token_endpoint_auth_method none, got %q", reg.TokenEndpointAuthMethod)
	}

	// A second flow for the same issuer reuses the stored credentials.
	_, resolved2, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID:  "session-2",
		UserID:     "user-2",
		ServerName: "server-1",
		Issuer:     as.server.URL,
		Scope:      "openid",
	})
	if err != nil {
		t.Fatalf("second GenerateAuthURL failed: %v", err)
	}
	if resolved2.Method != ClientIDMethodDCR {
		t.Errorf("expected method %q on reuse, got %q", ClientIDMethodDCR, resolved2.Method)
	}
	if n := as.registrationCount.Load(); n != 1 {
		t.Errorf("expected exactly one DCR registration, got %d", n)
	}
}

func TestClient_GenerateAuthURL_DCRFailedWhenRegistrationRejected(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseDCR = true
	as.registrationStatus = http.StatusBadRequest

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	startURL, resolved, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID:  "session-1",
		UserID:     "user-1",
		ServerName: "server-1",
		Issuer:     as.server.URL,
		Scope:      "openid",
	})
	if err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}
	// A rejected registration is reported as "dcr-failed" — not as
	// "cimd-fallback", which would falsely claim the AS advertises neither
	// mechanism — and carries the AS's rejection for the challenge message.
	if resolved.Method != ClientIDMethodDCRFailed {
		t.Errorf("expected method %q, got %q", ClientIDMethodDCRFailed, resolved.Method)
	}
	if resolved.RegistrationError == nil {
		t.Error("expected the registration rejection to be carried on the resolved client")
	} else if !strings.Contains(resolved.RegistrationError.Error(), "invalid_client_metadata") {
		t.Errorf("expected the AS's error on the resolved client, got %v", resolved.RegistrationError)
	}
	if got := upstreamClientID(t, client, startURL); got != "https://muster.example.com/.well-known/oauth-client.json" {
		t.Errorf("expected CIMD URL as fallback client_id, got %q", got)
	}
}

func TestClient_GenerateAuthURL_OmitsScopeFromRegistration(t *testing.T) {
	// Miro-style AS: any scope member on the RFC 7591 request is rejected
	// with invalid_client_metadata. Registration must still succeed because
	// muster omits scope entirely — the per-server scope rides on the
	// authorization request instead.
	as := newDCRTestServer(t)
	as.advertiseDCR = true
	as.rejectScope = true

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email groups offline_access")
	defer client.Stop()

	startURL, resolved, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID:  "session-1",
		UserID:     "user-1",
		ServerName: "server-1",
		Issuer:     as.server.URL,
		Scope:      "mcp:read",
	})
	if err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}
	if resolved.Method != ClientIDMethodDCR {
		t.Errorf("expected method %q, got %q", ClientIDMethodDCR, resolved.Method)
	}
	authQuery := upstreamAuthQuery(t, client, startURL)
	if got := authQuery.Get("client_id"); got != "dcr-client-123" {
		t.Errorf("expected DCR-issued client_id, got %q", got)
	}
	if reg, _ := as.lastRegistration.Load().(*pkgoauth.ClientMetadata); reg == nil || reg.Scope != "" {
		t.Errorf("registration request must not carry scope, got %+v", reg)
	}
	// The per-server scope still rides on the authorization request.
	if got := authQuery.Get("scope"); got != "mcp:read" {
		t.Errorf("expected the per-server scope on the authorization request, got %q", got)
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
	_, _, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID:  "session-1",
		UserID:     "user-1",
		ServerName: "server-1",
		Issuer:     as.server.URL,
		Scope:      "openid",
	})
	if err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}

	// The code exchange must present the same registered credentials. The
	// stub token endpoint echoes client_id|client_secret in the access token.
	token, err := client.ExchangeCode(context.Background(), "code-1", "verifier-1", as.server.URL, "")
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
	if _, _, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID:  "session-1",
		UserID:     "user-1",
		ServerName: "server-1",
		Issuer:     as.server.URL,
		Scope:      "openid",
	}); err != nil {
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
