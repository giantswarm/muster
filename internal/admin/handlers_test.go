package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServer builds an admin.Server with fake Deps and returns an httptest
// server that exercises the real router.
func newTestServer(t *testing.T, deps Deps) *httptest.Server {
	t.Helper()
	srv, err := NewServer(Config{BindAddress: "127.0.0.1", Port: 1}, deps)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return httptest.NewServer(srv.routes())
}

func TestHandleList_html(t *testing.T) {
	called := false
	ts := newTestServer(t, fakeDeps(func(d *fakeDepsState) {
		d.sessions = []SessionSummary{
			{SessionID: "a1f3c9xxxxx", Subject: "pau@giantswarm.io", ServerCount: 2, ToolCount: 42, LastSeen: time.Now()},
			{SessionID: "b7e24100000", Subject: "alice@x.io", ServerCount: 1, ToolCount: 7},
		}
		d.onListSessions = func() { called = true }
	}))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/sessions")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if !called {
		t.Fatal("ListSessions callback not invoked")
	}
	if !strings.Contains(string(body), "pau@giantswarm.io") || !strings.Contains(string(body), "alice@x.io") {
		t.Fatalf("expected both subjects in HTML, got: %s", body)
	}
	// Short session IDs should be used in the HTML links.
	if !strings.Contains(string(body), "a1f3c9xx") {
		t.Fatalf("expected short session ID in output: %s", body)
	}
}

func TestHandleList_json(t *testing.T) {
	ts := newTestServer(t, fakeDeps(func(d *fakeDepsState) {
		d.sessions = []SessionSummary{{SessionID: "s1", Subject: "u1"}}
	}))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/sessions", nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected json content-type, got %s", ct)
	}
	var got []SessionSummary
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "s1" {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestHandleDetail_rendersDecodedJWT(t *testing.T) {
	raw := buildJWT(t,
		`{"alg":"RS256","kid":"kid1"}`,
		`{"sub":"pau","aud":"kubernetes"}`,
		[]byte("SECRET-SIGNATURE-SHOULD-NEVER-LEAK"),
	)
	ts := newTestServer(t, fakeDeps(func(d *fakeDepsState) {
		d.detail = &SessionDetail{
			SessionID: "sid123",
			Subject:   "pau",
			Servers: []ServerEntry{
				{Name: "kubernetes", Issuer: "https://dex", ToolCount: 32, Pooled: true},
			},
			Tokens: []SessionToken{
				{Label: "muster → kubernetes", Raw: raw},
			},
		}
	}))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/sessions/sid123")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	if !strings.Contains(s, "kubernetes") {
		t.Fatalf("server name missing from detail: %s", s)
	}
	if !strings.Contains(s, "muster → kubernetes") {
		t.Fatalf("token label missing: %s", s)
	}
	if !strings.Contains(s, "&#34;sub&#34;: &#34;pau&#34;") && !strings.Contains(s, `"sub": "pau"`) {
		t.Fatalf("decoded payload claim not rendered: %s", s)
	}
	// Critically: the raw signature must never appear in the rendered page.
	if strings.Contains(s, "SECRET-SIGNATURE-SHOULD-NEVER-LEAK") {
		t.Fatal("raw signature leaked into rendered HTML")
	}
	// Nor the raw compact token.
	if strings.Contains(s, raw) {
		t.Fatal("raw compact JWT leaked into rendered HTML")
	}
	// And the b64url-encoded signature must not appear either.
	sigSegment := base64.RawURLEncoding.EncodeToString([]byte("SECRET-SIGNATURE-SHOULD-NEVER-LEAK"))
	if strings.Contains(s, sigSegment) {
		t.Fatal("base64-encoded signature leaked into rendered HTML")
	}
}

func TestHandleDetail_notFound(t *testing.T) {
	ts := newTestServer(t, fakeDeps(func(d *fakeDepsState) {}))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/sessions/missing")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHandleDelete_callsCallbackAndRedirects(t *testing.T) {
	var gotID string
	ts := newTestServer(t, fakeDeps(func(d *fakeDepsState) {
		d.onDelete = func(id string) { gotID = id }
	}))
	defer ts.Close()

	// Don't follow redirects — we want to observe the 303.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Post(ts.URL+"/sessions/sid999/delete", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Location") != "/sessions" {
		t.Fatalf("expected redirect to /sessions, got %s", resp.Header.Get("Location"))
	}
	if gotID != "sid999" {
		t.Fatalf("DeleteSession called with %q, want sid999", gotID)
	}
}

