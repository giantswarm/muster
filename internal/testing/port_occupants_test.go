package testing

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
)

// TestProcessExitedError_NamesPortOccupant checks the startup-failure error
// carries the socket table for the instance's ports when the process died of
// "address already in use", and stays quiet for any other exit.
func TestProcessExitedError_NamesPortOccupant(t *testing.T) {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skip("/proc/net/tcp not available")
	}
	m := newPortTestManager(t, 19100)

	// The "thief": a listener on the port the instance was going to use for
	// its metrics exporter.
	thief, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = thief.Close() }()
	metricsPort := thief.Addr().(*net.TCPAddr).Port
	instance := &MusterInstance{ID: "inst-diag", Port: 19100, MetricsPort: metricsPort}

	capture := func(stderr string) *managedProcess {
		lc := newLogCapture()
		_, _ = lc.stderrWriter.Write([]byte(stderr))
		lc.close()
		return &managedProcess{logCapture: lc, waitErr: errors.New("exit status 1"), exited: make(chan struct{})}
	}

	err = m.processExitedError(instance, capture(fmt.Sprintf(
		"Error: init meter: otel metric reader: binding address 127.0.0.1:%d for Prometheus exporter: listen tcp 127.0.0.1:%d: bind: address already in use\n",
		metricsPort, metricsPort)), m.logger)
	msg := err.Error()
	for _, want := range []string{
		fmt.Sprintf("sockets on the instance's ports 19100 and %d at failure", metricsPort),
		fmt.Sprintf("127.0.0.1:%d -> ", metricsPort),
		"LISTEN",
		fmt.Sprintf("pid %d ", os.Getpid()),
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error lacks %q:\n%s", want, msg)
		}
	}

	err = m.processExitedError(instance, capture("Error: something unrelated\n"), m.logger)
	if strings.Contains(err.Error(), "sockets on the instance's ports") {
		t.Fatalf("diagnostics attached to an unrelated failure:\n%s", err)
	}
}
