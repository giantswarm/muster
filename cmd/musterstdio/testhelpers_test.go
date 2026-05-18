package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	envTestChild       = "MUSTERSTDIO_TEST_CHILD"
	envTestChildMode   = "MUSTERSTDIO_TEST_CHILD_MODE"
	envTestChildStderr = "MUSTERSTDIO_TEST_CHILD_STDERR"

	flagChildCommand = "--child-command"
	flagChildArg     = "--child-arg"
	flagChildEnv     = "--child-env"
	flagListenPort   = "--listen-port"

	loopbackEphemeral = "127.0.0.1:0"
)

// TestMain re-executes this binary as a fake stdio MCP child when the
// envTestChild env var is set so we never depend on an external helper.
func TestMain(m *testing.M) {
	if os.Getenv(envTestChild) == "1" {
		fakeChildMain()
		return
	}
	os.Exit(m.Run())
}

// fakeChildMain implements behaviors used by tests, controlled via
// envTestChildMode:
//
//	"echo"        — for each request frame, reply with a result echoing params
//	"slow"        — sleep before responding (used to test cancellation/drain)
//	"crash"       — exit non-zero before reading anything
//	"ignore_term" — install handler that ignores SIGTERM so the shim falls back to SIGKILL
//	"out_of_order" — buffer up to 2 requests and respond in reverse order
func fakeChildMain() {
	mode := os.Getenv(envTestChildMode)
	if msg := os.Getenv(envTestChildStderr); msg != "" {
		_, _ = fmt.Fprintln(os.Stderr, msg)
	}
	switch mode {
	case "crash":
		os.Exit(7)
	case "ignore_term":
		ignoreSIGTERM()
	}

	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer func() { _ = out.Flush() }()

	pending := make([][]byte, 0, 2)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\n")
			respond := func(req []byte) {
				resp, hasID := buildResponse(req)
				if !hasID {
					return
				}
				if mode == "slow" {
					time.Sleep(200 * time.Millisecond)
				}
				_, _ = out.Write(resp)
				_ = out.WriteByte('\n')
				_ = out.Flush()
			}
			if mode == "out_of_order" {
				pending = append(pending, append([]byte(nil), line...))
				if len(pending) == 2 {
					respond(pending[1])
					respond(pending[0])
					pending = pending[:0]
				}
			} else {
				respond(line)
			}
		}
		if err != nil {
			return
		}
	}
}

func buildResponse(req []byte) ([]byte, bool) {
	var probe struct {
		ID     *json.RawMessage `json:"id,omitempty"`
		Method string           `json:"method,omitempty"`
		Params json.RawMessage  `json:"params,omitempty"`
	}
	if err := json.Unmarshal(req, &probe); err != nil {
		return nil, false
	}
	if probe.ID == nil {
		return nil, false
	}
	params := probe.Params
	if len(params) == 0 {
		params = json.RawMessage(`null`)
	}
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"echo":%s,"method":%q}}`,
		string(*probe.ID), string(params), probe.Method)
	return []byte(body), true
}

// ignoreSIGTERM installs a signal handler that swallows SIGTERM so the shim
// has to fall back to SIGKILL when stopping the child.
func ignoreSIGTERM() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	go func() {
		for range ch {
			// drop
		}
	}()
}

// buildShimBinary compiles cmd/musterstdio into a temp directory and returns
// the resulting binary path. Tests that need a real subprocess use this; pure
// unit tests use the in-process API instead.
func buildShimBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "musterstdio")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "go build failed: %s", stderr.String())
	return bin
}

func waitForHealth(t *testing.T, url string, deadline time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return false
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, deadline, 10*time.Millisecond, "shim never became healthy at %s", url)
}

// runChildArgs returns the cmd/args/env that re-exec the test binary as the
// fake child in the given mode.
func runChildArgs(mode string) (string, []string, []string) {
	env := []string{envTestChild + "=1", envTestChildMode + "=" + mode}
	return os.Args[0], []string{"-test.run=ZZZ_NoRealTests_ZZZ"}, env
}

// childFlagsFor returns flags that point the shim at the fake child running
// in the given mode.
func childFlagsFor(t *testing.T, mode string, listenPort int) []string {
	t.Helper()
	cmd, args, env := runChildArgs(mode)
	flags := []string{
		flagChildCommand, cmd,
		flagListenPort, strconv.Itoa(listenPort),
	}
	for _, a := range args {
		flags = append(flags, flagChildArg, a)
	}
	for _, e := range env {
		flags = append(flags, flagChildEnv, e)
	}
	return flags
}

// captured is a thread-safe collector for stderr/log output read in assertions.
type captured struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *captured) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *captured) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// httpPostJSON is a tiny helper around POST /mcp.
func httpPostJSON(t *testing.T, ctx context.Context, url string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, data
}

// parseEnv converts KEY=VALUE strings into a map.
func parseEnv(pairs []string) map[string]string {
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		m[k] = v
	}
	return m
}

// testLogger returns a slog logger that writes through the test's logger so
// failed-test output remains readable.
func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(testWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(bytes.TrimRight(p, "\n")))
	return len(p), nil
}
