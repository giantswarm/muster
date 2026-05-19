package aggregator

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"time"

	internalmcp "github.com/giantswarm/muster/internal/mcpserver"

	"github.com/giantswarm/muster/pkg/logging"
)

// proxyURLFor returns the dial URL muster's aggregator uses to reach the
// named MCPServer via agentgateway: <proxy>/mcp/<name>. Trailing slashes on
// the proxy are stripped so the join is single-slash regardless of caller
// hygiene. Callers MUST validate proxy non-empty before invoking; an empty
// proxy yields a relative path and is a misconfiguration.
func proxyURLFor(proxy, name string) string {
	return strings.TrimRight(proxy, "/") + "/mcp/" + name
}

// upstreamConnectRetryAttempts caps the inner retry loop in
// initializeWithConnectRetry. Six attempts at 250ms cadence gives ~1.5s of
// recovery, comfortably longer than agentgateway's observed ~300ms
// file-watch reload window without dragging out hard failures.
const upstreamConnectRetryAttempts = 6

// upstreamConnectRetryInterval is the per-attempt wait between Initialize
// retries while the connection is being refused.
const upstreamConnectRetryInterval = 250 * time.Millisecond

// initializeWithConnectRetry wraps StreamableHTTPClient.Initialize so we
// transparently absorb the race between yaml.Applier writing a new route
// to agentgateway.yaml and agentgateway re-reading the file + binding
// :8080. Without this, the very first RegisterUpstream for a fresh
// MCPServer always fails with "connection refused" until the outer
// reconcile-manager backoff fires (1s+ later).
//
// Only transient connect-refused errors are retried; auth/transport errors
// are returned to the caller verbatim so the AuthRequiredError path keeps
// working.
func initializeWithConnectRetry(ctx context.Context, client internalmcp.MCPClient, dialURL string) error {
	var lastErr error
	for attempt := 0; attempt < upstreamConnectRetryAttempts; attempt++ {
		err := client.Initialize(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isConnectionRefused(err) {
			return err
		}
		logging.Debug("Aggregator-Manager", "upstream %s connect refused (attempt %d/%d), retrying", dialURL, attempt+1, upstreamConnectRetryAttempts)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(upstreamConnectRetryInterval):
		}
	}
	return lastErr
}

func isConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
