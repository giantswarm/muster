package testing

import (
	"fmt"
	"net"
	"testing"
)

// newPortTestManager returns a concrete *musterInstanceManager wired with a
// silent logger and an isolated port range for deterministic port tests.
func newPortTestManager(t *testing.T, basePort int) *musterInstanceManager {
	t.Helper()
	mgr, err := NewMusterInstanceManagerWithLogger(false, basePort, NewSilentLogger(false, false))
	if err != nil {
		t.Fatalf("failed to create instance manager: %v", err)
	}
	m, ok := mgr.(*musterInstanceManager)
	if !ok {
		t.Fatalf("expected *musterInstanceManager, got %T", mgr)
	}
	return m
}

// TestFindAvailablePort_HoldsListenerUntilClosed is the regression guard for the
// TOCTOU port race: findAvailablePort must keep the probe listener bound so the
// OS cannot hand the reserved port to an ephemeral net.Listen(":0") before
// muster serve binds it. The port only becomes bindable again after the
// reservation listener is explicitly closed.
func TestFindAvailablePort_HoldsListenerUntilClosed(t *testing.T) {
	m := newPortTestManager(t, 41000)

	port, err := m.findAvailablePort("inst-hold", m.logger)
	if err != nil {
		t.Fatalf("findAvailablePort failed: %v", err)
	}

	// While reserved, the port must not be bindable: the held listener still
	// owns it at the OS level. A successful bind here would mean the port could
	// be stolen during instance setup.
	if ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port)); err == nil {
		_ = ln.Close()
		t.Fatalf("port %d was bindable while reserved; the probe listener was not held open", port)
	}

	// After releasing the reservation listener (as startMusterProcess does just
	// before exec), the port becomes available for muster serve to bind.
	m.closeReservedListener(port)

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		t.Fatalf("port %d not bindable after closeReservedListener: %v", port, err)
	}
	_ = ln.Close()
}

// TestFindAvailablePort_DistinctPortsAreHeldSimultaneously verifies that
// reserving several ports in a row keeps every one of them bound at once (no
// two instances are ever handed the same port, and earlier reservations are not
// silently freed when later ones are made).
func TestFindAvailablePort_DistinctPortsAreHeldSimultaneously(t *testing.T) {
	m := newPortTestManager(t, 41100)

	const n = 5
	ports := make([]int, 0, n)
	seen := make(map[int]bool)
	for i := 0; i < n; i++ {
		port, err := m.findAvailablePort(fmt.Sprintf("inst-%d", i), m.logger)
		if err != nil {
			t.Fatalf("findAvailablePort #%d failed: %v", i, err)
		}
		if seen[port] {
			t.Fatalf("port %d handed out twice", port)
		}
		seen[port] = true
		ports = append(ports, port)
	}

	// All reserved ports must still be held (unbindable) at the same time.
	for _, port := range ports {
		if ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port)); err == nil {
			_ = ln.Close()
			t.Fatalf("port %d was bindable while still reserved", port)
		}
	}

	// releasePort frees the held listener too, restoring bindability.
	for i, port := range ports {
		m.releasePort(port, fmt.Sprintf("inst-%d", i), m.logger)
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			t.Fatalf("port %d not bindable after releasePort: %v", port, err)
		}
		_ = ln.Close()
	}
}
