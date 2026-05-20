package aggregator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"syscall"
	"time"

	internalmcp "github.com/giantswarm/muster/internal/mcpserver"
)

// proxyURLFor returns the dial URL muster's aggregator uses to reach the
// named MCPServer via agentgateway: <proxy>/mcp/<name>. Trailing slashes on
// the proxy are stripped so the join is single-slash regardless of caller
// hygiene. Callers MUST validate proxy non-empty before invoking; an empty
// proxy yields a relative path and is a misconfiguration.
func proxyURLFor(proxy, name string) string {
	return strings.TrimRight(proxy, "/") + "/mcp/" + name
}

const (
	agentgatewayReadyPollInterval = 50 * time.Millisecond
	agentgatewayReadyMaxWait      = 2 * time.Second
)

// readinessURLFor returns agentgateway's readiness URL for the configured
// port. Zero readinessPort signals cluster mode where agentgateway runs
// out-of-band and probing is the caller's responsibility — returns "".
func readinessURLFor(readinessPort uint16) string {
	if readinessPort == 0 {
		return ""
	}
	return fmt.Sprintf("http://localhost:%d/healthz/ready", readinessPort)
}

// upstreamDialRetryAttempts caps the per-route dial loop that runs after
// the readiness handshake succeeds. agentgateway reports ready before its
// file-watch reload binds a brand-new /mcp/<name> route — readiness is
// gateway-level, not route-level — so a connection-refused error after a
// fresh Apply means "route not bound yet, try again." Four attempts at
// 100ms gives ~400ms which comfortably covers the observed bind latency.
const (
	upstreamDialRetryAttempts = 4
	upstreamDialRetryInterval = 100 * time.Millisecond
)

// initializeUpstream runs the readiness + dial handshake the aggregator
// performs for every new MCPServer. The readiness probe catches the case
// where agentgateway is restarting; the dial-side retry catches the
// per-route bind race after a yaml.Applier write. Non-transport errors
// (auth, protocol) are returned verbatim so the AuthRequiredError path keeps
// working.
func initializeUpstream(ctx context.Context, client internalmcp.MCPClient, readyURL string) error {
	if err := waitForAgentgatewayReady(ctx, nil, readyURL); err != nil {
		// Continue: a readiness failure is best-effort. The dial-side
		// retry below still gets a chance to succeed.
		_ = err
	}
	var lastErr error
	for attempt := 0; attempt < upstreamDialRetryAttempts; attempt++ {
		err := client.Initialize(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(upstreamDialRetryInterval):
		}
	}
	return lastErr
}

// waitForAgentgatewayReady polls agentgateway's /healthz/ready until the
// gateway returns 2xx or ctx fires. Returns nil immediately when readyURL
// is empty (cluster mode — agentgateway runs out-of-band, readiness is the
// pod owner's responsibility, not muster's).
func waitForAgentgatewayReady(ctx context.Context, httpClient *http.Client, readyURL string) error {
	if readyURL == "" {
		return nil
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: agentgatewayReadyPollInterval * 4}
	}
	deadline := time.Now().Add(agentgatewayReadyMaxWait)
	probeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	ticker := time.NewTicker(agentgatewayReadyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, readyURL, nil)
		if err != nil {
			return fmt.Errorf("build readiness request: %w", err)
		}
		resp, err := httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("agentgateway readiness %s returned status %d", readyURL, resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-probeCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("agentgateway readiness wait timed out (last error: %v)", lastErr)
			}
			return probeCtx.Err()
		case <-ticker.C:
		}
	}
}
