package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
			name:       "missing code",
			query:      "state=some-state",
			wantStatus: http.StatusBadRequest,
			wantInBody: "missing required parameters",
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	handler := NewHandler(client)

	req := httptest.NewRequest("GET", "/oauth/callback?error=access_denied&error_description=User+denied+access", nil)
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

	encodedState, err := client.stateStore.GenerateState("session-1", "user-1", "test-server", "https://idp.example.com", "verifier",
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
