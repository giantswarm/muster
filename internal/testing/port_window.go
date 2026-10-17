package testing

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/giantswarm/muster/v5/internal/netdiag"
)

// portWindowExhaustedError names the window, every port in it that is not
// reserved by this harness and so was found busy, and the sockets holding
// them, so a full window points at its occupants instead of a bare count.
// Caller must hold portMu.
func (m *musterInstanceManager) portWindowExhaustedError() error {
	var busy []int
	for port := m.basePort; port < m.basePort+portWindowSize; port++ {
		if _, reserved := m.reservedPorts[port]; !reserved {
			busy = append(busy, port)
		}
	}
	msg := fmt.Sprintf("no available port in the window [%d, %d]: %d reserved by this run",
		m.basePort, m.basePort+portWindowSize-1, len(m.reservedPorts))
	if len(busy) > 0 {
		msg += fmt.Sprintf(", busy: %s; choose another --base-port", formatPorts(busy))
		if occupants := netdiag.PortOccupants(busy...); occupants != "" {
			msg += "\n--- sockets on the busy ports ---\n" + strings.TrimRight(occupants, "\n")
		}
	}
	return errors.New(msg)
}

// formatPorts renders a sorted port list compactly, collapsing runs into
// ranges ("18000-18003, 18007").
func formatPorts(ports []int) string {
	var parts []string
	for i := 0; i < len(ports); {
		j := i
		for j+1 < len(ports) && ports[j+1] == ports[j]+1 {
			j++
		}
		if j == i {
			parts = append(parts, strconv.Itoa(ports[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", ports[i], ports[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// DefaultBasePort is the first port of muster test's port window. It lies
// below the Kubernetes NodePort range and the OS ephemeral range, so a test
// run never takes a port a kind cluster or a net.Listen(":0") expects.
const DefaultBasePort = 18000

// The Kubernetes NodePort range. kind clusters on the same machine (the
// agentlab among them) map host ports from it, so muster test keeps its
// window out of it.
const (
	nodePortLow  = 30000
	nodePortHigh = 32767
)

// ValidateBasePort refuses a base port whose window [basePort,
// basePort+portWindowSize-1] leaves the valid port range or overlaps the
// Kubernetes NodePort range, naming the window and the overlap.
func ValidateBasePort(basePort int) error {
	high := basePort + portWindowSize - 1
	if basePort < 1 || high > 65535 {
		return fmt.Errorf("--base-port %d: port window [%d, %d] must lie within [1, 65535]", basePort, basePort, high)
	}
	if basePort <= nodePortHigh && high >= nodePortLow {
		return fmt.Errorf("--base-port %d: port window [%d, %d] overlaps the Kubernetes NodePort range [%d, %d], "+
			"which kind clusters on this machine map host ports from; choose a base port of at most %d or at least %d (default %d)",
			basePort, basePort, high, nodePortLow, nodePortHigh, nodePortLow-portWindowSize, nodePortHigh+1, DefaultBasePort)
	}
	return nil
}
