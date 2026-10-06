//go:build !windows

package testing

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/listenfds"
)

const (
	// helperRoleServe makes the re-executed test binary stand in for the
	// `muster serve` startMusterProcess starts.
	helperRoleServe = "serve"
	// helperServeAPIPortEnv is the API port the stand-in binds when it
	// inherits no listener, as muster serve binds its configured port.
	helperServeAPIPortEnv = "MUSTER_TEST_SERVE_API_PORT"
)

// runHelperServe takes its listeners the way muster serve does -- inherited,
// or bound on the configured API port and OTEL_EXPORTER_PROMETHEUS_PORT --
// and answers every connection with the listener's name and how it got it
// until it is killed. A failed bind exits 1 with the error, as muster
// serve's does.
func runHelperServe() {
	listeners := map[string]net.Listener{}
	how := map[string]string{}
	for name, port := range map[string]string{
		listenfds.API:     os.Getenv(helperServeAPIPortEnv),
		listenfds.Metrics: os.Getenv("OTEL_EXPORTER_PROMETHEUS_PORT"),
	} {
		inherited, err := listenfds.Take(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve stand-in: %v\n", err)
			os.Exit(1)
		}
		if len(inherited) == 1 {
			listeners[name], how[name] = inherited[0], "inherited"
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve stand-in: %s: %v\n", name, err)
			os.Exit(1)
		}
		listeners[name], how[name] = ln, "bound"
	}
	for name, ln := range listeners {
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				_, _ = io.WriteString(conn, name+" "+how[name]+"\n")
				_ = conn.Close()
			}
		}()
	}
	// The test kills the stand-in; the timer bounds an orphan's life.
	select {
	case <-notifyTerm():
	case <-time.After(time.Minute):
	}
	os.Exit(0)
}

// TestStartMusterProcessWhileTheHarnessForks starts muster serve stand-ins
// through startMusterProcess while other goroutines fork child processes, as
// a harness running scenarios in parallel does all the time. A probe listener
// closed for muster serve to bind its port again lives on in a child forked
// at that moment until the child's exec, and the bind fails with "address
// already in use". Every stand-in must serve the listeners it inherited and
// bind no port itself.
func TestStartMusterProcessWhileTheHarnessForks(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := newPortTestManager(t, 19200)
	m.serveBinaryOnce.Do(func() {})
	m.serveBinary = ServeBinary{Path: self}
	t.Setenv(helperRoleEnv, helperRoleServe)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	forkCtx, stopForks := context.WithCancel(ctx)
	var forks sync.WaitGroup
	forkEnv := slices.DeleteFunc(os.Environ(), func(e string) bool {
		return strings.HasPrefix(e, helperRoleEnv+"=")
	})
	for range 4 {
		forks.Go(func() {
			for forkCtx.Err() == nil {
				fork := exec.CommandContext(forkCtx, self, "-test.run=^$") //nolint:gosec // re-exec of this test binary
				fork.Env = forkEnv
				_ = fork.Run()
			}
		})
	}
	defer func() {
		stopForks()
		forks.Wait()
	}()

	configPath := t.TempDir()
	deadline := time.Now().Add(time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		id := fmt.Sprintf("inst-%d", i)
		port, err := m.findAvailablePort(id, m.logger)
		if err != nil {
			t.Fatal(err)
		}
		metricsPort, err := m.findAvailablePort(id, m.logger)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(helperServeAPIPortEnv, fmt.Sprint(port))
		proc, err := m.startMusterProcess(ctx, configPath, port, metricsPort, instanceTiming{}, m.logger)
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		for name, p := range map[string]int{listenfds.API: port, listenfds.Metrics: metricsPort} {
			if err := expectServed(ctx, proc, p, name); err != nil {
				_ = proc.cmd.Process.Kill()
				<-proc.exited
				t.Fatalf("start %d: %s port %d: %v\n%s", i, name, p, err, proc.logCapture.getLogs())
			}
		}
		_ = proc.cmd.Process.Kill()
		<-proc.exited
		proc.logCapture.close()
		m.releasePort(port, id, m.logger)
		m.releasePort(metricsPort, id, m.logger)
	}
}

// expectServed waits until the process answers on port that it serves the
// listener named name it inherited, or exits.
func expectServed(ctx context.Context, proc *managedProcess, port int, name string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			line, err := bufio.NewReader(conn).ReadString('\n')
			_ = conn.Close()
			if err != nil {
				return fmt.Errorf("read: %w", err)
			}
			if got := strings.TrimSpace(line); got != name+" inherited" {
				return fmt.Errorf("answered %q", got)
			}
			return nil
		}
		select {
		case <-proc.exited:
			return fmt.Errorf("process exited: %v", proc.waitErr)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
