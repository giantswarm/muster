package aggregator

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/giantswarm/muster/internal/toolset"
)

// requestStartKey carries the time a protocol request reached the transport,
// so the hooks that log the response can report how long muster took --
// without a duration on the line, the request and response lines had to be
// subtracted by hand (#1225).
type requestStartKey struct{}

// withRequestStart stamps ctx with the current time. The transports call it
// per request (per POST for the HTTP transports, per message on stdio).
func withRequestStart(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestStartKey{}, time.Now())
}

// requestDuration returns how long ago the request reached the transport;
// false when ctx carries no start (a context built outside the transports).
func requestDuration(ctx context.Context) (time.Duration, bool) {
	start, ok := ctx.Value(requestStartKey{}).(time.Time)
	if !ok {
		return 0, false
	}
	return time.Since(start), true
}

// withRequestDuration appends duration_s -- how long since the request reached
// the transport, in seconds like the tool-call middleware's line -- to attrs
// when the context carries a start.
func withRequestDuration(ctx context.Context, attrs []slog.Attr) []slog.Attr {
	if d, ok := requestDuration(ctx); ok {
		attrs = append(attrs, slog.Float64("duration_s", d.Seconds()))
	}
	return attrs
}

// httpRequestContext is the per-request context func of the HTTP transports:
// the request's toolset source (X-Muster-Toolset) and its arrival time.
func httpRequestContext(ctx context.Context, r *http.Request) context.Context {
	return withRequestStart(toolset.HTTPContextFunc(ctx, r))
}
