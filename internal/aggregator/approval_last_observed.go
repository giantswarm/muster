package aggregator

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
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
// While the API server cannot be reached, nobody can change those settings,
// so the definition muster last read of the server (its reconciler reads
// every server on each watch event and resync) is still the server's, and
// the call is decided by it: the servers muster runs keep working through
// an outage. Only an unreachable API server falls back. A server muster
// never read, or last read as deleted, is still refused, and so is every
// other failure: NotFound (deleted), Forbidden or Unauthorized (muster may
// not read it), a definition that cannot be decoded.

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

// lastObservedWhileUnreachable returns the last observed definition of
// server when err says the API server cannot be reached, nil when the read
// failed otherwise or muster never observed the server.
func lastObservedWhileUnreachable(ctx context.Context, server string, err error) *api.MCPServerInfo {
	if !apiServerUnreachable(err) {
		return nil
	}
	info, observedAt, ok := api.LastObservedMCPServer(server)
	if !ok || info == nil {
		logging.WarnWithAttrs("Approval", "API server unreachable and no observed definition of the server: refusing the call",
			slog.String("server", server), slog.String("error", err.Error()))
		return nil
	}
	age := clock.Now().Sub(observedAt).Round(time.Millisecond)
	logging.WarnWithAttrs("Approval", "API server unreachable: deciding the call by the server's last observed approval settings",
		slog.String("server", server),
		slog.String("observedAt", observedAt.UTC().Format(time.RFC3339)),
		slog.Duration("age", age),
		slog.String("error", err.Error()))
	if counter := lastObservedFallbacks(); counter != nil {
		counter.Add(ctx, 1, metric.WithAttributes(attribute.String(attrDownstreamServer, server)))
	}
	return info
}

// apiServerUnreachable reports whether err says the API server could not be
// reached or could not answer: a transport error, a timeout, or an
// unavailable or throttling answer. NotFound, Forbidden, Unauthorized, a
// decode error and the caller's own cancellation are not.
func apiServerUnreachable(err error) bool {
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		return false
	case api.IsNotFound(err), apierrors.IsNotFound(err), apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return false
	case apierrors.IsServiceUnavailable(err), apierrors.IsTimeout(err), apierrors.IsServerTimeout(err), apierrors.IsTooManyRequests(err):
		return true
	case errors.Is(err, context.DeadlineExceeded):
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
