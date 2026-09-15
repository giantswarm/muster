package clock

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
)

// EnvControlSocket is the environment variable that selects the clock's
// control socket. Set by the integration test harness for every instance it
// runs; unset in production, where nothing can move the clock.
const EnvControlSocket = "MUSTER_TEST_CLOCK"

// controlTimeout bounds one exchange on the control socket, either side.
const controlTimeout = 5 * time.Second

// StartControl serves the clock's control endpoint on the Unix socket named
// by MUSTER_TEST_CLOCK and returns the function that stops it and removes the
// socket file. With the variable unset it serves nothing and returns a no-op.
// A socket file a previous life of the process left behind is replaced.
func StartControl() (stop func(), err error) {
	path := os.Getenv(EnvControlSocket)
	if path == "" {
		return func() {}, nil
	}
	_ = os.Remove(path) //nolint:gosec // G703: the path is the harness's own socket, selected by a test-only variable
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("clock: listen on %s=%s: %w", EnvControlSocket, path, err)
	}
	logging.Info("Clock", "Controllable clock enabled on %s (%s): timers run on the system time until the clock is advanced", path, EnvControlSocket)
	go serveControl(ln)
	return func() {
		_ = ln.Close()
		_ = os.Remove(path) //nolint:gosec // G703: same socket as above
	}, nil
}

func serveControl(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleControl(conn)
	}
}

// handleControl answers one request line on conn and closes it.
func handleControl(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(controlTimeout))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(conn, respond(strings.TrimSpace(line)))
}

// respond executes one control line. "advance <duration>" moves the clock and
// answers "ok <total offset>"; "offset" answers "ok <total offset>"; anything
// else answers "error: ...".
func respond(line string) string {
	verb, arg, _ := strings.Cut(line, " ")
	switch verb {
	case "offset":
		return "ok " + Offset().String()
	case "advance":
		d, err := time.ParseDuration(strings.TrimSpace(arg))
		if err != nil {
			return "error: invalid duration " + strconv.Quote(strings.TrimSpace(arg))
		}
		total, err := Advance(d)
		if err != nil {
			return "error: " + err.Error()
		}
		logging.Info("Clock", "Clock advanced by %s, offset now %s", d, total)
		return "ok " + total.String()
	}
	return "error: unknown command " + strconv.Quote(verb)
}

// RemoteAdvance advances the clock of the process serving its control
// endpoint on path by d and returns that process's total offset.
func RemoteAdvance(path string, d time.Duration) (time.Duration, error) {
	return request(path, "advance "+d.String())
}

// RemoteOffset returns the total offset of the process serving its control
// endpoint on path.
func RemoteOffset(path string) (time.Duration, error) {
	return request(path, "offset")
}

func request(path, line string) (time.Duration, error) {
	conn, err := net.DialTimeout("unix", path, controlTimeout)
	if err != nil {
		return 0, fmt.Errorf("clock control %s: %w", path, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(controlTimeout))
	if _, err := fmt.Fprintln(conn, line); err != nil {
		return 0, fmt.Errorf("clock control %s: %w", path, err)
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("clock control %s: %w", path, err)
	}
	reply = strings.TrimSpace(reply)
	if total, ok := strings.CutPrefix(reply, "ok "); ok {
		return time.ParseDuration(total)
	}
	return 0, fmt.Errorf("clock control %s: %s", path, reply)
}