func TestHandleReconnect_callsCallbackAndRedirects(t *testing.T) {
	var gotID, gotName string
	ts := newTestServer(t, fakeDeps(func(d *fakeDepsState) {
		d.onReconnect = func(id, name string) { gotID = id; gotName = name }
	}))
	defer ts.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Post(ts.URL+"/sessions/sid1/servers/github/reconnect", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Location") != "/sessions/sid1" {
		t.Fatalf("expected redirect to detail, got %s", resp.Header.Get("Location"))
	}
	if gotID != "sid1" || gotName != "github" {
		t.Fatalf("ReconnectServer called with (%q,%q)", gotID, gotName)
	}
}

func TestHandleMCPList_html(t *testing.T) {
	called := false
	ts := newTestServer(t, fakeDeps(func(d *fakeDepsState) {
		d.mcps = []MCPSummary{
			{Name: "github", Status: "connected", RequiresAuth: true},
			{Name: "kubernetes", Status: "connected"},
		}
		d.onListMCPs = func() { called = true }
	}))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/mcps")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !called {
		t.Fatal("ListMCPServers callback not invoked")
	}
	if !strings.Contains(s, `href="/mcps/github"`) || !strings.Contains(s, `href="/mcps/kubernetes"`) {
		t.Fatalf("expected MCP links in HTML: %s", s)
	}
}

func TestHandleMCPDetail_rendersMetadata(t *testing.T) {
	ts := newTestServer(t, fakeDeps(func(d *fakeDepsState) {
		d.mcpDetail = &MCPDetail{
			MCPSummary: MCPSummary{
				Name: "github", Status: "connected", URL: "https://mcp.github.com",
				RequiresAuth: true, Issuer: "https://github.com",
			},
			ToolPrefix: "gh",
		}
	}))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/mcps/github")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, "https://mcp.github.com") {
		t.Fatalf("expected URL rendered: %s", s)
	}
	if !strings.Contains(s, "https://github.com") {
		t.Fatalf("expected issuer rendered: %s", s)
	}
}

func TestHandleMCPDetail_notFound(t *testing.T) {
	ts := newTestServer(t, fakeDeps(func(d *fakeDepsState) {}))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/mcps/missing")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestNewServer_missingDeps(t *testing.T) {
	_, err := NewServer(Config{}, Deps{})
	if err == nil {
		t.Fatal("expected error for missing callbacks")
	}
}

// --- fake Deps helpers ---

type fakeDepsState struct {
	sessions  []SessionSummary
	detail    *SessionDetail
	mcps      []MCPSummary
	mcpDetail *MCPDetail

	onListSessions func()
	onDelete       func(id string)
	onReconnect    func(id, name string)
	onListMCPs     func()
}

func fakeDeps(setup func(*fakeDepsState)) Deps {
	state := &fakeDepsState{}
	setup(state)
	return Deps{
		ListSessions: func(ctx context.Context) ([]SessionSummary, error) {
			if state.onListSessions != nil {
				state.onListSessions()
			}
			return state.sessions, nil
		},
		GetSessionDetail: func(ctx context.Context, id string) (*SessionDetail, bool, error) {
			if state.detail != nil && state.detail.SessionID == id {
				return state.detail, true, nil
			}
			return nil, false, nil
		},
		DeleteSession: func(ctx context.Context, id string) error {
			if state.onDelete != nil {
				state.onDelete(id)
			}
			return nil
		},
		ReconnectServer: func(ctx context.Context, id, name string) error {
			if state.onReconnect != nil {
				state.onReconnect(id, name)
			}
			return nil
		},
		ListMCPServers: func(ctx context.Context) ([]MCPSummary, error) {
			if state.onListMCPs != nil {
				state.onListMCPs()
			}
			return state.mcps, nil
		},
		GetMCPDetail: func(ctx context.Context, name string) (*MCPDetail, bool, error) {
			if state.mcpDetail != nil && state.mcpDetail.Name == name {
				return state.mcpDetail, true, nil
			}
			return nil, false, nil
		},
	}
}
