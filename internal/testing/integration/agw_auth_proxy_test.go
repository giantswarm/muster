//go:build linux

// Package integration: agw_auth_proxy_test.go verifies that agentgateway
// v1.2.1 round-trips the upstream's 401 + WWW-Authenticate header
// unchanged, including the RFC 9728 resource_metadata parameter that
// muster's OAuth pending-auth path keys on (see
// internal/mcpserver/types.go:extractAuthInfoFromHandler).
//
// Skipped unless MUSTER_AGW_BINARY points at the pinned agentgateway
// release (the same env var internal/agentgateway/binary.Resolve honours).
//
// The test deliberately bypasses muster: it dials agentgateway directly so
// any header rewriting performed by agentgateway is observable on the
// response, isolating the question "does agentgateway preserve the auth
// challenge?" from muster's reconciler / client-construction code.
package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// agwProxyPort is the port the test agentgateway listener binds. It
	// differs from the default 8080 so this test can run alongside the
	// subprocess-topology test without contention.
	agwProxyPort = 18080
	// agwReadyPort is agentgateway's own readiness probe. Same caveat.
	agwReadyPort = 19021
	// upstreamRoutePath is the per-MCPServer path agentgateway exposes.
	upstreamRoutePath = "/mcp/protected"
)

func TestAgentgatewayProxiesWWWAuthenticate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; skipped in -short mode")
	}
	agwBin := os.Getenv(envAgwBinary)
	if agwBin == "" {
		t.Skipf("%s not set; download agentgateway-v1.2.1 and export the path to run this test", envAgwBinary)
	}
	requireExecutable(t, agwBin)

	upstream := newAuthChallengeUpstream(t)
	defer upstream.Close()
	upstreamHost, upstreamPort := splitHostPort(t, upstream.Listener.Addr().String())

	configDir := t.TempDir()
	configPath := writeAuthProxyConfig(t, configDir, upstreamHost, upstreamPort)

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, agwBin, "-f", configPath) //nolint:gosec // path is an env-supplied test fixture, not user input
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	require.NoError(t, cmd.Start())
	agwPID := cmd.Process.Pid
	t.Logf("agentgateway pid=%d", agwPID)

	t.Cleanup(func() {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return
		}
		_ = syscall.Kill(-agwPID, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	readyURL := fmt.Sprintf("http://127.0.0.1:%d/healthz/ready", agwReadyPort)
	require.Eventually(t, func() bool { return probeReadyURL(ctx, readyURL) },
		30*time.Second, 250*time.Millisecond,
		"agentgateway readiness probe at %s never came up", readyURL)

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d%s", agwProxyPort, upstreamRoutePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyURL, nil)
	require.NoError(t, err)

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, err, "GET %s through agentgateway must succeed at the transport layer", proxyURL)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"upstream 401 must be surfaced as 401 through agentgateway")

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	require.NotEmpty(t, wwwAuth,
		"agentgateway must preserve the WWW-Authenticate header; muster's RFC 9728 discovery fails without it")
	require.Truef(t, strings.HasPrefix(wwwAuth, "Bearer "),
		"agentgateway must preserve the Bearer scheme verbatim, got %q", wwwAuth)
	require.Containsf(t, wwwAuth, `resource_metadata="`,
		"agentgateway must preserve the RFC 9728 resource_metadata parameter, got %q", wwwAuth)
	require.Containsf(t, wwwAuth, upstream.URL,
		"agentgateway must preserve the upstream's full resource_metadata URL, got %q", wwwAuth)
}

// newAuthChallengeUpstream returns an httptest.Server that responds with
// 401 + the exact WWW-Authenticate format muster's mock protected MCP
// server emits (see internal/testing/mock/protected_mcp_server.go:308).
func newAuthChallengeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewUnstartedServer(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		issuer := "http://" + server.Listener.Addr().String()
		resourceMetadata := issuer + "/.well-known/oauth-protected-resource"
		authHeader := fmt.Sprintf(`Bearer resource_metadata="%s", error="invalid_token", error_description="missing access token"`, resourceMetadata)
		w.Header().Set("WWW-Authenticate", authHeader)
		w.WriteHeader(http.StatusUnauthorized)
	})
	server.Start()
	return server
}

func writeAuthProxyConfig(t *testing.T, dir, upstreamHost string, upstreamPort int) string {
	t.Helper()
	configYAML := fmt.Sprintf(`# yaml-language-server: $schema=https://raw.githubusercontent.com/agentgateway/agentgateway/refs/tags/v1.2.1/schema/config.json
binds:
  - port: %d
    listeners:
      - name: muster
        routes:
          - name: protected
            matches:
              - path:
                  pathPrefix: %s
            backends:
              - mcp:
                  targets:
                    - name: protected
                      mcp:
                        host: %s
                        port: %d
                        path: /
            policies:
              backendAuth:
                passthrough: {}
`, agwProxyPort, upstreamRoutePath, upstreamHost, upstreamPort)
	path := filepath.Join(dir, "agentgateway.yaml")
	require.NoError(t, os.WriteFile(path, []byte(configYAML), 0o600))
	return path
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	parts := strings.Split(addr, ":")
	require.Len(t, parts, 2, "addr %q must be host:port", addr)
	port, err := strconv.Atoi(parts[1])
	require.NoError(t, err)
	host := parts[0]
	if host == "" {
		host = "127.0.0.1"
	}
	return host, port
}

func probeReadyURL(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 250 * time.Millisecond}).Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
