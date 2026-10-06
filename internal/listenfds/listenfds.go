package listenfds

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Names the harness gives the listeners of a `muster serve` instance.
const (
	// API is the aggregator's MCP endpoint.
	API = "api"
	// Metrics is the Prometheus exporter's /metrics endpoint.
	Metrics = "metrics"
)

// PrometheusExporter is the OTEL_METRICS_EXPORTER value that serves the
// Prometheus exporter on the inherited listener named Metrics instead of
// binding OTEL_EXPORTER_PROMETHEUS_HOST:OTEL_EXPORTER_PROMETHEUS_PORT.
const PrometheusExporter = "prometheus-inherited"

const (
	envPID   = "LISTEN_PID"
	envFDs   = "LISTEN_FDS"
	envNames = "LISTEN_FDNAMES"

	// firstFD is the first inherited descriptor: 0-2 are stdio.
	firstFD = 3
)

// Supported reports whether this platform passes descriptors to a child.
// Elsewhere the harness closes its probe listener and the child binds the
// port itself.
const Supported = supported

// Listener is a listener to pass to a child under a name.
type Listener struct {
	Name     string
	Listener net.Listener
}

// filer is a listener whose socket can be duplicated into an *os.File;
// *net.TCPListener and *net.UnixListener are.
type filer interface {
	File() (*os.File, error)
}

// Pass arranges for cmd's process to inherit the listeners, in order, under
// their names. The caller closes the returned files once cmd started (or
// failed to); the parent's listeners stay open until the caller closes them.
// cmd.ExtraFiles must be empty: the protocol starts at descriptor 3.
func Pass(cmd *exec.Cmd, listeners ...Listener) ([]*os.File, error) {
	if !Supported {
		return nil, errors.New("passing listeners to a child is not supported on this platform")
	}
	if len(cmd.ExtraFiles) > 0 {
		return nil, errors.New("cmd.ExtraFiles is already in use")
	}
	files := make([]*os.File, 0, len(listeners))
	names := make([]string, 0, len(listeners))
	for _, l := range listeners {
		f, ok := l.Listener.(filer)
		if !ok {
			closeFiles(files)
			return nil, fmt.Errorf("listener %q (%T) has no file descriptor", l.Name, l.Listener)
		}
		file, err := f.File()
		if err != nil {
			closeFiles(files)
			return nil, fmt.Errorf("duplicate listener %q: %w", l.Name, err)
		}
		files = append(files, file)
		names = append(names, l.Name)
	}

	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	// The parent's own activation variables (a harness started by systemd)
	// would describe descriptors the child does not have.
	env = slices.DeleteFunc(slices.Clone(env), func(e string) bool {
		return strings.HasPrefix(e, envPID+"=") || strings.HasPrefix(e, envFDs+"=") || strings.HasPrefix(e, envNames+"=")
	})
	cmd.Env = append(env,
		envFDs+"="+strconv.Itoa(len(files)),
		envNames+"="+strings.Join(names, ":"),
	)
	cmd.ExtraFiles = files
	return files, nil
}

func closeFiles(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

var (
	inheritOnce sync.Once
	inheritErr  error
	mu          sync.Mutex
	inherited   []Listener
)

// load takes the process's inherited listeners over once: it marks their
// descriptors close-on-exec and clears the activation variables, so a child
// muster starts (a stdio MCP server) neither inherits nor claims them.
func load() error {
	inheritOnce.Do(func() {
		defer func() {
			_ = os.Unsetenv(envPID)
			_ = os.Unsetenv(envFDs)
			_ = os.Unsetenv(envNames)
		}()
		if pid := os.Getenv(envPID); pid != "" && pid != strconv.Itoa(os.Getpid()) {
			return
		}
		count := os.Getenv(envFDs)
		if count == "" {
			return
		}
		n, err := strconv.Atoi(count)
		if err != nil || n < 0 {
			inheritErr = fmt.Errorf("%s=%q is not a descriptor count", envFDs, count)
			return
		}
		names := strings.Split(os.Getenv(envNames), ":")
		for i := 0; i < n; i++ {
			fd := firstFD + i
			name := "unknown"
			if i < len(names) && names[i] != "" {
				name = names[i]
			}
			ln, err := fileListener(fd, name)
			if err != nil {
				inheritErr = fmt.Errorf("inherited descriptor %d (%s): %w", fd, name, err)
				return
			}
			inherited = append(inherited, Listener{Name: name, Listener: ln})
		}
	})
	return inheritErr
}

// Take returns the inherited listeners named name and forgets them, so each
// is served once. None is not an error.
func Take(name string) ([]net.Listener, error) {
	return take(func(n string) bool { return n == name })
}

// TakeAllBut returns every inherited listener whose name is not among names
// and forgets them. The aggregator serves all but the exporter's, whatever a
// systemd socket unit names them.
func TakeAllBut(names ...string) ([]net.Listener, error) {
	return take(func(n string) bool { return !slices.Contains(names, n) })
}

func take(match func(name string) bool) ([]net.Listener, error) {
	if err := load(); err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	var taken []net.Listener
	inherited = slices.DeleteFunc(inherited, func(l Listener) bool {
		if match(l.Name) {
			taken = append(taken, l.Listener)
			return true
		}
		return false
	})
	return taken, nil
}
