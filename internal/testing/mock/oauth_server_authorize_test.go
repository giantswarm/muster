package mock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// startDCRServer starts a DCR-only mock AS and registers one client via its
// registration endpoint, returning the server and the issued client_id.
func startDCRServer(t *testing.T, config OAuthServerConfig) (*OAuthServer, string) {
	t.Helper()
	config.SupportsDCR = true
	config.RequireRegisteredClient = true
	config.PKCERequired = true
	server := NewOAuthServer(config)
	if _, err := server.Start(context.Background()); err != nil {
		t.Fatalf("failed to start mock AS: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop(context.Background()) })

	resp, err := http.Post(server.GetIssuerURL()+"/register", "application/json",
		strings.NewReader(`{"redirect_uris":["https://muster.example.com/oauth/proxy/callback"],"application_type":"web"}`))
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil || reg.ClientID == "" {
		t.Fatalf("bad registration response: %v", err)
	}
	return server, reg.ClientID
}

func authorize(t *testing.T, server *OAuthServer, params url.Values) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(server.GetAuthorizeURL() + "?" + params.Encode())
	if err != nil {
		t.Fatalf("authorize request failed: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestOAuthServer_Authorize_UnknownClientIsDirectInvalidClient(t *testing.T) {
	server, _ := startDCRServer(t, OAuthServerConfig{})

	resp := authorize(t, server, url.Values{
		"client_id":    {"dcr-forgotten"},
		"redirect_uri": {"https://muster.example.com/oauth/proxy/callback"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown client must be answered directly with 400, got %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("expected a JSON error object: %v", err)
	}
	if body["error"] != "invalid_client" {
		t.Errorf("error = %q, want invalid_client", body["error"])
	}
}

func TestOAuthServer_Authorize_KnownClientParameterErrorsRedirect(t *testing.T) {
	server, clientID := startDCRServer(t, OAuthServerConfig{})

	// No response_type: the client is known, so the error goes to its redirect_uri.
	resp := authorize(t, server, url.Values{
		"client_id":    {clientID},
		"redirect_uri": {"https://muster.example.com/oauth/proxy/callback"},
		"state":        {"s1"},
	})
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("known client parameter error must redirect, got %d", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Host != "muster.example.com" || location.Path != "/oauth/proxy/callback" {
		t.Errorf("redirect must target the redirect_uri, got %s", location)
	}
	if location.Query().Get("error") != "unsupported_response_type" || location.Query().Get("state") != "s1" {
		t.Errorf("redirect must carry the error and echo the state, got %s", location.RawQuery)
	}

	// Missing PKCE with a known client is redirected too.
	resp = authorize(t, server, url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {"https://muster.example.com/oauth/proxy/callback"},
		"response_type": {"code"},
	})
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("missing PKCE must redirect, got %d", resp.StatusCode)
	}
	location, _ = url.Parse(resp.Header.Get("Location"))
	if location.Query().Get("error") != "invalid_request" {
		t.Errorf("expected invalid_request, got %s", location.RawQuery)
	}

	// Missing redirect_uri cannot be redirected.
	resp = authorize(t, server, url.Values{"client_id": {clientID}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing redirect_uri must be a direct 400, got %d", resp.StatusCode)
	}
}

func TestOAuthServer_Authorize_AutoApproveStillIssuesCode(t *testing.T) {
	server, clientID := startDCRServer(t, OAuthServerConfig{AutoApprove: true})

	resp := authorize(t, server, url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://muster.example.com/oauth/proxy/callback"},
		"response_type":         {"code"},
		"state":                 {"s2"},
		"code_challenge":        {"challenge"},
		"code_challenge_method": {"S256"},
	})
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected the auto-approve redirect, got %d", resp.StatusCode)
	}
	location, _ := url.Parse(resp.Header.Get("Location"))
	if location.Query().Get("code") == "" || location.Query().Get("state") != "s2" {
		t.Errorf("expected code and state on the redirect, got %s", location.RawQuery)
	}
}

func TestOAuthServer_Authorize_AcceptsAnyClientWhenConfigured(t *testing.T) {
	server, _ := startDCRServer(t, OAuthServerConfig{AuthorizeAcceptsAnyClient: true})

	resp := authorize(t, server, url.Values{
		"client_id":    {"dcr-forgotten"},
		"redirect_uri": {"https://muster.example.com/oauth/proxy/callback"},
	})
	if resp.StatusCode != http.StatusFound {
		t.Errorf("authorize must not reveal the unknown client, got %d", resp.StatusCode)
	}

	// The token endpoint still enforces registration.
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {"x"}, "client_id": {"dcr-forgotten"},
	}
	tokenResp, err := http.PostForm(server.GetTokenURL(), form)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tokenResp.Body.Close() }()
	if tokenResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("token endpoint must still reject unknown clients, got %d", tokenResp.StatusCode)
	}
}

func TestOAuthServer_ForgetRegisteredClients(t *testing.T) {
	server, clientID := startDCRServer(t, OAuthServerConfig{})

	if got := server.RegisteredClientCount(); got != 1 {
		t.Fatalf("expected 1 registered client, got %d", got)
	}
	if forgotten := server.ForgetRegisteredClients(); forgotten != 1 {
		t.Errorf("expected to forget 1 client, got %d", forgotten)
	}
	if got := server.RegisteredClientCount(); got != 0 {
		t.Errorf("expected no registered clients, got %d", got)
	}
	if got := server.RegistrationCount(); got != 1 {
		t.Errorf("registration count must survive forgetting, got %d", got)
	}
	if server.isAcceptedClientID(clientID) {
		t.Errorf("forgotten client %s must not be accepted", clientID)
	}

	resp := authorize(t, server, url.Values{
		"client_id":    {clientID},
		"redirect_uri": {"https://muster.example.com/oauth/proxy/callback"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("forgotten client must be answered with invalid_client, got %d", resp.StatusCode)
	}
}
