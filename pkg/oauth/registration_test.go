package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const probeRedirectURI = "https://muster.example.com/oauth/proxy/callback"

func TestClient_ProbeClientRegistration(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    RegistrationStatus
	}{
		{
			name: "error redirected to the registered redirect_uri means the client is known",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("client_id") != "dcr-1" || r.URL.Query().Get("redirect_uri") != probeRedirectURI {
					t.Errorf("probe carried unexpected parameters: %s", r.URL.RawQuery)
				}
				if r.URL.Query().Has("response_type") {
					t.Errorf("probe must not carry response_type, got %s", r.URL.RawQuery)
				}
				http.Redirect(w, r, probeRedirectURI+"?error=invalid_request&error_description=missing+response_type", http.StatusFound)
			},
			want: RegistrationActive,
		},
		{
			name: "redirect somewhere else is inconclusive",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://as.example.com/login", http.StatusFound)
			},
			want: RegistrationUnknown,
		},
		{
			name: "direct JSON invalid_client (MCP TypeScript SDK) means the client is gone",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"Invalid client_id"}`))
			},
			want: RegistrationGone,
		},
		{
			name: "direct page naming invalid_client means the client is gone",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "invalid_client", http.StatusBadRequest)
			},
			want: RegistrationGone,
		},
		{
			name: "direct 400 for another reason is inconclusive",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"response_type is required"}`))
			},
			want: RegistrationUnknown,
		},
		{
			name: "rendered login page is inconclusive",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("<html><body>Sign in</body></html>"))
			},
			want: RegistrationUnknown,
		},
		{
			name: "server error is inconclusive",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "invalid_client", http.StatusInternalServerError)
			},
			want: RegistrationUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			as := httptest.NewServer(tc.handler)
			defer as.Close()

			client := NewClient()
			got, err := client.ProbeClientRegistration(context.Background(), as.URL+"/authorize", "dcr-1", probeRedirectURI)
			if got != tc.want {
				t.Errorf("status = %s, want %s (err=%v)", got, tc.want, err)
			}
			if got != RegistrationUnknown && err != nil {
				t.Errorf("definitive status must not carry an error, got %v", err)
			}
			if got == RegistrationUnknown && err == nil {
				t.Error("inconclusive status must explain itself with an error")
			}
		})
	}
}

func TestClient_ProbeClientRegistration_Unreachable(t *testing.T) {
	as := httptest.NewServer(http.NotFoundHandler())
	as.Close()

	client := NewClient()
	got, err := client.ProbeClientRegistration(context.Background(), as.URL+"/authorize", "dcr-1", probeRedirectURI)
	if got != RegistrationUnknown || err == nil {
		t.Errorf("unreachable AS must be inconclusive, got %s / %v", got, err)
	}

	got, err = client.ProbeClientRegistration(context.Background(), "", "dcr-1", probeRedirectURI)
	if got != RegistrationUnknown || err == nil {
		t.Errorf("missing authorization endpoint must be inconclusive, got %s / %v", got, err)
	}
}

func TestClient_ReadClientRegistration(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   RegistrationStatus
	}{
		{"200 confirms the registration", http.StatusOK, RegistrationActive},
		{"401 means token or registration is gone (RFC 7592 §2.3)", http.StatusUnauthorized, RegistrationGone},
		{"403 is inconclusive", http.StatusForbidden, RegistrationUnknown},
		{"404 is inconclusive", http.StatusNotFound, RegistrationUnknown},
		{"500 is inconclusive", http.StatusInternalServerError, RegistrationUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/register/dcr-1" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get(HeaderAuthorization); got != "Bearer rat-1" {
					t.Errorf("Authorization = %q, want the registration access token as bearer", got)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"client_id":"dcr-1"}`))
			}))
			defer as.Close()

			client := NewClient()
			got, err := client.ReadClientRegistration(context.Background(), as.URL+"/register/dcr-1", "rat-1")
			if got != tc.want {
				t.Errorf("status = %s, want %s (err=%v)", got, tc.want, err)
			}
		})
	}

	t.Run("missing token or URI is inconclusive without a request", func(t *testing.T) {
		client := NewClient()
		if got, err := client.ReadClientRegistration(context.Background(), "https://as.example.com/register/x", ""); got != RegistrationUnknown || err == nil {
			t.Errorf("got %s / %v", got, err)
		}
		if got, err := client.ReadClientRegistration(context.Background(), "", "rat"); got != RegistrationUnknown || err == nil {
			t.Errorf("got %s / %v", got, err)
		}
	})
}

func TestClient_ExchangeCode_TokenEndpointErrorIsTyped(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"client not registered"}`))
	}))
	defer as.Close()

	client := NewClient()
	_, err := client.ExchangeCode(context.Background(), as.URL+"/token", "code", probeRedirectURI, "dcr-1", "", "verifier", "")
	if err == nil {
		t.Fatal("expected the token endpoint error")
	}
	if !IsInvalidClientError(err) {
		t.Errorf("expected IsInvalidClientError, got %v", err)
	}
	var tokenErr *TokenEndpointError
	if !errors.As(err, &tokenErr) || tokenErr.StatusCode != http.StatusUnauthorized || tokenErr.Code != ErrInvalidClient {
		t.Errorf("unexpected typed error: %+v", tokenErr)
	}
	if err.Error() != "token request failed: invalid_client - client not registered" {
		t.Errorf("error text changed: %q", err.Error())
	}
}

func TestClient_ExchangeCode_TokenEndpointErrorWithoutOAuthBody(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gateway timeout", http.StatusBadGateway)
	}))
	defer as.Close()

	client := NewClient()
	_, err := client.ExchangeCode(context.Background(), as.URL+"/token", "code", probeRedirectURI, "dcr-1", "", "verifier", "")
	if err == nil {
		t.Fatal("expected the token endpoint error")
	}
	if IsInvalidClientError(err) {
		t.Errorf("a non-OAuth failure must not read as invalid_client: %v", err)
	}
	if err.Error() != "token request failed with status 502" {
		t.Errorf("error text changed: %q", err.Error())
	}
}

func TestIsInvalidClientError_OtherErrors(t *testing.T) {
	if IsInvalidClientError(nil) {
		t.Error("nil is not invalid_client")
	}
	if IsInvalidClientError(errors.New("token request failed: invalid_client")) {
		t.Error("only the typed error counts, not matching text")
	}
	if IsInvalidClientError(&TokenEndpointError{StatusCode: 400, Code: ErrInvalidGrant}) {
		t.Error("invalid_grant is not invalid_client")
	}
}
