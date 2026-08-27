package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/giantswarm/muster/internal/config"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

func TestHandler_HandleCallback_MissingParams(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	handler := NewHandler(client)

	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantInBody string
	}{
		{
			// The state is validated before the code is read, so an
			// unknown state is reported as an expired session.
			name:       "missing code",
			query:      "state=some-state",
			wantStatus: http.StatusBadRequest,
			wantInBody: "Authentication session expired",
		},
		{
			name:       "missing state",
			query:      "code=some-code",
			wantStatus: http.StatusBadRequest,
			wantInBody: "missing required parameters",
		},
		{
			name:       "both missing",
			query:      "",
			wantStatus: http.StatusBadRequest,
			wantInBody: "missing required parameters",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/oauth/callback?"+tc.query, nil)
			rr := httptest.NewRecorder()

			handler.HandleCallback(rr, req)

			if rr.Code != tc.wantStatus {
				t.Errorf("Expected status %d, got %d", tc.wantStatus, rr.Code)
			}

			body := rr.Body.String()
			if !strings.Contains(body, tc.wantInBody) {
				t.Errorf("Expected body to contain %q, got %q", tc.wantInBody, body)
			}
		})
	}
}

func TestHandler_HandleCallback_OAuthError(t *testing.T) {
	authServer := newAuthorizationServer(t, authorizationServerOptions{})
	defer authServer.Close()

	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	handler := NewHandler(client)
	encodedState := storeCallbackState(t, client, authServer.URL)

	req := httptest.NewRequest("GET", "/oauth/callback?error=access_denied&error_description=User+denied+access&state="+url.QueryEscape(encodedState), nil)
	rr := httptest.NewRecorder()

	handler.HandleCallback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	body := rr.Body.String()
	// We now use a generic error message to avoid leaking sensitive information
	// from OAuth provider error descriptions
	if !strings.Contains(body, "Authentication was denied or failed") {
		t.Errorf("Expected body to contain generic error message, got %q", body)
	}
}

// TestHandler_HandleCallback_ErrorWithoutValidState covers RFC 9207: an error
// response that cannot be attributed to a stored flow is rejected, and its
// error parameters are never shown.
func TestHandler_HandleCallback_ErrorWithoutValidState(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	handler := NewHandler(client)

	req := httptest.NewRequest("GET", "/oauth/callback?error=access_denied&error_description=User+denied+access", nil)
	rr := httptest.NewRecorder()

	handler.HandleCallback(rr, req)

	body := rr.Body.String()
	if strings.Contains(body, "Authentication was denied or failed") {
		t.Errorf("Expected the error response to be rejected, got %q", body)
	}
	if !strings.Contains(body, "missing required parameters") {
		t.Errorf("Expected body to report missing parameters, got %q", body)
	}
}

func TestHandler_HandleCallback_InvalidState(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	handler := NewHandler(client)

	// State that doesn't exist in the store
	req := httptest.NewRequest("GET", "/oauth/callback?code=auth-code&state=invalid-state", nil)
	rr := httptest.NewRecorder()

	handler.HandleCallback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "expired") {
		t.Errorf("Expected body to mention expired session, got %q", body)
	}
}

func TestHandler_ServeHTTP(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	handler := NewHandler(client)

	// Verify handler implements http.Handler
	var _ http.Handler = handler

	req := httptest.NewRequest("GET", "/oauth/proxy/callback", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should return a response (error due to missing params is expected)
	if rr.Code == 0 {
		t.Error("Expected a response status code")
	}
}

func TestHandler_RenderSuccessPage(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	handler := NewHandler(client)

	rr := httptest.NewRecorder()
	handler.renderSuccessPage(rr, testServerName)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rr.Code)
	}

	body := rr.Body.String()

	// Check for expected content
	checks := []string{
		"Authentication Successful",
		testServerName,
		"Muster",
		"close this window",
	}

	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("Expected body to contain %q", check)
		}
	}

	// Check content type
	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Expected Content-Type to be text/html, got %q", contentType)
	}
}

