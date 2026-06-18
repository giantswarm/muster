package admin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func noopDeps() Deps {
	return fakeDeps(func(*fakeDepsState) {})
}

func TestNewServer_refusesNonLoopbackWithoutAuth(t *testing.T) {
	for _, addr := range []string{"0.0.0.0", "10.0.0.5", "example.com", "::"} {
		_, err := NewServer(Config{BindAddress: addr}, noopDeps())
		if err == nil {
			t.Fatalf("BindAddress %q: expected error without AuthMiddleware", addr)
		}
	}
}

func TestNewServer_allowsLoopbackWithoutAuth(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "localhost", "::1", ""} {
		if _, err := NewServer(Config{BindAddress: addr}, noopDeps()); err != nil {
			t.Fatalf("BindAddress %q: unexpected error: %v", addr, err)
		}
	}
}

func TestNewServer_allowsNonLoopbackWithAuth(t *testing.T) {
	cfg := Config{
		BindAddress:    "0.0.0.0",
		AuthMiddleware: func(next http.Handler) http.Handler { return next },
	}
	if _, err := NewServer(cfg, noopDeps()); err != nil {
		t.Fatalf("unexpected error with AuthMiddleware: %v", err)
	}
}

func TestRoutes_authMiddlewareGatesEveryRoute(t *testing.T) {
	var wrapped int
	srv, err := NewServer(Config{
		BindAddress: "127.0.0.1",
		AuthMiddleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wrapped++
				if r.Header.Get("Authorization") == "" {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
			})
		},
	}, noopDeps())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/sessions")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", resp.StatusCode)
	}
	if wrapped == 0 {
		t.Fatal("AuthMiddleware was not invoked")
	}
}
