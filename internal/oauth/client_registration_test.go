package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// forgetfulAS is a DCR-only authorization server stub that can forget its
// registered clients, the way an AS with an in-memory client store does when
// it restarts. Its authorization endpoint behaves like the MCP TypeScript
// SDK's: an unknown client_id gets a direct 400 invalid_client, every other
// parameter error is redirected to the client's redirect_uri.
type forgetfulAS struct {
	server *httptest.Server

	// offer7592 adds registration_client_uri / registration_access_token to
	// registration responses and serves the RFC 7592 read endpoint.
	offer7592 bool

	// authorizeInconclusive renders a login page for every authorization
	// request, so the probe cannot tell whether the client is known.
	authorizeInconclusive bool

	// authorizeAcceptsAll skips the client check at the authorization
	// endpoint; only the token endpoint enforces registration.
	authorizeAcceptsAll bool

	mu            sync.Mutex
	clients       map[string]bool
	nextID        int
	registrations int
	authorizeHits int
	readHits      int
	tokenClients  []string
}

func newForgetfulAS(t *testing.T) *forgetfulAS {
	t.Helper()
	as := &forgetfulAS{clients: make(map[string]bool)}
	as.server = httptest.NewServer(http.HandlerFunc(as.serve))
	t.Cleanup(as.server.Close)
	return as
}

func (as *forgetfulAS) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == pkgoauth.WellKnownAuthorizationServer:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                as.server.URL,
			"authorization_endpoint":                as.server.URL + "/authorize",
			"token_endpoint":                        as.server.URL + "/token",
			"registration_endpoint":                 as.server.URL + "/register",
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})

	case r.URL.Path == "/register" && r.Method == http.MethodPost:
		as.mu.Lock()
		as.nextID++
		as.registrations++
		id := fmt.Sprintf("dcr-%d", as.nextID)
		as.clients[id] = true
		as.mu.Unlock()

		resp := map[string]any{"client_id": id}
		if as.offer7592 {
			resp["registration_client_uri"] = as.server.URL + "/register/" + id
			resp["registration_access_token"] = "rat-" + id
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)

	case strings.HasPrefix(r.URL.Path, "/register/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(r.URL.Path, "/register/")
		as.mu.Lock()
		as.readHits++
		known := as.clients[id]
		as.mu.Unlock()
		if !known || r.Header.Get("Authorization") != "Bearer rat-"+id {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_token"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"client_id": id})

	case r.URL.Path == "/authorize":
		as.mu.Lock()
		as.authorizeHits++
		known := as.clients[r.URL.Query().Get("client_id")]
		as.mu.Unlock()

		if as.authorizeInconclusive {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><body>Sign in</body></html>"))
			return
		}
		if !known && !as.authorizeAcceptsAll {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client", "error_description": "Invalid client_id"})
			return
		}
		target, _ := url.Parse(r.URL.Query().Get("redirect_uri"))
		q := target.Query()
		if r.URL.Query().Get("response_type") != "code" {
			q.Set("error", "invalid_request")
		} else {
			q.Set("code", "code-1")
			q.Set("state", r.URL.Query().Get("state"))
		}
		target.RawQuery = q.Encode()
		http.Redirect(w, r, target.String(), http.StatusFound)

	case r.URL.Path == "/token":
		_ = r.ParseForm()
		clientID := r.Form.Get("client_id")
		as.mu.Lock()
		as.tokenClients = append(as.tokenClients, clientID)
		known := as.clients[clientID]
		as.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if !known {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client", "error_description": "client not registered"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-" + clientID,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})

	default:
		http.NotFound(w, r)
	}
}

// forget drops every registered client, like an AS restart with an
// in-memory client store.
func (as *forgetfulAS) forget() {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.clients = make(map[string]bool)
}

func (as *forgetfulAS) counts() (registrations, authorizeHits, readHits int) {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.registrations, as.authorizeHits, as.readHits
}

func newRegistrationTestClient() *Client {
	return NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
}

// startFlow runs GenerateAuthURL and returns the client_id the upstream
// authorization URL carries.
func startFlow(t *testing.T, client *Client, issuer string) string {
	t.Helper()
	startURL, _, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID:  "session-1",
		UserID:     "user-1",
		ServerName: "server-1",
		Issuer:     issuer,
		Scope:      "mcp:read",
	})
	if err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}
	return upstreamClientID(t, client, startURL)
}

