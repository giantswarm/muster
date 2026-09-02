package testing

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// describePortOccupants reports every TCP socket in this network namespace
// whose local port is one of ports: local and remote address, state and —
// when the owning process is visible in this pid namespace — pid and command
// line. It is attached to instance startup failures that name "address
// already in use", so a stolen port names its occupant instead of leaving a
// bare EADDRINUSE. A socket without a visible owner belongs to a process in
// another pid namespace that shares this network namespace (on CircleCI's v2
// docker runtime the job container shares its pod's namespace with the task
// agent). Linux only: it reads /proc/net/tcp{,6} and /proc/*/fd; elsewhere,
// or if /proc is unavailable, it returns "".
func describePortOccupants(ports ...int) string {
	want := make(map[int]bool, len(ports))
	for _, p := range ports {
		want[p] = true
	}
	var entries []socketEntry
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path) //nolint:gosec // one of two fixed /proc tables
		if err != nil {
			continue
		}
		entries = append(entries, parseProcNetTCP(f, want)...)
		_ = f.Close()
	}
	if len(entries) == 0 {
		return ""
	}
	inodes := make(map[string]bool, len(entries))
	for _, e := range entries {
		inodes[e.inode] = true
	}
	owners := socketOwners(inodes)
	sort.Slice(entries, func(i, j int) bool { return entries[i].localPort < entries[j].localPort })
	var b strings.Builder
	for _, e := range entries {
		owner, ok := owners[e.inode]
		if !ok {
			owner = "owner not visible in this pid namespace"
		}
		fmt.Fprintf(&b, "%s -> %s %s (inode %s, %s)\n", e.local, e.remote, e.state, e.inode, owner)
	}
	return b.String()
}

// socketEntry is one row of /proc/net/tcp{,6} reduced to what the diagnostic
// prints.
type socketEntry struct {
	local     string
	localPort int
	remote    string
	state     string
	inode     string
}

// tcpStates maps the hexadecimal st column of /proc/net/tcp to its name.
var tcpStates = map[string]string{
	"01": "ESTABLISHED", "02": "SYN_SENT", "03": "SYN_RECV", "04": "FIN_WAIT1",
	"05": "FIN_WAIT2", "06": "TIME_WAIT", "07": "CLOSE", "08": "CLOSE_WAIT",
	"09": "LAST_ACK", "0A": "LISTEN", "0B": "CLOSING", "0C": "NEW_SYN_RECV",
}

// parseProcNetTCP returns the rows of a /proc/net/tcp or /proc/net/tcp6 table
// whose local port is in want.
func parseProcNetTCP(r io.Reader, want map[int]bool) []socketEntry {
	var out []socketEntry
	sc := bufio.NewScanner(r)
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// sl local_address rem_address st tx_queue:rx_queue tr:tm->when retrnsmt uid timeout inode ...
		if len(f) < 10 {
			continue
		}
		local, lport, ok := decodeProcAddr(f[1])
		if !ok || !want[lport] {
			continue
		}
		remote, _, _ := decodeProcAddr(f[2])
		state, ok := tcpStates[strings.ToUpper(f[3])]
		if !ok {
			state = "state " + f[3]
		}
		out = append(out, socketEntry{local: local, localPort: lport, remote: remote, state: state, inode: f[9]})
	}
	return out
}

// decodeProcAddr turns a /proc/net/tcp address ("0100007F:7541" or its 32-hex
// IPv6 form) into "host:port" and the port. The kernel prints each 32-bit
// word of the address in host byte order.
func decodeProcAddr(s string) (string, int, bool) {
	host, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return "", 0, false
	}
	port64, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return "", 0, false
	}
	raw, err := hex.DecodeString(host)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", 0, false
	}
	ip := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		ip[i], ip[i+1], ip[i+2], ip[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(port64))), int(port64), true
}

// socketOwners maps socket inodes to "pid N (cmdline)" for every process in
// this pid namespace whose fd table references the socket. Processes that
// cannot be inspected are skipped.
func socketOwners(inodes map[string]bool) map[string]string {
	owners := make(map[string]string)
	procs, err := filepath.Glob("/proc/[0-9]*/fd")
	if err != nil {
		return owners
	}
	for _, fdDir := range procs {
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if !inodes[inode] {
				continue
			}
			pidDir := filepath.Dir(fdDir)
			cmdline, _ := os.ReadFile(filepath.Join(pidDir, "cmdline")) //nolint:gosec // /proc/<pid>/cmdline from a /proc glob
			cmd := strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))
			if len(cmd) > 120 {
				cmd = cmd[:120] + "…"
			}
			owners[inode] = fmt.Sprintf("pid %s (%s)", filepath.Base(pidDir), cmd)
		}
	}
	return owners
}