func TestHandler_RenderErrorPage(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	handler := NewHandler(client)

	rr := httptest.NewRecorder()
	handler.renderErrorPage(rr, "Test error message")

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	body := rr.Body.String()

	// Check for expected content
	checks := []string{
		"Authentication Failed",
		"Test error message",
		"Muster",
	}

	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("Expected body to contain %q", check)
		}
	}
}

func TestHandler_SecurityHeaders(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	handler := NewHandler(client)

	tests := []struct {
		name      string
		render    func(w http.ResponseWriter)
		headerMap map[string]string
	}{
		{
			name: "success page has security headers",
			render: func(w http.ResponseWriter) {
				handler.renderSuccessPage(w, "test-server")
			},
			headerMap: map[string]string{
				"X-Content-Type-Options":  "nosniff",
				"X-Frame-Options":         "DENY",
				"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'",
				"Referrer-Policy":         "no-referrer",
				"Cache-Control":           "no-store, no-cache, must-revalidate",
			},
		},
		{
			name: "error page has security headers",
			render: func(w http.ResponseWriter) {
				handler.renderErrorPage(w, "test error")
			},
			headerMap: map[string]string{
				"X-Content-Type-Options":  "nosniff",
				"X-Frame-Options":         "DENY",
				"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'",
				"Referrer-Policy":         "no-referrer",
				"Cache-Control":           "no-store, no-cache, must-revalidate",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.render(rr)

			for header, expectedValue := range tc.headerMap {
				actualValue := rr.Header().Get(header)
				if actualValue != expectedValue {
					t.Errorf("Expected header %q to be %q, got %q", header, expectedValue, actualValue)
				}
			}
		})
	}
}

func TestHandler_ServeCIMD(t *testing.T) {
	clientID := "https://muster.example.com/.well-known/oauth-client.json"
	publicURL := "https://muster.example.com"
	callbackPath := "/oauth/proxy/callback"

	client := NewClient(clientID, publicURL, callbackPath, "openid profile email")
	defer client.Stop()

	handler := NewHandler(client)

	t.Run("successful CIMD response", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/.well-known/oauth-client.json", nil)
		rr := httptest.NewRecorder()

		handler.ServeCIMD(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, rr.Code)
		}

		// Check content type
		contentType := rr.Header().Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Expected Content-Type application/json, got %q", contentType)
		}

		// Check CORS header
		corsHeader := rr.Header().Get("Access-Control-Allow-Origin")
		if corsHeader != "*" {
			t.Errorf("Expected CORS header '*', got %q", corsHeader)
		}

		// Check cache header
		cacheHeader := rr.Header().Get("Cache-Control")
		if !strings.Contains(cacheHeader, "max-age=3600") {
			t.Errorf("Expected Cache-Control with max-age=3600, got %q", cacheHeader)
		}

		// Parse and verify CIMD content
		var cimd pkgoauth.ClientMetadata
		if err := json.NewDecoder(rr.Body).Decode(&cimd); err != nil {
			t.Fatalf("Failed to decode CIMD: %v", err)
		}

		// Verify client_id
		if cimd.ClientID != clientID {
			t.Errorf("Expected client_id %q, got %q", clientID, cimd.ClientID)
		}

		// Verify redirect_uris
		expectedRedirectURI := publicURL + callbackPath
		if len(cimd.RedirectURIs) != 1 || cimd.RedirectURIs[0] != expectedRedirectURI {
			t.Errorf("Expected redirect_uris [%q], got %v", expectedRedirectURI, cimd.RedirectURIs)
		}

		// Verify grant types
		if len(cimd.GrantTypes) != 2 ||
			cimd.GrantTypes[0] != "authorization_code" ||
			cimd.GrantTypes[1] != "refresh_token" {
			t.Errorf("Expected grant_types [authorization_code, refresh_token], got %v", cimd.GrantTypes)
		}

		// Verify response types
		if len(cimd.ResponseTypes) != 1 || cimd.ResponseTypes[0] != "code" {
			t.Errorf("Expected response_types [code], got %v", cimd.ResponseTypes)
		}

		// Verify token endpoint auth method
		if cimd.TokenEndpointAuthMethod != "none" {
			t.Errorf("Expected token_endpoint_auth_method 'none', got %q", cimd.TokenEndpointAuthMethod)
		}

		// Verify client name
		if cimd.ClientName != "Muster MCP Aggregator" {
			t.Errorf("Expected client_name 'Muster MCP Aggregator', got %q", cimd.ClientName)
		}

		// Verify software ID
		if cimd.SoftwareID != "giantswarm-muster" {
			t.Errorf("Expected software_id 'giantswarm-muster', got %q", cimd.SoftwareID)
		}
	})

	t.Run("method not allowed for POST", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/.well-known/oauth-client.json", nil)
		rr := httptest.NewRecorder()

		handler.ServeCIMD(rr, req)

		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
		}
	})
}

