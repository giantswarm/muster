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

// callbackTestResource is the RFC 8707 resource indicator the test flows carry.
const callbackTestResource = "https://mcp.example.com/mcp"

type authorizationServerOptions struct {
	// issSupported sets authorization_response_iss_parameter_supported in
	// the published metadata.
	issSupported bool

	// tokenForm receives the parsed form of every token request.
	tokenForm chan url.Values
}

// newAuthorizationServer starts an authorization server that publishes RFC
// 8414 metadata naming itself as the issuer and issues a token for any code.
func newAuthorizationServer(t *testing.T, opts authorizationServerOptions) *httptest.Server {
	t.Helper()

	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pkgoauth.WellKnownAuthorizationServer:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pkgoauth.Metadata{
				Issuer:                        "http://" + server.Listener.Addr().String(),
				AuthorizationEndpoint:         "http://" + server.Listener.Addr().String() + "/authorize",
				TokenEndpoint:                 "http://" + server.Listener.Addr().String() + "/token",
				CodeChallengeMethodsSupported: []string{"S256"},
				AuthorizationResponseIssParameterSupported: opts.issSupported,
			})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("failed to parse token request form: %v", err)
			}
			if opts.tokenForm != nil {
				opts.tokenForm <- r.Form
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access-token","token_type":"Bearer","expires_in":3600}`))
		default:
			http.NotFound(w, r)
		}
	})
	server.Start()
	return server
}

// storeCallbackState records a flow for the given issuer and returns the
// encoded state a callback must present.
func storeCallbackState(t *testing.T, client *Client, issuer string) string {
	t.Helper()

	encodedState, err := client.stateStore.GenerateState(StateParams{
		SessionID:    "session-1",
		UserID:       "user-1",
		ServerName:   "test-server",
		Issuer:       issuer,
		Resource:     callbackTestResource,
		CodeVerifier: "verifier",
	}, nil)
	if err != nil {
		t.Fatalf("failed to generate state: %v", err)
	}
	return encodedState
}

func TestHandler_HandleCallback_IssValidation(t *testing.T) {
	tests := []struct {
		name         string
		issSupported bool
		iss          func(issuer string) string
		wantSuccess  bool
		wantInBody   string
	}{
		{
			name:        "iss matches the authorization server",
			iss:         func(issuer string) string { return issuer },
			wantSuccess: true,
		},
		{
			name:       "iss names a different authorization server",
			iss:        func(string) string { return "https://evil.example.com" },
			wantInBody: "could not be verified",
		},
		{
			name:         "iss absent although the server advertises it",
			issSupported: true,
			iss:          func(string) string { return "" },
			wantInBody:   "could not be verified",
		},
		{
			name:        "iss absent and the server does not advertise it",
			iss:         func(string) string { return "" },
			wantSuccess: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			authServer := newAuthorizationServer(t, authorizationServerOptions{issSupported: tc.issSupported})
			defer authServer.Close()

			client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
			defer client.Stop()

			handler := NewHandler(client)
			encodedState := storeCallbackState(t, client, authServer.URL)

			query := url.Values{
				"code":  {"auth-code"},
				"state": {encodedState},
			}
			if iss := tc.iss(authServer.URL); iss != "" {
				query.Set("iss", iss)
			}

			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/oauth/callback?"+query.Encode(), nil)
			recorder := httptest.NewRecorder()
			handler.HandleCallback(recorder, request)

			body := recorder.Body.String()
			if tc.wantSuccess {
				if recorder.Code != http.StatusOK {
					t.Fatalf("expected status %d, got %d (body %q)", http.StatusOK, recorder.Code, body)
				}
				return
			}
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
			}
			if !strings.Contains(body, tc.wantInBody) {
				t.Errorf("expected body to contain %q, got %q", tc.wantInBody, body)
			}
		})
	}
}

// TestHandler_HandleCallback_SendsResourceOnTokenRequest covers RFC 8707 on
// the token request: the callback replays the resource recorded with the flow.
func TestHandler_HandleCallback_SendsResourceOnTokenRequest(t *testing.T) {
	tokenForm := make(chan url.Values, 1)
	authServer := newAuthorizationServer(t, authorizationServerOptions{tokenForm: tokenForm})
	defer authServer.Close()

	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	handler := NewHandler(client)
	encodedState := storeCallbackState(t, client, authServer.URL)

	query := url.Values{
		"code":  {"auth-code"},
		"state": {encodedState},
		"iss":   {authServer.URL},
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/oauth/callback?"+query.Encode(), nil)
	recorder := httptest.NewRecorder()
	handler.HandleCallback(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d (body %q)", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	select {
	case form := <-tokenForm:
		if got := form.Get("resource"); got != callbackTestResource {
			t.Errorf("expected resource %q on the token request, got %q", callbackTestResource, got)
		}
	default:
		t.Fatal("token endpoint was not called")
	}
}

// TestClient_GenerateAuthURL_SendsResource covers RFC 8707 on the
// authorization request.
func TestClient_GenerateAuthURL_SendsResource(t *testing.T) {
	authServer := newAuthorizationServer(t, authorizationServerOptions{})
	defer authServer.Close()

	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	startURL, err := client.GenerateAuthURL(t.Context(), "session-1", "user-1", "test-server",
		authServer.URL, callbackTestResource, "openid")
	if err != nil {
		t.Fatalf("failed to generate auth URL: %v", err)
	}

	parsedStart, err := url.Parse(startURL)
	if err != nil {
		t.Fatalf("failed to parse start URL: %v", err)
	}
	state := client.stateStore.Update(parsedStart.Query().Get("state"), func(*OAuthState) {})
	if state == nil {
		t.Fatal("state should be stored")
	}

	upstream, err := url.Parse(state.AuthorizationURL)
	if err != nil {
		t.Fatalf("failed to parse authorization URL: %v", err)
	}
	if got := upstream.Query().Get("resource"); got != callbackTestResource {
		t.Errorf("expected resource %q on the authorization URL, got %q", callbackTestResource, got)
	}
	if state.Resource != callbackTestResource {
		t.Errorf("expected resource %q recorded with the state, got %q", callbackTestResource, state.Resource)
	}
}
