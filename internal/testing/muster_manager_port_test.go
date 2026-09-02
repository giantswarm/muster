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
	// before exec), the port becomes available for muster serve to bind on the
	// address it uses. (A wildcard bind stays blocked by the guard listener on
	// Linux — see TestPortGuard_ProtectsPortThroughExecWindow.)
	m.closeReservedListener(port)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("port %d not bindable after closeReservedListener: %v", port, err)
	}
	_ = ln.Close()
	m.releasePort(port, "inst-hold", m.logger)
}

// TestPortGuard_ProtectsPortThroughExecWindow is the regression guard for the
// stolen-port startup failures ("listen tcp 127.0.0.1:<port>: bind: address
// already in use" from muster serve, CircleCI go-build job 23692): between the
// probe listener closing for exec and muster serve binding, the kernel's
// ephemeral port search — what net.Listen(":0") in a mock server and any
// connect() perform — could hand the port out. The guard listener must keep
// every wildcard bind on the port failing through that window and until the
// reservation is released, while muster serve's own 127.0.0.1 bind succeeds,
// and it must keep a second harness on the same base port from taking it.
func TestPortGuard_ProtectsPortThroughExecWindow(t *testing.T) {
	m := newPortTestManager(t, 18800)
	if !m.portGuards {
		t.Skip("port guards are only supported on Linux")
	}

	port, err := m.findAvailablePort("inst-guard", m.logger)
	if err != nil {
		t.Fatalf("findAvailablePort failed: %v", err)
	}
	if _, ok := m.reservedGuards[port]; !ok {
		t.Fatalf("no guard listener held for reserved port %d", port)
	}

	// The exec point: the probe is gone, only the guard protects the port.
	m.closeReservedListener(port)

	// What an ephemeral steal looks like at the socket level: a wildcard bind on
	// the port. It must be rejected while the instance owns the port.
	if ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port)); err == nil {
		_ = ln.Close()
		t.Fatalf("wildcard bind on port %d succeeded during the exec window; the guard did not hold", port)
	}

	// A second harness process sharing the base port must not be handed it.
	other := newPortTestManager(t, 18800)
	otherPort, err := other.findAvailablePort("other-harness", other.logger)
	if err != nil {
		t.Fatalf("second manager could not reserve any port: %v", err)
	}
	if otherPort == port {
		t.Fatalf("second manager was handed port %d while the first still owns it", port)
	}
	other.releasePort(otherPort, "other-harness", other.logger)

	// muster serve binds 127.0.0.1:<port> for its aggregator and its Prometheus
	// exporter; that bind must succeed despite the guard.
	inst, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("instance bind on 127.0.0.1:%d failed while guarded: %v", port, err)
	}
	_ = inst.Close()

	// Releasing the reservation drops the guard and returns the port to the OS.
	m.releasePort(port, "inst-guard", m.logger)
	if _, ok := m.reservedGuards[port]; ok {
		t.Fatalf("guard listener for port %d still held after releasePort", port)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		t.Fatalf("port %d not bindable after releasePort: %v", port, err)
	}
	_ = ln.Close()
}

// TestFindAvailablePort_SkipsOccupiedInstanceAddress verifies the probe checks
// the address muster serve actually binds: a foreign listener on
// 127.0.0.1:<base> must make the allocator move on rather than hand out a port
// the instance cannot bind.
func TestFindAvailablePort_SkipsOccupiedInstanceAddress(t *testing.T) {
	m := newPortTestManager(t, 18900)

	occupant, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", m.basePort))
	if err != nil {
		t.Fatalf("could not occupy 127.0.0.1:%d: %v", m.basePort, err)
	}
	defer func() { _ = occupant.Close() }()

	port, err := m.findAvailablePort("inst-skip", m.logger)
	if err != nil {
		t.Fatalf("findAvailablePort failed: %v", err)
	}
	if port == m.basePort {
		t.Fatalf("allocator handed out occupied port %d", port)
	}
	m.releasePort(port, "inst-skip", m.logger)
}

// TestFindAvailablePort_TakesLowestFreePort documents the allocation order:
// the lowest free port in the window wins, so a released port is handed to the
// next instance and the harness's footprint stays compact (see portWindowSize).
func TestFindAvailablePort_TakesLowestFreePort(t *testing.T) {
	m := newPortTestManager(t, 19000)

	first, err := m.findAvailablePort("inst-a", m.logger)
	if err != nil {
		t.Fatalf("findAvailablePort failed: %v", err)
	}
	if first != m.basePort {
		t.Fatalf("first reservation got %d, want the base port %d", first, m.basePort)
	}
	second, err := m.findAvailablePort("inst-b", m.logger)
	if err != nil {
		t.Fatalf("findAvailablePort failed: %v", err)
	}
	if second != m.basePort+1 {
		t.Fatalf("second reservation got %d, want %d", second, m.basePort+1)
	}

	m.releasePort(first, "inst-a", m.logger)
	third, err := m.findAvailablePort("inst-c", m.logger)
	if err != nil {
		t.Fatalf("findAvailablePort failed: %v", err)
	}
	if third != first {
		t.Fatalf("released port %d was not reused; got %d", first, third)
	}
	m.releasePort(second, "inst-b", m.logger)
	m.releasePort(third, "inst-c", m.logger)
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
	if port < m.basePort || port >= m.basePort+portWindowSize {
		t.Fatalf("fallback port %d outside the configured window [%d, %d)", port, m.basePort, m.basePort+portWindowSize)
	}
	m.releasePort(port, "inst-fallback", m.logger)
}

// TestDetectEphemeralPortRange asserts the detector returns a sane range on any
// platform (real values on Linux, the documented fallback elsewhere).
func TestDetectEphemeralPortRange(t *testing.T) {
	low, high := detectEphemeralPortRange()
	if low <= 0 || high < low {
		t.Fatalf("detectEphemeralPortRange returned invalid range [%d, %d]", low, high)
	}
}
