package aggregator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/pkg/logging"
	"github.com/giantswarm/muster/v5/pkg/observability"
)

// Every call reads its server's approval settings live from the API server.
// While the API server cannot be reached, the definition muster last read of
// the server (its reconciler reads every server on each watch event and
// resync) decides the call, so the servers muster runs keep working through
// an outage. The fallback is bounded:
//
//   - Only an unreachable API server falls back (a transport error, a
//     timeout, an unavailable answer). Throttling (HTTP 429) does not: the
//     API server is reachable and an admin may have tightened the server
//     meanwhile, so the read is retried briefly within the call's deadline
//     and the call is refused if it stays throttled.
//   - Only a definition read within the maximum age (approval.lastObservedMaxAge,
//     DefaultLastObservedMaxAge) is used: a muster-only partition is
//     indistinguishable from an outage, so the window in which a connector
//     tightened through a reachable API server goes unseen is that age.
//     Beyond it the call is refused as "approval settings unavailable".
//   - A server muster never read, or last read as deleted, is refused, and so
//     is every other failure: NotFound (deleted), Forbidden or Unauthorized
//     (muster may not read it), a definition that cannot be decoded.

// DefaultLastObservedMaxAge is how old a server's last observed definition
// may be and still decide a call while the API server cannot be reached.
const DefaultLastObservedMaxAge = 10 * time.Minute

// lastObservedMaxAge holds the configured maximum age in nanoseconds; zero
// means DefaultLastObservedMaxAge. Set at start (newApprovalSettings).
var lastObservedMaxAge atomic.Int64

func maxLastObservedAge() time.Duration {
	if d := time.Duration(lastObservedMaxAge.Load()); d > 0 {
		return d
	}
	return DefaultLastObservedMaxAge
}

// Reasons recorded on the fallback and refusal counters and in the log.
const (
	reasonUnreachable   = "api_server_unreachable"
	reasonTooOld        = "last_observed_too_old"
	reasonNeverObserved = "never_observed"
	reasonThrottled     = "throttled"
)

// throttleBackoffs are the waits before each retry of a read the API server
// throttled; a variable so tests do not wait.
var throttleBackoffs = []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond}

// maxRetryAfter caps the wait a Retry-After header asks for.
const maxRetryAfter = time.Second