func TestHandler_ServeCIMD_CustomScopes(t *testing.T) {
	clientID := "https://muster.example.com/.well-known/oauth-client.json"
	publicURL := "https://muster.example.com"
	callbackPath := "/oauth/proxy/callback"
	customScopes := "openid profile email offline_access https://mail.google.com/ https://www.googleapis.com/auth/calendar"

	client := NewClient(clientID, publicURL, callbackPath, customScopes)
	defer client.Stop()

	handler := NewHandler(client)

	req := httptest.NewRequest("GET", "/.well-known/oauth-client.json", nil)
	rr := httptest.NewRecorder()

	handler.ServeCIMD(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rr.Code)
	}

	// Parse and verify CIMD content
	var cimd pkgoauth.ClientMetadata
	if err := json.NewDecoder(rr.Body).Decode(&cimd); err != nil {
		t.Fatalf("Failed to decode CIMD: %v", err)
	}

	// Verify custom scopes are present in the CIMD
	if cimd.Scope != customScopes {
		t.Errorf("Expected scope %q, got %q", customScopes, cimd.Scope)
	}

	// Verify Google API scopes are included
	if !strings.Contains(cimd.Scope, "https://mail.google.com/") {
		t.Error("Expected CIMD scope to contain Gmail scope")
	}
	if !strings.Contains(cimd.Scope, "https://www.googleapis.com/auth/calendar") {
		t.Error("Expected CIMD scope to contain Calendar scope")
	}
}

func TestClient_GetCIMDURL(t *testing.T) {
	tests := []struct {
		name         string
		clientID     string
		publicURL    string
		expectedCIMD string
	}{
		{
			name:         "returns configured client ID as CIMD URL",
			clientID:     "https://muster.example.com/.well-known/oauth-client.json",
			publicURL:    "https://muster.example.com",
			expectedCIMD: "https://muster.example.com/.well-known/oauth-client.json",
		},
		{
			name:         "external CIMD URL",
			clientID:     "https://external.example.com/oauth-client.json",
			publicURL:    "https://muster.example.com",
			expectedCIMD: "https://external.example.com/oauth-client.json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient(tc.clientID, tc.publicURL, "/oauth/proxy/callback", "openid profile email")
			defer client.Stop()

			cimdURL := client.GetCIMDURL()
			if cimdURL != tc.expectedCIMD {
				t.Errorf("Expected CIMD URL %q, got %q", tc.expectedCIMD, cimdURL)
			}
		})
	}
}

// startTestHandler returns a handler whose state store holds one flow with
// the given upstream authorization URL, plus the encoded state for it.
func startTestHandler(t *testing.T, authorizationURL string, allowlist ...string) (*Handler, string) {
	t.Helper()
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	t.Cleanup(client.Stop)

	handler := NewHandler(client)
	var prefixes []*url.URL
	for _, raw := range allowlist {
		prefix, err := parsePostLoginRedirect(raw)
		if err != nil {
			t.Fatalf("parsePostLoginRedirect(%q): %v", raw, err)
		}
		prefixes = append(prefixes, prefix)
	}
	handler.SetPostLoginRedirectAllowlist(prefixes)

	encodedState, err := client.stateStore.GenerateState(StateParams{SessionID: "session-1", UserID: "user-1", ServerName: "test-server", Issuer: "https://idp.example.com", CodeVerifier: "verifier"},
		func(string) (string, error) { return authorizationURL, nil })
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	return handler, encodedState
}

