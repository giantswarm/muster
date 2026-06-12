package testing

import (
	"fmt"
	"net"
	"testing"
)

// Deterministic ephemeral range for port tests, independent of the host OS.
const (
	testEphemeralLow  = 32768
	testEphemeralHigh = 60999
)

// newPortTestManager returns a concrete *musterInstanceManager wired with a
// silent logger and an isolated port range for deterministic port tests. The
// ephemeral range is pinned so the tests behave the same on every platform.
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
	m.ephemeralLow = testEphemeralLow
	m.ephemeralHigh = testEphemeralHigh
	return m
}

// TestFindAvailablePort_HoldsListenerUntilClosed is the regression guard for the
// TOCTOU port race: findAvailablePort must keep the probe listener bound so the
// OS cannot hand the reserved port to an ephemeral net.Listen(":0") before
// muster serve binds it. The port only becomes bindable again after the
// reservation listener is explicitly closed.
func TestFindAvailablePort_HoldsListenerUntilClosed(t *testing.T) {
	m := newPortTestManager(t, 18500)

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
	m := newPortTestManager(t, 18600)

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

// TestFindAvailablePort_SkipsEphemeralRange verifies that when the configured
// window straddles the OS ephemeral range, allocation never hands out a port
// inside it. Ports inside the range can be stolen by a mock server's
// net.Listen(":0") during the brief exec window, so they must be avoided
// whenever a safe port is still reachable.
func TestFindAvailablePort_SkipsEphemeralRange(t *testing.T) {
	// Base just below a (pinned) tiny ephemeral range so the 100-port window
	// crosses into it. Override the range to a narrow band for a deterministic,
	// fast test that still leaves safe ports available.
	m := newPortTestManager(t, 18700)
	m.ephemeralLow = 18710
	m.ephemeralHigh = 18760

	for i := 0; i < 20; i++ {
		port, err := m.findAvailablePort(fmt.Sprintf("inst-%d", i), m.logger)
		if err != nil {
			t.Fatalf("findAvailablePort #%d failed: %v", i, err)
		}
		if port >= m.ephemeralLow && port <= m.ephemeralHigh {
			t.Fatalf("port %d was allocated inside the ephemeral range [%d, %d]", port, m.ephemeralLow, m.ephemeralHigh)
		}
	}
}

// TestFindAvailablePort_FallsBackInsideEphemeralRange verifies that a base port
// whose entire window lies inside the ephemeral range still yields a usable
// port (preserving backward compatibility) rather than failing outright.
func TestFindAvailablePort_FallsBackInsideEphemeralRange(t *testing.T) {
	// Pin the whole 100-port window inside the ephemeral range.
	m := newPortTestManager(t, 40000)
	m.ephemeralLow = 39000
	m.ephemeralHigh = 41000

	port, err := m.findAvailablePort("inst-fallback", m.logger)
	if err != nil {
		t.Fatalf("findAvailablePort should fall back inside the ephemeral range, got error: %v", err)
	}
	if port < m.basePort || port >= m.basePort+100 {
		t.Fatalf("fallback port %d outside the configured window [%d, %d)", port, m.basePort, m.basePort+100)
	}
	m.closeReservedListener(port)
}

// TestDetectEphemeralPortRange asserts the detector returns a sane range on any
// platform (real values on Linux, the documented fallback elsewhere).
func TestDetectEphemeralPortRange(t *testing.T) {
	low, high := detectEphemeralPortRange()
	if low <= 0 || high < low {
		t.Fatalf("detectEphemeralPortRange returned invalid range [%d, %d]", low, high)
	}
}