// lastObservedFallbacks counts the calls decided by a server's last observed
// definition; exported as muster_approval_last_observed_fallbacks_total.
var lastObservedFallbacks = sync.OnceValue(func() metric.Int64Counter {
	counter, err := otel.Meter(observability.TracerName).Int64Counter("muster.approval.last_observed_fallbacks",
		metric.WithDescription("Tool calls whose approval settings were taken from the server's last observed definition because the API server could not be reached."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		logging.Warn("Approval", "create muster.approval.last_observed_fallbacks counter: %v", err)
		return nil
	}
	return counter
})

// lastObservedRefusals counts the reads of approval settings that could not
// be answered, by reason; exported as muster_approval_settings_refusals_total.
var lastObservedRefusals = sync.OnceValue(func() metric.Int64Counter {
	counter, err := otel.Meter(observability.TracerName).Int64Counter("muster.approval.settings_refusals",
		metric.WithDescription("Reads of a server's approval settings that failed with no usable fallback, by reason (last_observed_too_old, never_observed, throttled)."),
		metric.WithUnit("{read}"),
	)
	if err != nil {
		logging.Warn("Approval", "create muster.approval.settings_refusals counter: %v", err)
		return nil
	}
	return counter
})

func countRefusal(ctx context.Context, server, reason string) {
	if counter := lastObservedRefusals(); counter != nil {
		counter.Add(ctx, 1, metric.WithAttributes(attribute.String(attrDownstreamServer, server), attribute.String("reason", reason)))
	}
}

// readThrottleRetried runs read and, while the API server throttles it
// (HTTP 429), retries it after a short wait, never past the call's deadline.
// The last error is returned when the read stays throttled.
func readThrottleRetried[T any](ctx context.Context, read func() (T, error)) (T, error) {
	for attempt := 0; ; attempt++ {
		value, err := read()
		if err == nil || !apierrors.IsTooManyRequests(err) || attempt >= len(throttleBackoffs) {
			return value, err
		}
		wait := throttleBackoffs[attempt]
		if seconds, ok := apierrors.SuggestsClientDelay(err); ok {
			wait = max(wait, min(time.Duration(seconds)*time.Second, maxRetryAfter))
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= wait {
			return value, err
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return value, err
		case <-timer.C:
		}
	}
}

// throttledRefusal is the error of a read that stayed throttled.
func throttledRefusal(ctx context.Context, server string, err error) error {
	logging.WarnWithAttrs("Approval", "API server throttled the read of the server's approval settings: refusing the call, no fallback to the last observed definition",
		slog.String("server", server), slog.String("reason", reasonThrottled), slog.String("error", err.Error()))
	countRefusal(ctx, server, reasonThrottled)
	return fmt.Errorf("approval settings unavailable: the API server throttled the read (HTTP 429) and muster does not decide by a last observed definition for that: %w", err)
}

// lastObservedWhileUnreachable returns the last observed definition of
// server when err says the API server cannot be reached and the definition
// is within the maximum age. It returns (nil, nil) when err is not an
// unreachable API server or muster never observed the server (the read's own
// error stands), and an "approval settings unavailable" error when the
// definition is too old.
func lastObservedWhileUnreachable(ctx context.Context, server string, err error) (*api.MCPServerInfo, error) {
	if !apiServerUnreachable(err) {
		return nil, nil
	}
	info, observedAt, ok := api.LastObservedMCPServer(server)
	if !ok || info == nil {
		logging.WarnWithAttrs("Approval", "API server unreachable and no observed definition of the server: refusing the call",
			slog.String("server", server), slog.String("reason", reasonNeverObserved), slog.String("error", err.Error()))
		countRefusal(ctx, server, reasonNeverObserved)
		return nil, nil
	}
	age := clock.Now().Sub(observedAt)
	if limit := maxLastObservedAge(); age > limit {
		logging.WarnWithAttrs("Approval", "API server unreachable and the server's last observed approval settings are too old: refusing the call",
			slog.String("server", server), slog.String("reason", reasonTooOld),
			slog.String("observedAt", observedAt.UTC().Format(time.RFC3339)),
			slog.Duration("age", age.Round(time.Second)), slog.Duration("maxAge", limit),
			slog.String("error", err.Error()))
		countRefusal(ctx, server, reasonTooOld)
		return nil, fmt.Errorf("approval settings unavailable: the API server cannot be reached and the last observed settings of the server are %s old (maximum %s, approval.lastObservedMaxAge): %w",
			age.Round(time.Second), limit, err)
	}
	logging.WarnWithAttrs("Approval", "API server unreachable: deciding the call by the server's last observed approval settings",
		slog.String("server", server), slog.String("reason", reasonUnreachable),
		slog.String("observedAt", observedAt.UTC().Format(time.RFC3339)),
		slog.Duration("age", age.Round(time.Millisecond)),
		slog.String("error", err.Error()))
	if counter := lastObservedFallbacks(); counter != nil {
		counter.Add(ctx, 1, metric.WithAttributes(attribute.String(attrDownstreamServer, server), attribute.String("reason", reasonUnreachable)))
	}
	return info, nil
}

// apiServerUnreachable reports whether err says the API server could not be
// reached or could not answer: a transport error, a timeout, or an
// unavailable answer. Throttling (429), NotFound, Forbidden, Unauthorized, a
// decode error and the caller's own cancellation are not.
func apiServerUnreachable(err error) bool {
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		return false
	case api.IsNotFound(err), apierrors.IsNotFound(err), apierrors.IsForbidden(err), apierrors.IsUnauthorized(err), apierrors.IsTooManyRequests(err):
		return false
	case apierrors.IsServiceUnavailable(err), apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		return true
	case errors.Is(err, context.DeadlineExceeded):
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// lastObservedListingWhileUnreachable returns the last observed definitions
// of every server when err says the API server cannot be reached and the
// oldest of them is within the maximum age, so a listing reports what a call
// would be decided by. ok is false when the listing failed otherwise, muster
// observed no server, or a definition is too old (the listing reports the
// settings unavailable, which a call refuses).
func lastObservedListingWhileUnreachable(ctx context.Context, err error) (infos []api.MCPServerInfo, ok bool) {
	if !apiServerUnreachable(err) {
		return nil, false
	}
	infos, oldest, ok := api.LastObservedMCPServers()
	if !ok {
		return nil, false
	}
	age := clock.Now().Sub(oldest)
	if limit := maxLastObservedAge(); age > limit {
		logging.WarnWithAttrs("Approval", "API server unreachable and the servers' last observed approval settings are too old: listing them as unavailable",
			slog.String("reason", reasonTooOld), slog.Int("servers", len(infos)),
			slog.Duration("oldestAge", age.Round(time.Second)), slog.Duration("maxAge", limit),
			slog.String("error", err.Error()))
		countRefusal(ctx, "", reasonTooOld)
		return nil, false
	}
	logging.WarnWithAttrs("Approval", "API server unreachable: listing by the servers' last observed approval settings",
		slog.String("reason", reasonUnreachable), slog.Int("servers", len(infos)),
		slog.Duration("oldestAge", age.Round(time.Millisecond)),
		slog.String("error", err.Error()))
	return infos, true
}