func TestClient_GenerateAuthURL_ReregistersWhenASForgotClient(t *testing.T) {
	as := newForgetfulAS(t)
	client := newRegistrationTestClient()
	defer client.Stop()

	if got := startFlow(t, client, as.server.URL); got != "dcr-1" {
		t.Fatalf("first flow should use the first registration, got %q", got)
	}

	// The AS restarts and forgets muster. The stored credentials look valid
	// to muster (no expiry, nothing revoked through muster).
	as.forget()

	got := startFlow(t, client, as.server.URL)
	if got != "dcr-2" {
		t.Errorf("flow after the AS forgot the client should use a fresh registration, got %q", got)
	}
	if registrations, _, _ := as.counts(); registrations != 2 {
		t.Errorf("expected exactly one re-registration (2 total), got %d", registrations)
	}
	if stored := client.credStore.Get(as.server.URL); stored == nil || stored.ClientID != "dcr-2" {
		t.Errorf("store should hold the fresh registration, got %+v", stored)
	}

	// The re-registration is one-shot: a healthy registration is reused.
	if got := startFlow(t, client, as.server.URL); got != "dcr-2" {
		t.Errorf("healthy registration should be reused, got %q", got)
	}
	if registrations, _, _ := as.counts(); registrations != 2 {
		t.Errorf("healthy registration must not register again, got %d registrations", registrations)
	}
}

func TestClient_GenerateAuthURL_KeepsRegistrationWhenProbeInconclusive(t *testing.T) {
	as := newForgetfulAS(t)
	as.authorizeInconclusive = true
	client := newRegistrationTestClient()
	defer client.Stop()

	startFlow(t, client, as.server.URL)
	as.forget()

	// Nothing definitive says the registration is gone, so muster must not
	// churn registrations: another user's refresh tokens are bound to the
	// current client_id.
	if got := startFlow(t, client, as.server.URL); got != "dcr-1" {
		t.Errorf("inconclusive probe must keep the stored registration, got %q", got)
	}
	if registrations, _, _ := as.counts(); registrations != 1 {
		t.Errorf("inconclusive probe must not register again, got %d registrations", registrations)
	}
}

func TestClient_GenerateAuthURL_ReregistersViaRFC7592Read(t *testing.T) {
	as := newForgetfulAS(t)
	as.offer7592 = true
	// The authorization endpoint tells nothing, so only the RFC 7592 read can
	// have decided the outcome.
	as.authorizeInconclusive = true
	client := newRegistrationTestClient()
	defer client.Stop()

	startFlow(t, client, as.server.URL)
	stored := client.credStore.Get(as.server.URL)
	if stored == nil || stored.RegistrationClientURI == "" || stored.RegistrationAccessToken == "" {
		t.Fatalf("registration management data should be stored, got %+v", stored)
	}

	// Alive: the read confirms the client and no probe is needed.
	if got := startFlow(t, client, as.server.URL); got != "dcr-1" {
		t.Errorf("confirmed registration should be reused, got %q", got)
	}
	if _, authorizeHits, readHits := as.counts(); readHits != 1 || authorizeHits != 0 {
		t.Errorf("expected one RFC 7592 read and no authorize probe, got reads=%d probes=%d", readHits, authorizeHits)
	}

	as.forget()

	if got := startFlow(t, client, as.server.URL); got != "dcr-2" {
		t.Errorf("401 on the RFC 7592 read should trigger re-registration, got %q", got)
	}
	if registrations, _, _ := as.counts(); registrations != 2 {
		t.Errorf("expected 2 registrations, got %d", registrations)
	}
}

func TestClient_ExchangeCode_InvalidClientDropsRegistration(t *testing.T) {
	as := newForgetfulAS(t)
	// The authorization endpoint accepts anything, so the probe sees a
	// healthy AS and the loss only surfaces at the token endpoint.
	as.authorizeAcceptsAll = true
	client := newRegistrationTestClient()
	defer client.Stop()

	startFlow(t, client, as.server.URL)
	as.forget()

	if got := startFlow(t, client, as.server.URL); got != "dcr-1" {
		t.Fatalf("probe cannot see the loss here; expected the stale client_id, got %q", got)
	}

	_, err := client.ExchangeCode(context.Background(), "code-1", "verifier-1", as.server.URL, "")
	if err == nil || !pkgoauth.IsInvalidClientError(err) {
		t.Fatalf("expected invalid_client from the token endpoint, got %v", err)
	}
	if stored := client.credStore.Get(as.server.URL); stored != nil {
		t.Errorf("invalid_client at the token endpoint must drop the stored registration, still have %+v", stored)
	}

	// The user's retry registers again and the exchange succeeds.
	if got := startFlow(t, client, as.server.URL); got != "dcr-2" {
		t.Errorf("retry should use a fresh registration, got %q", got)
	}
	token, err := client.ExchangeCode(context.Background(), "code-2", "verifier-2", as.server.URL, "")
	if err != nil {
		t.Fatalf("exchange with the fresh registration failed: %v", err)
	}
	if token.AccessToken != "at-dcr-2" {
		t.Errorf("exchange did not present the fresh client_id: %q", token.AccessToken)
	}
}

