package oauth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// newTokenServer starts an authorization server with RFC 8414 metadata and a
// token endpoint that answers with tokenStatus.
func newTokenServer(t *testing.T, tokenStatus int) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pkgoauth.WellKnownAuthorizationServer:
			_ = json.NewEncoder(w).Encode(pkgoauth.Metadata{
				Issuer:                        server.URL,
				AuthorizationEndpoint:         server.URL + "/authorize",
				TokenEndpoint:                 server.URL + "/token",
				CodeChallengeMethodsSupported: []string{"S256"},
			})
		case "/token":
			w.WriteHeader(tokenStatus)
			if tokenStatus == http.StatusOK {
				_, _ = io.WriteString(w, `{"access_token":"at","token_type":"Bearer","expires_in":3600}`)
				return
			}
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestWaitForCallback_PageShowsOutcome covers the browser page after the
// agent's sign-in: it reports what the client concluded from the callback,
// so a rejected or failed sign-in never shows "Signed in".
func TestWaitForCallback_PageShowsOutcome(t *testing.T) {
	tests := []struct {
		name        string
		tokenStatus int
		query       func(flow *AuthFlow) string
		wantErr     bool
		wantStatus  int
		want        string
	}{
		{
			name:        "success",
			tokenStatus: http.StatusOK,
			query:       func(flow *AuthFlow) string { return "code=c&state=" + url.QueryEscape(flow.State) },
			wantStatus:  http.StatusOK,
			want:        "<h1>Signed in</h1>",
		},
		{
			name:        "state mismatch",
			tokenStatus: http.StatusOK,
			query:       func(*AuthFlow) string { return "code=c&state=forged" },
			wantErr:     true,
			wantStatus:  http.StatusBadRequest,
			want:        "The sign-in response could not be verified",
		},
		{
			name:        "provider error",
			tokenStatus: http.StatusOK,
			query: func(flow *AuthFlow) string {
				return "error=access_denied&state=" + url.QueryEscape(flow.State)
			},
			wantErr:    true,
			wantStatus: http.StatusBadRequest,
			want:       "<code>access_denied</code>",
		},
		{
			name:        "no code",
			tokenStatus: http.StatusOK,
			query:       func(flow *AuthFlow) string { return "state=" + url.QueryEscape(flow.State) },
			wantErr:     true,
			wantStatus:  http.StatusBadRequest,
			want:        "The sign-in response is incomplete",
		},
		{
			name:        "code exchange fails",
			tokenStatus: http.StatusBadRequest,
			query:       func(flow *AuthFlow) string { return "code=c&state=" + url.QueryEscape(flow.State) },
			wantErr:     true,
			wantStatus:  http.StatusBadRequest,
			want:        "The sign-in could not be completed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := newTokenServer(t, tc.tokenStatus)
			client, err := NewClient(ClientConfig{
				CallbackPort:     freeLoopbackPort(t),
				TokenStoreConfig: TokenStoreConfig{StorageDir: t.TempDir()},
			})
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			defer func() { _ = client.Close() }()

			if _, err := client.StartAuthFlowWithOptions(t.Context(), "https://mcp.example.com/v1/mcp", server.URL, nil); err != nil {
				t.Fatalf("StartAuthFlowWithOptions: %v", err)
			}
			flow := client.currentFlow

			type page struct {
				status int
				body   string
				err    error
			}
			pageCh := make(chan page, 1)
			go func() {
				resp, err := http.Get(flow.CallbackServer.GetRedirectURI() + "?" + tc.query(flow)) //nolint:gosec // G107: the flow's own local callback server
				if err != nil {
					pageCh <- page{err: err}
					return
				}
				defer func() { _ = resp.Body.Close() }()
				body, err := io.ReadAll(resp.Body)
				pageCh <- page{status: resp.StatusCode, body: string(body), err: err}
			}()

			_, err = client.WaitForCallback(t.Context())
			if (err != nil) != tc.wantErr {
				t.Errorf("expected error %v, got %v", tc.wantErr, err)
			}

			got := <-pageCh
			if got.err != nil {
				t.Fatalf("request the callback page: %v", got.err)
			}
			if got.status != tc.wantStatus {
				t.Errorf("expected status %d, got %d", tc.wantStatus, got.status)
			}
			if !strings.Contains(got.body, tc.want) {
				t.Errorf("expected the page to contain %q, got %q", tc.want, got.body)
			}
			if tc.wantErr && strings.Contains(got.body, "Signed in") {
				t.Errorf("expected no success page for a failed sign-in")
			}
		})
	}
}
