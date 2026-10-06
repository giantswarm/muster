//go:build !windows

package listenfds

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// childEnv makes this test binary, re-executed by a test, play the child that
// inherits the listeners instead of running the tests.
const childEnv = "LISTENFDS_TEST_CHILD"

// TestMain diverts the child role before any test runs.
func TestMain(m *testing.M) {
	if os.Getenv(childEnv) != "" {
		runChild()
		return
	}
	os.Exit(m.Run())
}

// runChild takes the API listener and the rest the way muster serve does,
// reports what it found on stdout, and answers one connection on each
// listener with its name.
func runChild() {
	api, err := Take(API)
	if err != nil {
		fmt.Printf("error %v\n", err)
		os.Exit(1)
	}
	rest, err := TakeAllBut(API)
	if err != nil {
		fmt.Printf("error %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("api=%d rest=%d env=%q\n", len(api), len(rest), os.Getenv(envFDs)+os.Getenv(envNames))
	var served sync.WaitGroup
	for name, lns := range map[string][]net.Listener{API: api, Metrics: rest} {
		for _, ln := range lns {
			served.Go(func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				_, _ = io.WriteString(conn, name+"\n")
				_ = conn.Close()
			})
		}
	}
	served.Wait()
	os.Exit(0)
}

func startChild(t *testing.T, extraEnv []string, listeners ...Listener) *bufio.Reader {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self) //nolint:gosec // re-exec of this test binary
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	files, err := Pass(cmd, listeners...)
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	err = cmd.Start()
	for _, f := range files {
		_ = f.Close()
	}
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return bufio.NewReader(stdout)
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v (got %q)", err, line)
	}
	return strings.TrimSpace(line)
}

// TestPassServesInChild hands two listeners to a child and closes the
// parent's copies: the child serves both, under their names, and its own
// children see no activation variables.
func TestPassServesInChild(t *testing.T) {
	api, metrics := listen(t), listen(t)
	out := startChild(t, nil,
		Listener{Name: API, Listener: api},
		Listener{Name: Metrics, Listener: metrics},
	)
	apiAddr, metricsAddr := api.Addr().String(), metrics.Addr().String()
	_ = api.Close()
	_ = metrics.Close()

	if got := readLine(t, out); got != `api=1 rest=1 env=""` {
		t.Fatalf("child reported %q", got)
	}
	for addr, want := range map[string]string{apiAddr: API, metricsAddr: Metrics} {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial %s after the parent closed its listener: %v", want, err)
		}
		if got := readLine(t, bufio.NewReader(conn)); got != want {
			t.Fatalf("%s answered %q", addr, got)
		}
		_ = conn.Close()
	}
}

// TestListenersForAnotherPIDAreNotTaken: systemd's LISTEN_PID names the
// process the descriptors are for; a child that inherited the variables is
// not it.
func TestListenersForAnotherPIDAreNotTaken(t *testing.T) {
	out := startChild(t, []string{envPID + "=1"}, Listener{Name: API, Listener: listen(t)})
	if got := readLine(t, out); got != `api=0 rest=0 env=""` {
		t.Fatalf("child reported %q", got)
	}
}

func TestPassRefusesUsedExtraFiles(t *testing.T) {
	cmd := exec.Command("true")
	cmd.ExtraFiles = []*os.File{os.Stdin}
	if _, err := Pass(cmd, Listener{Name: API, Listener: listen(t)}); err == nil {
		t.Fatal("Pass accepted a command whose ExtraFiles are in use")
	}
}
