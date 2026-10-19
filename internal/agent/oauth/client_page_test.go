package oauth

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/giantswarm/muster/v5/internal/signinpage"
)

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
			name:        "no parameters",
			tokenStatus: http.StatusOK,
			query:       func(*AuthFlow) string { return "" },
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
			server := newAuthorizationServer(t, false, tc.tokenStatus)
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

// TestEndFlow_LeavesNewerFlowRunning covers a sign-in that ends after a newer
// one started: it must not stop the newer one's callback server.
func TestEndFlow_LeavesNewerFlowRunning(t *testing.T) {
	server := newAuthorizationServer(t, false, http.StatusOK)
	client, err := NewClient(ClientConfig{
		CallbackPort:     freeLoopbackPort(t),
		TokenStoreConfig: TokenStoreConfig{StorageDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.StartAuthFlowWithOptions(t.Context(), "https://mcp.example.com/v1/mcp", server.URL, nil); err != nil {
		t.Fatalf("first StartAuthFlowWithOptions: %v", err)
	}
	older := client.currentFlow
	if _, err := client.StartAuthFlowWithOptions(t.Context(), "https://mcp.example.com/v1/mcp", server.URL, nil); err != nil {
		t.Fatalf("second StartAuthFlowWithOptions: %v", err)
	}
	newer := client.currentFlow

	client.endFlow(older, http.StatusBadRequest, signinpage.Failed("The sign-in was cancelled", ""))

	if client.currentFlow != newer {
		t.Fatal("expected the newer flow to stay current")
	}
	pageCh := make(chan string, 1)
	go func() {
		resp, err := http.Get(newer.CallbackServer.GetRedirectURI() + "?code=c&state=" + url.QueryEscape(newer.State)) //nolint:gosec // G107: the flow's own local callback server
		if err != nil {
			pageCh <- err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		pageCh <- string(body)
	}()
	if _, err := client.WaitForCallback(t.Context()); err != nil {
		t.Fatalf("WaitForCallback for the newer flow: %v", err)
	}
	if page := <-pageCh; !strings.Contains(page, "<h1>Signed in</h1>") {
		t.Errorf("expected the newer flow's page to report success, got %q", page)
	}
}
