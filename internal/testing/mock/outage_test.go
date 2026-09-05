package mock

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestOutageGateAnswersThenRecovers: an armed gate answers exactly the
// configured number of requests with the status and then lets the handler
// serve again; a disarmed gate is transparent.
func TestOutageGateAnswersThenRecovers(t *testing.T) {
	served := 0
	var gate outageGate
	handler := gate.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		w.WriteHeader(http.StatusOK)
	}))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	get := func() int {
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if got := get(); got != http.StatusOK {
		t.Fatalf("disarmed gate must be transparent, got %d", got)
	}

	gate.set(http.StatusGatewayTimeout, 2)
	for i := 0; i < 2; i++ {
		if got := get(); got != http.StatusGatewayTimeout {
			t.Fatalf("request %d during the outage: got %d, want 504", i+1, got)
		}
	}
	if got := gate.remainingRequests(); got != 0 {
		t.Fatalf("remaining after the outage: got %d, want 0", got)
	}
	if got := get(); got != http.StatusOK {
		t.Fatalf("after the outage the handler must serve again, got %d", got)
	}
	if served != 2 {
		t.Fatalf("the handler must not see requests answered by the gate: served %d, want 2", served)
	}

	// Ending an outage early.
	gate.set(http.StatusServiceUnavailable, 5)
	gate.set(0, 0)
	if got := get(); got != http.StatusOK {
		t.Fatalf("a cleared gate must be transparent, got %d", got)
	}
}

// TestHTTPServerOutageSurvivesRestart: the gate lives on the server, not on
// the handler, so an outage armed while the server is stopped applies once it
// listens again on the same port.
func TestHTTPServerOutageSurvivesRestart(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "outage.yaml")
	if err := os.WriteFile(configPath, []byte("tools: []\n"), 0o600); err != nil {
		t.Fatalf("write mock config: %v", err)
	}
	mockServer, err := NewServerFromFile(configPath, false)
	if err != nil {
		t.Fatalf("NewServerFromFile: %v", err)
	}
	srv := NewHTTPServer(mockServer, HTTPTransportStreamableHTTP, false)
	port, err := srv.Start(t.Context())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := srv.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	srv.SetOutage(http.StatusBadGateway, 1)
	if err := srv.StartOnPort(t.Context(), port); err != nil {
		t.Fatalf("StartOnPort: %v", err)
	}
	defer func() { _ = srv.Stop(t.Context()) }()
	if err := srv.WaitForReady(t.Context()); err != nil {
		t.Fatalf("WaitForReady: %v", err)
	}

	resp, err := http.Get(srv.Endpoint())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("outage armed across a restart must apply: got %d, want 502", resp.StatusCode)
	}
	if srv.OutageRemaining() != 0 {
		t.Fatalf("outage must be consumed, remaining %d", srv.OutageRemaining())
	}
}