func startRequest(handler *Handler, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/oauth/proxy/start?"+query, nil)
	rr := httptest.NewRecorder()
	handler.HandleStart(rr, req)
	return rr
}

func TestHandler_HandleStart_RedirectsUpstream(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")

	rr := startRequest(handler, "state="+url.QueryEscape(encodedState))

	if rr.Code != http.StatusFound {
		t.Fatalf("Expected status %d, got %d", http.StatusFound, rr.Code)
	}
	if got := rr.Header().Get("Location"); got != "https://idp.example.com/authorize?state=abc" {
		t.Errorf("Expected upstream Location, got %q", got)
	}
}

func TestHandler_HandleStart_RecordsAllowlistedRedirect(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc",
		"https://gateway.example.com/connectors")

	rr := startRequest(handler, "state="+url.QueryEscape(encodedState)+
		"&redirect="+url.QueryEscape("https://gateway.example.com/connectors/complete?s=gw-state-1"))

	if rr.Code != http.StatusFound {
		t.Fatalf("Expected status %d, got %d", http.StatusFound, rr.Code)
	}

	state := handler.client.stateStore.Update(encodedState, func(*OAuthState) {})
	if state == nil {
		t.Fatal("state disappeared")
	}
	if state.RedirectURI != "https://gateway.example.com/connectors/complete?s=gw-state-1" {
		t.Errorf("Expected redirect recorded on state, got %q", state.RedirectURI)
	}
}

func TestHandler_HandleStart_RecordsExactEntryPathRedirect(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc",
		"https://gateway.example.com/connectors")

	rr := startRequest(handler, "state="+url.QueryEscape(encodedState)+
		"&redirect="+url.QueryEscape("https://gateway.example.com/connectors?s=gw-state-1"))

	if rr.Code != http.StatusFound {
		t.Fatalf("Expected status %d, got %d", http.StatusFound, rr.Code)
	}
	state := handler.client.stateStore.Update(encodedState, func(*OAuthState) {})
	if state == nil {
		t.Fatal("state disappeared")
	}
	if state.RedirectURI != "https://gateway.example.com/connectors?s=gw-state-1" {
		t.Errorf("Expected exact-path redirect recorded on state, got %q", state.RedirectURI)
	}
}

func TestHandler_HandleStart_RejectsNonAllowlistedRedirect(t *testing.T) {
	tests := []struct {
		name     string
		redirect string
	}{
		{name: "other host", redirect: "https://evil.example.com/connectors/complete"},
		{name: "host suffix trick", redirect: "https://gateway.example.com.evil.example.com/connectors/complete"},
		{name: "other path", redirect: "https://gateway.example.com/other"},
		{name: "scheme downgrade", redirect: "http://gateway.example.com/connectors/complete"},
		{name: "userinfo trick", redirect: "https://gateway.example.com@evil.example.com/connectors"},
		{name: "not a url", redirect: "javascript:alert(1)"},
		{name: "prefix without segment boundary", redirect: "https://gateway.example.com/connectorsevil"},
		{name: "dot segments", redirect: "https://gateway.example.com/connectors/../admin"},
		{name: "encoded dot segments", redirect: "https://gateway.example.com/connectors/%2e%2e/admin"},
		{name: "mixed-case encoded dot segments", redirect: "https://gateway.example.com/connectors/.%2E/admin"},
		{name: "encoded slash hiding the boundary", redirect: "https://gateway.example.com/connectors%2f..%2fadmin"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc",
				"https://gateway.example.com/connectors")

			rr := startRequest(handler, "state="+url.QueryEscape(encodedState)+"&redirect="+url.QueryEscape(tc.redirect))

			if rr.Code != http.StatusFound {
				t.Fatalf("Expected login to proceed with status %d, got %d", http.StatusFound, rr.Code)
			}
			state := handler.client.stateStore.Update(encodedState, func(*OAuthState) {})
			if state == nil {
				t.Fatal("state disappeared")
			}
			if state.RedirectURI != "" {
				t.Errorf("Expected rejected redirect to leave state untouched, got %q", state.RedirectURI)
			}
		})
	}
}

