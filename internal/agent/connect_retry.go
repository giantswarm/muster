package agent

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// DefaultConnectRetryBudget is how long Connect keeps retrying a connection
// that failed on the way to the aggregator. A gateway in front of muster
// answers for a while after the aggregator's pod was replaced -- the old pod's
// address is dialled until the connect timeout, or no endpoint is ready yet --
// and a fresh session reaches the new pod once it is ready, typically within
// half a minute.
const DefaultConnectRetryBudget = 30 * time.Second

// connectRetryDelays are the pauses between connection attempts; the last one
// repeats until the budget or the caller's context ends.
var connectRetryDelays = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}

// transientGatewayAnswers are the answers of a proxy in front of muster that
// say the aggregator behind it was not reached, never that it refused the
// request: agentgateway's failed upstream dial (a 500 or 503 carrying
// "upstream call failed: Connect: deadline has elapsed"), Envoy's and
// nginx's bad-gateway family.
var transientGatewayAnswers = []string{
	"upstream call failed",
	"upstream connect error",
	"no healthy upstream",
	"status 502",
	"status 503",
	"status 504",
}

// isTransientConnectError reports whether a failed connection attempt is
// worth another one with a fresh session: the network, or a gateway that did
// not reach the aggregator. A 401, a server that speaks only legacy SSE, a
// host that does not resolve and the caller's own cancellation are final.
func isTransientConnectError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if pkgoauth.IsOAuthUnauthorizedError(err) || errors.Is(err, transport.ErrLegacySSEServer) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := err.Error()
	for _, answer := range transientGatewayAnswers {
		if strings.Contains(msg, answer) {
			return true
		}
	}
	return false
}

// connectRetryDelay is the pause before attempt n (1 for the second attempt).
func connectRetryDelay(n int) time.Duration {
	if n > len(connectRetryDelays) {
		n = len(connectRetryDelays)
	}
	return connectRetryDelays[n-1]
}

// retryConnect runs attempt until it succeeds, fails for good, or budget has
// passed since the first attempt; the caller's context bounds it too. The
// error is the last attempt's.
func retryConnect(ctx context.Context, budget time.Duration, logger *Logger, attempt func(context.Context) error) error {
	start := time.Now()
	for n := 1; ; n++ {
		err := attempt(ctx)
		if !isTransientConnectError(ctx, err) {
			return err
		}
		delay := connectRetryDelay(n)
		if time.Since(start)+delay > budget {
			return err
		}
		if logger != nil {
			logger.Info("Connecting to the aggregator failed (attempt %d), retrying in %s: %v", n, delay, err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
}
