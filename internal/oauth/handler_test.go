package oauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_HandleCallback_MissingParams(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/callback")
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/callback")
	defer client.Stop()

	handler := NewHandler(client)

	req := httptest.NewRequest("GET", "/oauth/callback?error=access_denied&error_description=User+denied+access", nil)
	rr := httptest.NewRecorder()

	handler.HandleCallback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "User denied access") {
		t.Errorf("Expected body to contain error description, got %q", body)
	}
}

func TestHandler_HandleCallback_InvalidState(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/callback")
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/callback")
	defer client.Stop()

	handler := NewHandler(client)

	// Verify handler implements http.Handler
	var _ http.Handler = handler

	req := httptest.NewRequest("GET", "/oauth/callback", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should return a response (error due to missing params is expected)
	if rr.Code == 0 {
		t.Error("Expected a response status code")
	}
}

func TestHandler_RenderSuccessPage(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/callback")
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/callback")
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/callback")
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