func TestHandler_HandleStart_InvalidState(t *testing.T) {
	handler, _ := startTestHandler(t, "https://idp.example.com/authorize?state=abc")

	rr := startRequest(handler, "state=not-a-real-state")

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHandler_HandleStart_MissingState(t *testing.T) {
	handler, _ := startTestHandler(t, "https://idp.example.com/authorize?state=abc")

	rr := startRequest(handler, "")

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHandler_FinishSuccess_RedirectRecordedOnState(t *testing.T) {
	handler, _ := startTestHandler(t, "")

	req := httptest.NewRequest("GET", "/oauth/proxy/callback", nil)
	rr := httptest.NewRecorder()
	handler.finishSuccess(rr, req, &OAuthState{
		ServerName:  "gazelle mcp/pro",
		RedirectURI: "https://gateway.example.com/connectors/complete?s=gw-state-1",
	})

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("Expected status %d, got %d", http.StatusSeeOther, rr.Code)
	}
	location := rr.Header().Get("Location")
	want := "https://gateway.example.com/connectors/complete?s=gw-state-1&server=gazelle+mcp%2Fpro"
	if location != want {
		t.Errorf("Expected Location %q, got %q", want, location)
	}
}

func TestHandler_FinishSuccess_NoRedirectRendersSuccessPage(t *testing.T) {
	handler, _ := startTestHandler(t, "")

	req := httptest.NewRequest("GET", "/oauth/proxy/callback", nil)
	rr := httptest.NewRecorder()
	handler.finishSuccess(rr, req, &OAuthState{ServerName: "test-server"})

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "test-server") {
		t.Errorf("Expected success page to mention the server, got %q", rr.Body.String())
	}
}

// startCallbackFlow drives GenerateAuthURL against the stub AS and returns
// the handler plus the callback path a browser would be redirected to.
func startCallbackFlow(t *testing.T, client *Client, issuer string) string {
	t.Helper()
	startURL, _, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID:  "session-1",
		UserID:     "user-1",
		ServerName: "server-1",
		Issuer:     issuer,
		Resource:   "https://backend.example.com",
		Scope:      "openid",
	})
	if err != nil {
		t.Fatalf("GenerateAuthURL failed: %v", err)
	}
	parsed, err := url.Parse(startURL)
	if err != nil {
		t.Fatalf("failed to parse start URL: %v", err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatal("start URL carries no state")
	}
	return "/oauth/proxy/callback?code=code-1&state=" + url.QueryEscape(state)
}

