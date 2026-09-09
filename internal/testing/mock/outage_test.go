package mock

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// TestOutageGatePassesPingsThrough: the orchestrator's health probe pings a
// connected server on every interval. An armed gate serves a ping uncounted,
// with its body intact for the handler, so a scenario that arms N requests
// sees exactly N connection attempts fail whenever the probe lands.
func TestOutageGatePassesPingsThrough(t *testing.T) {
	var gate outageGate
	var seen []string
	handler := gate.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the body behind the gate: %v", err)
		}
		seen = append(seen, string(body))
		w.WriteHeader(http.StatusOK)
	}))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	post := func(body string) int {
		resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	const ping = `{"jsonrpc":"2.0","id":7,"method":"ping"}`
	gate.set(http.StatusGatewayTimeout, 1)
	if got := post(ping); got != http.StatusOK {
		t.Fatalf("a ping during the outage must be served, got %d", got)
	}
	if got := gate.remainingRequests(); got != 1 {
		t.Fatalf("a ping must not consume the outage: remaining %d, want 1", got)
	}
	if got := post(`{"jsonrpc":"2.0","id":8,"method":"initialize","params":{}}`); got != http.StatusGatewayTimeout {
		t.Fatalf("a connection attempt during the outage: got %d, want 504", got)
	}
	if len(seen) != 1 || seen[0] != ping {
		t.Fatalf("the handler must see the ping with its body intact, saw %q", seen)
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
