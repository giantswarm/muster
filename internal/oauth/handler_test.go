package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_HandleCallback_MissingParams(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback")
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback")
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback")
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback")
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback")
	defer client.Stop()

	handler := NewHandler(client)

	rr := httptest.NewRecorder()
	handler.renderSuccessPage(rr, "mcp-kubernetes")

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rr.Code)
	}

	body := rr.Body.String()

	// Check for expected content
	checks := []string{
		"Authentication Successful",
		"mcp-kubernetes",
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback")
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback")
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

	client := NewClient(clientID, publicURL, callbackPath)
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
		var cimd ClientMetadata
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
			client := NewClient(tc.clientID, tc.publicURL, "/oauth/proxy/callback")
			defer client.Stop()

			cimdURL := client.GetCIMDURL()
			if cimdURL != tc.expectedCIMD {
				t.Errorf("Expected CIMD URL %q, got %q", tc.expectedCIMD, cimdURL)
			}
		})
	}
}