func TestClient_ExchangeCode_OtherErrorsKeepRegistration(t *testing.T) {
	as := newForgetfulAS(t)
	client := newRegistrationTestClient()
	defer client.Stop()
	startFlow(t, client, as.server.URL)

	// Swap the token endpoint answer to invalid_grant for the known client.
	as.mu.Lock()
	as.clients["dcr-1"] = true
	as.mu.Unlock()
	grantRejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
	}))
	defer grantRejecting.Close()

	metadata, err := client.DiscoverMetadata(context.Background(), as.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	metadata.TokenEndpoint = grantRejecting.URL + "/token"

	_, err = client.ExchangeCode(context.Background(), "code-1", "verifier-1", as.server.URL, "")
	if err == nil || pkgoauth.IsInvalidClientError(err) {
		t.Fatalf("expected a non-invalid_client error, got %v", err)
	}
	if stored := client.credStore.Get(as.server.URL); stored == nil || stored.ClientID != "dcr-1" {
		t.Errorf("invalid_grant must not touch the registration, got %+v", stored)
	}
}

func TestClient_ResetClientRegistration(t *testing.T) {
	as := newForgetfulAS(t)
	client := newRegistrationTestClient()
	defer client.Stop()

	if client.ResetClientRegistration(as.server.URL) {
		t.Error("reset without stored credentials must report false")
	}

	startFlow(t, client, as.server.URL)
	if !client.ResetClientRegistration(as.server.URL) {
		t.Error("reset with stored credentials must report true")
	}
	if stored := client.credStore.Get(as.server.URL); stored != nil {
		t.Errorf("reset must drop the stored registration, still have %+v", stored)
	}
	if got := startFlow(t, client, as.server.URL); got != "dcr-2" {
		t.Errorf("flow after reset should register again, got %q", got)
	}
}

func TestManager_CreateAuthChallenge_ResetClientRegistration(t *testing.T) {
	as := newForgetfulAS(t)
	client := newRegistrationTestClient()
	defer client.Stop()
	manager := &Manager{client: client, serverConfigs: make(map[string]*AuthServerConfig)}

	params := AuthChallengeParams{
		SessionID: "session-1", UserID: "user-1", ServerName: "server-1",
		Issuer: as.server.URL, Scope: "mcp:read",
	}
	if _, err := manager.CreateAuthChallenge(context.Background(), params); err != nil {
		t.Fatal(err)
	}

	params.ResetClientRegistration = true
	challenge, err := manager.CreateAuthChallenge(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if registrations, _, _ := as.counts(); registrations != 2 {
		t.Errorf("reset must force a new registration, got %d registrations", registrations)
	}
	if !strings.Contains(challenge.Message, "discarded as requested") {
		t.Errorf("challenge should tell the user the registration was reset, got %q", challenge.Message)
	}
	if challenge.ClientIDMethod != ClientIDMethodDCR {
		t.Errorf("expected dcr after reset, got %q", challenge.ClientIDMethod)
	}
}

func TestHandler_HandleCallback_InvalidClientDropsRegistration(t *testing.T) {
	as := newForgetfulAS(t)
	client := newRegistrationTestClient()
	defer client.Stop()
	handler := NewHandler(client)

	startFlow(t, client, as.server.URL)

	// access_denied says nothing about the registration.
	state := storeCallbackState(t, client, as.server.URL)
	rr := httptest.NewRecorder()
	handler.HandleCallback(rr, httptest.NewRequest(http.MethodGet,
		"/oauth/proxy/callback?error=access_denied&state="+url.QueryEscape(state), nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected the error page, got %d", rr.Code)
	}
	if stored := client.credStore.Get(as.server.URL); stored == nil {
		t.Fatal("access_denied must keep the registration")
	}

	// invalid_client redirected to the callback means the AS refused the
	// registration itself.
	state = storeCallbackState(t, client, as.server.URL)
	rr = httptest.NewRecorder()
	handler.HandleCallback(rr, httptest.NewRequest(http.MethodGet,
		"/oauth/proxy/callback?error=invalid_client&error_description=Invalid+client_id&state="+url.QueryEscape(state), nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected the error page, got %d", rr.Code)
	}
	if stored := client.credStore.Get(as.server.URL); stored != nil {
		t.Errorf("invalid_client on the callback must drop the registration, still have %+v", stored)
	}
}
