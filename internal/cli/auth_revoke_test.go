package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// advertisedRevokePath is deliberately not mcp-oauth's default path, so a
// request that reaches it proves the endpoint was read from the metadata.
const advertisedRevokePath = "/as/revoke"

type revocationRequest struct {
	path, token, hint, clientID string
}

// fakeAuthServer is an authorization server that serves RFC 8414 metadata at
// its origin and records every revocation request and every request
// elsewhere, in order.
type fakeAuthServer struct {
	*httptest.Server

	// advertise makes the metadata name advertisedRevokePath as
	// revocation_endpoint; otherwise the metadata names none.
	advertise bool
	// revokeStatus is the status revocation requests are answered with.
	revokeStatus int

	mu          sync.Mutex
	revocations []revocationRequest
	requests    []string
}

func newFakeAuthServer(t *testing.T, advertise bool, revokeStatus int) *fakeAuthServer {
	t.Helper()
	f := &fakeAuthServer{advertise: advertise, revokeStatus: revokeStatus}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAuthServer) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)

	switch {
	case r.Method == http.MethodGet && r.URL.Path == pkgoauth.WellKnownAuthorizationServer:
		metadata := pkgoauth.Metadata{Issuer: f.URL, TokenEndpoint: f.URL + "/oauth/token"}
		if f.advertise {
			metadata.RevocationEndpoint = f.URL + advertisedRevokePath
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metadata)
	case r.Method == http.MethodPost && (r.URL.Path == advertisedRevokePath || r.URL.Path == defaultRevocationPath):
		_ = r.ParseForm()
		f.revocations = append(f.revocations, revocationRequest{
			path:     r.URL.Path,
			token:    r.Form.Get("token"),
			hint:     r.Form.Get("token_type_hint"),
			clientID: r.Form.Get("client_id"),
		})
		w.WriteHeader(f.revokeStatus)
	case r.Method == http.MethodDelete && r.URL.Path == "/user-tokens":
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeAuthServer) recordedRevocations() []revocationRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]revocationRequest(nil), f.revocations...)
}

func (f *fakeAuthServer) recordedRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// storeSession writes the token file a login to the server's MCP endpoint
// leaves behind and returns that endpoint.
func storeSession(t *testing.T, dir string, f *fakeAuthServer, accessToken, refreshToken string) string {
	t.Helper()
	endpoint := f.URL + "/mcp"
	serverURL := normalizeEndpoint(endpoint)
	writeTestTokenFile(t, dir, map[string]interface{}{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"server_url":    serverURL,
		"issuer_url":    f.URL,
		"client_id":     "muster-cli",
	}, serverURL)
	return endpoint
}

func newTestAdapter(t *testing.T) (*AuthAdapter, string) {
	t.Helper()
	dir := t.TempDir()
	adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{TokenStorageDir: dir})
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	return adapter, dir
}

func assertNoTokenFiles(t *testing.T, dir string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatalf("glob token dir: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected local state cleared, found %d token file(s)", len(files))
	}
}

