package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newProbeTestManager(t *testing.T) *AuthManager {
	t.Helper()
	mgr, err := NewAuthManager(AuthManagerConfig{TokenStorageDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

// TestAuthManager_StartAuthFlow_UntrustedCertificate pins that a server whose
// certificate the client does not trust is reported with the x509 error and
// the endpoint, not as a server without a challenge.
func TestAuthManager_StartAuthFlow_UntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="muster", issuer="https://issuer.example"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	mgr := newProbeTestManager(t)
	endpoint := server.URL + "/mcp"

	state, err := mgr.CheckConnection(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("CheckConnection: %v", err)
	}
	if state != AuthStatePendingAuth {
		t.Fatalf("expected AuthStatePendingAuth, got %s", state)
	}

	_, err = mgr.StartAuthFlow(context.Background())
	if err == nil {
		t.Fatal("expected an error for an untrusted certificate")
	}
	msg := err.Error()
	if !strings.Contains(msg, "x509") {
		t.Errorf("expected the x509 error, got: %v", err)
	}
	if !strings.Contains(msg, endpoint) {
		t.Errorf("expected the error to name %s, got: %v", endpoint, err)
	}
	if strings.Contains(msg, "no auth challenge available") {
		t.Errorf("expected no missing-challenge report, got: %v", err)
	}
}

// TestAuthManager_StartAuthFlow_NoChallenge pins that a reachable server
// that sends no challenge is still reported as such.
func TestAuthManager_StartAuthFlow_NoChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mgr := newProbeTestManager(t)
	if _, err := mgr.CheckConnection(context.Background(), server.URL+"/mcp"); err != nil {
		t.Fatalf("CheckConnection: %v", err)
	}

	_, err := mgr.StartAuthFlow(context.Background())
	if err == nil || err.Error() != "no auth challenge available" {
		t.Errorf("expected \"no auth challenge available\", got: %v", err)
	}
}

// TestAuthManager_RequireAuth_UntrustedCertificate pins that a re-login
// against an untrusted certificate reports the x509 error.
func TestAuthManager_RequireAuth_UntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	mgr := newProbeTestManager(t)
	err := mgr.RequireAuth(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "x509") {
		t.Errorf("expected the x509 error, got: %v", err)
	}
	if mgr.GetState() == AuthStatePendingAuth {
		t.Error("expected no flow to be prepared for an unreachable server")
	}
}

// TestAuthManager_ProbeErrorClearedOnReachableServer pins that a failed probe
// does not leak into the next one.
func TestAuthManager_ProbeErrorClearedOnReachableServer(t *testing.T) {
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer untrusted.Close()
	reachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer reachable.Close()

	mgr := newProbeTestManager(t)
	ctx := context.Background()
	if _, err := mgr.CheckConnection(ctx, untrusted.URL); err != nil {
		t.Fatalf("CheckConnection: %v", err)
	}
	if _, err := mgr.CheckConnection(ctx, reachable.URL); err != nil {
		t.Fatalf("CheckConnection: %v", err)
	}

	_, err := mgr.StartAuthFlow(ctx)
	if err == nil || err.Error() != "no auth challenge available" {
		t.Errorf("expected \"no auth challenge available\", got: %v", err)
	}
}