// Browsers and IdP redirect pages deliver the callback navigation more than
// once (observed live: a second navigation 430ms after the first). The
// duplicate must re-render the success outcome, not "session expired" over a
// successful sign-in — and the code exchange must run exactly once.
func TestHandler_HandleCallback_DuplicateDeliveryRendersOutcome(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseCIMD = true

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()
	handler := NewHandler(client)

	callbackPath := startCallbackFlow(t, client, as.server.URL)

	first := httptest.NewRecorder()
	handler.HandleCallback(first, httptest.NewRequest("GET", callbackPath, nil))
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "Authentication Successful") {
		t.Fatalf("first delivery: expected success page, got status %d body %q", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	handler.HandleCallback(second, httptest.NewRequest("GET", callbackPath, nil))
	if second.Code != http.StatusOK {
		t.Errorf("duplicate delivery: expected status %d, got %d (body %q)", http.StatusOK, second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "Authentication Successful") {
		t.Errorf("duplicate delivery: expected the success outcome re-rendered, got %q", second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "server-1") {
		t.Errorf("duplicate delivery: expected the server name on the page, got %q", second.Body.String())
	}

	if n := as.tokenCount.Load(); n != 1 {
		t.Errorf("expected exactly one code exchange, got %d", n)
	}

	// A state that never completed still renders the error page.
	unknown := httptest.NewRecorder()
	handler.HandleCallback(unknown, httptest.NewRequest("GET", "/oauth/proxy/callback?code=x&state=never-issued", nil))
	if unknown.Code != http.StatusBadRequest {
		t.Errorf("unknown state: expected status %d, got %d", http.StatusBadRequest, unknown.Code)
	}
}

// A flow that recorded a post-login redirect re-issues the same redirect on a
// duplicate delivery, so the second navigation lands where the first did.
func TestHandler_HandleCallback_DuplicateDeliveryRepeatsRedirect(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseCIMD = true

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()
	handler := NewHandler(client)
	prefix, err := parsePostLoginRedirect("https://portal.example.com/servers")
	if err != nil {
		t.Fatalf("parsePostLoginRedirect: %v", err)
	}
	handler.SetPostLoginRedirectAllowlist([]*url.URL{prefix})

	callbackPath := startCallbackFlow(t, client, as.server.URL)
	state := httptest.NewRequest("GET", callbackPath, nil).URL.Query().Get("state")

	// The browser reaches the start endpoint with an allowlisted redirect
	// target, which is recorded on the state.
	start := httptest.NewRecorder()
	handler.HandleStart(start, httptest.NewRequest("GET",
		"/oauth/proxy/start?state="+url.QueryEscape(state)+"&redirect="+url.QueryEscape("https://portal.example.com/servers"), nil))
	if start.Code != http.StatusFound {
		t.Fatalf("start endpoint: expected redirect, got %d", start.Code)
	}

	first := httptest.NewRecorder()
	handler.HandleCallback(first, httptest.NewRequest("GET", callbackPath, nil))
	if first.Code != http.StatusSeeOther {
		t.Fatalf("first delivery: expected redirect %d, got %d (body %q)", http.StatusSeeOther, first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	handler.HandleCallback(second, httptest.NewRequest("GET", callbackPath, nil))
	if second.Code != http.StatusSeeOther {
		t.Fatalf("duplicate delivery: expected redirect %d, got %d (body %q)", http.StatusSeeOther, second.Code, second.Body.String())
	}
	if got, want := second.Header().Get("Location"), first.Header().Get("Location"); got != want {
		t.Errorf("duplicate delivery: expected Location %q, got %q", want, got)
	}
}

// The browser aborting the callback request (re-navigation, closed tab) must
// not cancel the flow: the state is already consumed, so the exchange and the
// session-connection setup can only complete on this request.
func TestHandler_HandleCallback_SurvivesClientDisconnect(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseCIMD = true

	manager := NewManager(config.OAuthMCPClientConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "https://muster.example.com/.well-known/oauth-client.json",
		CallbackPath: "/oauth/proxy/callback",
	})
	defer manager.Stop()

	reqCtx, abortRequest := context.WithCancel(context.Background())
	defer abortRequest()
	callbackCtxErr := make(chan error, 1)
	manager.SetAuthCompletionCallback(func(ctx context.Context, sessionID, userID, serverName, accessToken string) error {
		// The browser re-navigates mid-callback: the request context dies
		// while the session connection is being established.
		abortRequest()
		callbackCtxErr <- ctx.Err()
		return nil
	})

	callbackPath := startCallbackFlow(t, manager.client, as.server.URL)

	rr := httptest.NewRecorder()
	manager.handler.HandleCallback(rr, httptest.NewRequest("GET", callbackPath, nil).WithContext(reqCtx))

	if err := <-callbackCtxErr; err != nil {
		t.Errorf("completion callback context must survive the client disconnect, got %v", err)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d (body %q)", http.StatusOK, rr.Code, rr.Body.String())
	}
}

// Even a request whose context is already dead when processing starts (the
// connection dropped right after delivery) completes the consumed flow.
func TestHandler_HandleCallback_ExchangeSurvivesClientDisconnect(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseCIMD = true

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()
	handler := NewHandler(client)

	callbackPath := startCallbackFlow(t, client, as.server.URL)

	deadCtx, cancel := context.WithCancel(context.Background())
	cancel()

	rr := httptest.NewRecorder()
	handler.HandleCallback(rr, httptest.NewRequest("GET", callbackPath, nil).WithContext(deadCtx))

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Authentication Successful") {
		t.Errorf("expected the flow to complete despite the dead request context, got status %d body %q", rr.Code, rr.Body.String())
	}
	if n := as.tokenCount.Load(); n != 1 {
		t.Errorf("expected the code exchange to have run, got %d", n)
	}
}