func TestResolveRevocationEndpoint(t *testing.T) {
	t.Run("takes the endpoint the metadata advertises", func(t *testing.T) {
		f := newFakeAuthServer(t, true, http.StatusOK)

		got, err := resolveRevocationEndpoint(context.Background(), f.URL+"/mcp", f.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := f.URL + advertisedRevokePath; got != want {
			t.Errorf("expected %s, got %s", want, got)
		}
	})

	t.Run("discovers at the origin of an MCP endpoint when no issuer is stored", func(t *testing.T) {
		f := newFakeAuthServer(t, true, http.StatusOK)

		got, err := resolveRevocationEndpoint(context.Background(), f.URL+"/mcp", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := f.URL + advertisedRevokePath; got != want {
			t.Errorf("expected %s, got %s", want, got)
		}
	})

	t.Run("falls back to the issuer's /oauth/revoke when none is advertised", func(t *testing.T) {
		f := newFakeAuthServer(t, false, http.StatusOK)

		got, err := resolveRevocationEndpoint(context.Background(), f.URL+"/mcp", f.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := f.URL + "/oauth/revoke"; got != want {
			t.Errorf("expected %s, got %s", want, got)
		}
	})

	t.Run("fails when the metadata cannot be discovered", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(srv.Close)

		if _, err := resolveRevocationEndpoint(context.Background(), srv.URL+"/mcp", srv.URL); err == nil {
			t.Error("expected an error")
		}
	})

	t.Run("fails on an endpoint without scheme and host", func(t *testing.T) {
		if _, err := resolveRevocationEndpoint(context.Background(), "not-a-url", ""); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestLogout_RevokesBothTokens(t *testing.T) {
	f := newFakeAuthServer(t, true, http.StatusOK)
	adapter, dir := newTestAdapter(t)
	endpoint := storeSession(t, dir, f, "access-tok", "refresh-tok")

	if err := adapter.Logout(endpoint); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []revocationRequest{
		{path: advertisedRevokePath, token: "refresh-tok", hint: "refresh_token", clientID: "muster-cli"},
		{path: advertisedRevokePath, token: "access-tok", hint: "access_token", clientID: "muster-cli"},
	}
	got := f.recordedRevocations()
	if len(got) != len(want) {
		t.Fatalf("expected %d revocations, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("revocation %d: expected %+v, got %+v", i, want[i], got[i])
		}
	}
	for _, req := range f.recordedRequests() {
		if req == "POST /mcp/oauth/revoke" {
			t.Errorf("revocation was sent below the MCP endpoint: %s", req)
		}
	}
	assertNoTokenFiles(t, dir)
}

func TestLogout_RevokesAccessTokenWithoutRefreshToken(t *testing.T) {
	f := newFakeAuthServer(t, true, http.StatusOK)
	adapter, dir := newTestAdapter(t)
	endpoint := storeSession(t, dir, f, "access-tok", "")

	if err := adapter.Logout(endpoint); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := f.recordedRevocations()
	if len(got) != 1 || got[0].token != "access-tok" || got[0].hint != "access_token" {
		t.Errorf("expected one access_token revocation, got %+v", got)
	}
}

func TestLogout_NoStoredTokenRevokesNothing(t *testing.T) {
	f := newFakeAuthServer(t, true, http.StatusOK)
	adapter, _ := newTestAdapter(t)

	if err := adapter.Logout(f.URL + "/mcp"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := f.recordedRequests(); len(got) != 0 {
		t.Errorf("expected no requests, got %v", got)
	}
}

func TestLogout_ReportsFailedRevocation(t *testing.T) {
	t.Run("server refuses the revocation", func(t *testing.T) {
		f := newFakeAuthServer(t, true, http.StatusServiceUnavailable)
		adapter, dir := newTestAdapter(t)
		endpoint := storeSession(t, dir, f, "access-tok", "refresh-tok")

		err := adapter.Logout(endpoint)

		var revokeErr *RevocationFailedError
		if !errors.As(err, &revokeErr) {
			t.Fatalf("expected *RevocationFailedError, got %v", err)
		}
		if len(revokeErr.Endpoints) != 1 || revokeErr.Endpoints[0] != normalizeEndpoint(endpoint) {
			t.Errorf("expected the endpoint named, got %v", revokeErr.Endpoints)
		}
		// Both tokens were tried even though the first failed.
		if got := f.recordedRevocations(); len(got) != 2 {
			t.Errorf("expected both tokens tried, got %+v", got)
		}
		assertNoTokenFiles(t, dir)
	})

	t.Run("server unreachable", func(t *testing.T) {
		f := newFakeAuthServer(t, true, http.StatusOK)
		adapter, dir := newTestAdapter(t)
		endpoint := storeSession(t, dir, f, "access-tok", "refresh-tok")
		f.Close()

		err := adapter.Logout(endpoint)

		var revokeErr *RevocationFailedError
		if !errors.As(err, &revokeErr) {
			t.Fatalf("expected *RevocationFailedError, got %v", err)
		}
		assertNoTokenFiles(t, dir)
	})
}

func TestLogoutAll_RevokesEveryEndpoint(t *testing.T) {
	f1 := newFakeAuthServer(t, true, http.StatusOK)
	f2 := newFakeAuthServer(t, false, http.StatusOK)
	adapter, dir := newTestAdapter(t)
	storeSession(t, dir, f1, "access-1", "refresh-1")
	storeSession(t, dir, f2, "access-2", "refresh-2")

	if err := adapter.LogoutAll(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, f := range []*fakeAuthServer{f1, f2} {
		if got := f.recordedRevocations(); len(got) != 2 {
			t.Errorf("%s: expected refresh and access token revoked, got %+v", f.URL, got)
		}
	}
	assertNoTokenFiles(t, dir)
}

func TestLogoutAll_DeletesUserTokensBeforeRevokingAccessToken(t *testing.T) {
	f := newFakeAuthServer(t, true, http.StatusOK)
	adapter, dir := newTestAdapter(t)
	storeSession(t, dir, f, "access-tok", "refresh-tok")

	if err := adapter.LogoutAll(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var deleteAt, revokeAt = -1, -1
	for i, req := range f.recordedRequests() {
		switch req {
		case "DELETE /user-tokens":
			deleteAt = i
		case "POST " + advertisedRevokePath:
			if revokeAt == -1 {
				revokeAt = i
			}
		}
	}
	if deleteAt == -1 || revokeAt == -1 || deleteAt > revokeAt {
		t.Errorf("expected DELETE /user-tokens before the first revocation, got %v", f.recordedRequests())
	}
}

func TestLogoutAll_ReportsFailedRevocation(t *testing.T) {
	ok := newFakeAuthServer(t, true, http.StatusOK)
	failing := newFakeAuthServer(t, true, http.StatusInternalServerError)
	adapter, dir := newTestAdapter(t)
	storeSession(t, dir, ok, "access-1", "refresh-1")
	storeSession(t, dir, failing, "access-2", "refresh-2")

	err := adapter.LogoutAll()

	var revokeErr *RevocationFailedError
	if !errors.As(err, &revokeErr) {
		t.Fatalf("expected *RevocationFailedError, got %v", err)
	}
	if len(revokeErr.Endpoints) != 1 || revokeErr.Endpoints[0] != normalizeEndpoint(failing.URL+"/mcp") {
		t.Errorf("expected only the failing endpoint named, got %v", revokeErr.Endpoints)
	}
	assertNoTokenFiles(t, dir)
}
