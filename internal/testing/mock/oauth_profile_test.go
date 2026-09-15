package mock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// startProfileServer starts a mock authorization server with the profile's
// bundle applied over cfg.
func startProfileServer(t *testing.T, profile Profile, cfg OAuthServerConfig) *OAuthServer {
	t.Helper()
	profile.Apply(&cfg)
	server := NewOAuthServer(cfg)
	ctx := context.Background()
	if _, err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start mock AS: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop(ctx) })
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := server.WaitForReady(readyCtx); err != nil {
		t.Fatalf("mock AS not ready: %v", err)
	}
	return server
}

// getJSON GETs the URL and returns the status and the decoded body (nil when
// the body is not a JSON object).
func getJSON(t *testing.T, rawURL string, header http.Header) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// registerClient POSTs an RFC 7591 registration and returns the status and
// the decoded response.
func registerClient(t *testing.T, server *OAuthServer) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(server.GetIssuerURL()+"/register", "application/json",
		strings.NewReader(`{"redirect_uris":["https://muster.example.com/oauth/proxy/callback"],"application_type":"web"}`))
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// exchangeCode mints an authorization code for clientID and redeems it at
// the token endpoint, returning the status and the decoded token response.
func exchangeCode(t *testing.T, server *OAuthServer, clientID, scope string) (int, map[string]any) {
	t.Helper()
	code := server.GenerateAuthCode(clientID, "https://muster.example.com/oauth/proxy/callback", scope, "s", "", "", "")
	resp, err := http.PostForm(server.GetTokenURL(), url.Values{
		"grant_type": {"authorization_code"},
		"code":       {code},
		"client_id":  {clientID},
	})
	if err != nil {
		t.Fatalf("token request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func assertKeys(t *testing.T, body map[string]any, present, absent []string) {
	t.Helper()
	for _, k := range present {
		if _, ok := body[k]; !ok {
			t.Errorf("response lacks %q: %v", k, body)
		}
	}
	for _, k := range absent {
		if _, ok := body[k]; ok {
			t.Errorf("response must not carry %q: %v", k, body)
		}
	}
}

func TestParseProfile(t *testing.T) {
	for _, name := range []string{"", "github", "dex", "pro"} {
		if p, err := ParseProfile(name); err != nil || string(p) != name {
			t.Errorf("ParseProfile(%q) = %q, %v", name, p, err)
		}
	}
	if _, err := ParseProfile("okta"); err == nil || !strings.Contains(err.Error(), "okta") {
		t.Errorf("unknown profile must be refused by name, got %v", err)
	}
	if Profile("").Bundle() != (ProfileBundle{}) {
		t.Error("the empty profile must switch nothing on")
	}
}

func TestProfile_ApplyKeepsExplicitFlags(t *testing.T) {
	cfg := OAuthServerConfig{PKCERequired: true, SupportsCIMD: true}
	ProfileDex.Apply(&cfg)
	if !cfg.PKCERequired || !cfg.SupportsCIMD {
		t.Error("Apply must leave flags outside the bundle untouched")
	}
	if !cfg.SupportsDCR || !cfg.OmitTokenScope {
		t.Error("Apply must switch the bundle's flags on")
	}
	if cfg.OmitDiscovery || cfg.RequireRegisteredClient || cfg.OmitTokenExpiry {
		t.Error("Apply must not switch on what the bundle leaves alone")
	}
}

// GitHub: nothing to discover, nothing to register, a pre-registered client
// only; `scope` and `expires_in` absent from the token response.
func TestProfileGitHub_Wire(t *testing.T) {
	server := startProfileServer(t, ProfileGitHub, OAuthServerConfig{ClientID: "github-client"})

	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		if status, _ := getJSON(t, server.GetIssuerURL()+path, nil); status != http.StatusNotFound {
			t.Errorf("%s: GitHub publishes no discovery document, got %d", path, status)
		}
	}
	if status, _ := registerClient(t, server); status != http.StatusNotFound {
		t.Errorf("GitHub offers no RFC 7591 registration, got %d", status)
	}

	// An unknown client is refused directly at /authorize, never redirected.
	resp := authorize(t, server, url.Values{
		"client_id":    {"somebody-else"},
		"redirect_uri": {"https://muster.example.com/oauth/proxy/callback"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown client at /authorize: got %d, want 400", resp.StatusCode)
	}

	status, token := exchangeCode(t, server, "github-client", "repo")
	if status != http.StatusOK {
		t.Fatalf("code exchange for the pre-registered client: got %d %v", status, token)
	}
	assertKeys(t, token, []string{"access_token", "refresh_token", "token_type"}, []string{"scope", "expires_in"})

	b := ProfileGitHub.Bundle()
	if !b.ResourceOmitsMetadata || !b.GrantScopeSubject || !b.PinWithEndpoints {
		t.Errorf("GitHub's resources answer a bare 401, grants are the person's and the pin carries endpoints: %+v", b)
	}
}

// Dex: a discovery document with registration, `scope` omitted, an id_token
// and `expires_in` with every token.
func TestProfileDex_Wire(t *testing.T) {
	server := startProfileServer(t, ProfileDex, OAuthServerConfig{})

	status, metadata := getJSON(t, server.GetMetadataURL(), nil)
	if status != http.StatusOK {
		t.Fatalf("Dex serves a discovery document, got %d", status)
	}
	assertKeys(t, metadata, []string{"issuer", "authorization_endpoint", "token_endpoint", "registration_endpoint"},
		[]string{"client_id_metadata_document_supported"})

	status, registration := registerClient(t, server)
	if status != http.StatusCreated {
		t.Fatalf("Dex registers clients, got %d %v", status, registration)
	}
	clientID, _ := registration["client_id"].(string)
	assertKeys(t, registration, []string{"client_id", "registration_client_uri", "registration_access_token"}, nil)

	status, token := exchangeCode(t, server, clientID, "openid profile")
	if status != http.StatusOK {
		t.Fatalf("code exchange: got %d %v", status, token)
	}
	assertKeys(t, token, []string{"access_token", "id_token", "expires_in"}, []string{"scope"})
}

// pro (MCP TypeScript SDK): discovery with registration, a registration
// response without the RFC 7592 pair, invalid_client answered directly at
// /authorize, a token endpoint that refuses unregistered clients, and
// registrations that a restart forgets.
func TestProfilePro_Wire(t *testing.T) {
	server := startProfileServer(t, ProfilePro, OAuthServerConfig{})

	status, metadata := getJSON(t, server.GetMetadataURL(), nil)
	if status != http.StatusOK {
		t.Fatalf("pro serves a discovery document, got %d", status)
	}
	assertKeys(t, metadata, []string{"registration_endpoint"}, nil)

	status, registration := registerClient(t, server)
	if status != http.StatusCreated {
		t.Fatalf("pro registers clients, got %d %v", status, registration)
	}
	clientID, _ := registration["client_id"].(string)
	assertKeys(t, registration, []string{"client_id"}, []string{"registration_client_uri", "registration_access_token"})
	if status, _ := getJSON(t, server.GetIssuerURL()+"/register/"+clientID, nil); status != http.StatusNotFound {
		t.Errorf("pro has no RFC 7592 endpoint, got %d", status)
	}

	resp := authorize(t, server, url.Values{
		"client_id":    {"dcr-forgotten"},
		"redirect_uri": {"https://muster.example.com/oauth/proxy/callback"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown client at /authorize: got %d, want a direct 400", resp.StatusCode)
	}
	var authorizeErr map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&authorizeErr); err != nil || authorizeErr["error"] != "invalid_client" {
		t.Errorf("unknown client must be answered with a JSON invalid_client object, got %v (%v)", authorizeErr, err)
	}

	if status, body := exchangeCode(t, server, "https://muster.example.com/muster-agent.json", "mcp:read"); status != http.StatusUnauthorized || body["error"] != "invalid_client" {
		t.Errorf("an unregistered client_id at the token endpoint: got %d %v, want 401 invalid_client", status, body)
	}
	if status, token := exchangeCode(t, server, clientID, "mcp:read"); status != http.StatusOK {
		t.Errorf("the registered client's code exchange: got %d %v", status, token)
	}

	port := server.Port()
	forgotten, err := server.Restart(context.Background())
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if forgotten != 1 || server.RegisteredClientCount() != 0 || server.RegistrationCount() != 1 {
		t.Errorf("a pro restart forgets its registrations: forgotten=%d known=%d served=%d",
			forgotten, server.RegisteredClientCount(), server.RegistrationCount())
	}
	if server.Port() != port {
		t.Errorf("restart must keep the port: %d -> %d", port, server.Port())
	}
	readyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.WaitForReady(readyCtx); err != nil {
		t.Fatalf("not serving after restart: %v", err)
	}
	if status, _ := getJSON(t, server.GetMetadataURL(), nil); status != http.StatusOK {
		t.Errorf("discovery after restart: got %d", status)
	}
	if status, body := exchangeCode(t, server, clientID, "mcp:read"); status != http.StatusUnauthorized || body["error"] != "invalid_client" {
		t.Errorf("the forgotten client's code exchange after restart: got %d %v, want 401 invalid_client", status, body)
	}
}

// The default (no profile) registers clients with the RFC 7592 pair and
// answers the client read: 200 with the right token, 401 otherwise and once
// the registration is forgotten.
func TestOAuthServer_RegistrationRead(t *testing.T) {
	server := startProfileServer(t, "", OAuthServerConfig{SupportsDCR: true})

	status, registration := registerClient(t, server)
	if status != http.StatusCreated {
		t.Fatalf("registration: got %d %v", status, registration)
	}
	readURI, _ := registration["registration_client_uri"].(string)
	token, _ := registration["registration_access_token"].(string)
	if readURI == "" || token == "" {
		t.Fatalf("a well-behaved server hands out the RFC 7592 pair: %v", registration)
	}
	bearer := http.Header{"Authorization": {"Bearer " + token}}

	if status, body := getJSON(t, readURI, bearer); status != http.StatusOK || body["client_id"] != registration["client_id"] {
		t.Errorf("client read with its token: got %d %v", status, body)
	}
	if status, _ := getJSON(t, readURI, http.Header{"Authorization": {"Bearer wrong"}}); status != http.StatusUnauthorized {
		t.Errorf("client read with a wrong token: got %d, want 401", status)
	}
	if status, _ := getJSON(t, readURI, nil); status != http.StatusUnauthorized {
		t.Errorf("client read without a token: got %d, want 401", status)
	}
	if forgotten := server.ForgetRegisteredClients(); forgotten != 1 {
		t.Fatalf("forgot %d registrations, want 1", forgotten)
	}
	if status, _ := getJSON(t, readURI, bearer); status != http.StatusUnauthorized {
		t.Errorf("client read of a forgotten registration: got %d, want 401", status)
	}
}

// A restart without ForgetRegistrationsOnRestart keeps the registrations, as
// a server with a persistent client store does.
func TestOAuthServer_RestartKeepsRegistrationsByDefault(t *testing.T) {
	server := startProfileServer(t, ProfileDex, OAuthServerConfig{})
	if status, _ := registerClient(t, server); status != http.StatusCreated {
		t.Fatalf("registration: got %d", status)
	}
	forgotten, err := server.Restart(context.Background())
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if forgotten != 0 || server.RegisteredClientCount() != 1 {
		t.Errorf("restart must keep the registrations: forgotten=%d known=%d", forgotten, server.RegisteredClientCount())
	}
	if _, err := (&OAuthServer{}).Restart(context.Background()); err == nil {
		t.Error("restarting a server that never started must fail")
	}
}
