package testing

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
)

// TestParseProcNetTCP_DecodesAddressesAndStates pins the /proc/net/tcp row
// format the diagnostic depends on: host-byte-order hex addresses, hex port,
// hex state, inode in column 10.
func TestParseProcNetTCP_DecodesAddressesAndStates(t *testing.T) {
	table := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:7541 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0\n" +
		"   1: 0100007F:7541 0100007F:9C40 06 00000000:00000000 00:00000000 00000000     0        0 0 1 0000000000000000 100 0 0 10 0\n" +
		"   2: 0100007F:7542 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 777 1 0000000000000000 100 0 0 10 0\n"
	entries := parseProcNetTCP(strings.NewReader(table), map[int]bool{30017: true})
	if len(entries) != 2 {
		t.Fatalf("want 2 rows for port 30017, got %d: %+v", len(entries), entries)
	}
	if entries[0].local != "127.0.0.1:30017" || entries[0].state != "LISTEN" || entries[0].inode != "12345" {
		t.Fatalf("unexpected first row: %+v", entries[0])
	}
	if entries[1].remote != "127.0.0.1:40000" || entries[1].state != "TIME_WAIT" {
		t.Fatalf("unexpected second row: %+v", entries[1])
	}

	v6 := "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000000000000000000001000000:7541 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 42 1 0000000000000000 100 0 0 10 0\n"
	entries = parseProcNetTCP(strings.NewReader(v6), map[int]bool{30017: true})
	if len(entries) != 1 || entries[0].local != "[::1]:30017" {
		t.Fatalf("unexpected IPv6 decode: %+v", entries)
	}
}

// TestDescribePortOccupants_NamesThisProcess holds a listener and checks the
// diagnostic reports it as LISTEN owned by this process.
func TestDescribePortOccupants_NamesThisProcess(t *testing.T) {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skip("/proc/net/tcp not available")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	got := describePortOccupants(port)
	if !strings.Contains(got, fmt.Sprintf("127.0.0.1:%d", port)) || !strings.Contains(got, "LISTEN") {
		t.Fatalf("diagnostic does not show the held listener:\n%s", got)
	}
	if !strings.Contains(got, fmt.Sprintf("pid %d ", os.Getpid())) {
		t.Fatalf("diagnostic does not attribute the listener to this process:\n%s", got)
	}
}

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
