package mcpserver

import (
	"context"
	"maps"

	"github.com/mark3labs/mcp-go/client/transport"
)

// Per-call headers: muster's own request headers for one call, set by the
// aggregator on the call's context (X-Muster-Caller-Kind for an agent
// caller's call to a governed server). Every HTTP-based client (streamable
// HTTP, SSE, dynamic auth, SigV4) adds them to each request it sends under
// that context, whichever connection carries the call: shared, per session,
// token exchange or forwarding. A client's own headers never reach this
// context, so a client cannot set them.

type callHeadersKey struct{}

// WithCallHeaders returns ctx carrying headers for the requests of the call
// made under it.
func WithCallHeaders(ctx context.Context, headers map[string]string) context.Context {
	merged := maps.Clone(callHeaders(ctx))
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, headers)
	return context.WithValue(ctx, callHeadersKey{}, merged)
}

func callHeaders(ctx context.Context) map[string]string {
	headers, _ := ctx.Value(callHeadersKey{}).(map[string]string)
	return headers
}

// withCallHeaders returns a header function adding the call's headers to
// what base returns; the call's headers win.
func withCallHeaders(base transport.HTTPHeaderFunc) transport.HTTPHeaderFunc {
	return func(ctx context.Context) map[string]string {
		var out map[string]string
		if base != nil {
			out = maps.Clone(base(ctx))
		}
		if extra := callHeaders(ctx); len(extra) > 0 {
			if out == nil {
				out = make(map[string]string, len(extra))
			}
			maps.Copy(out, extra)
		}
		return out
	}
}

// CallHeadersForTest returns the per-call headers ctx carries.
func CallHeadersForTest(ctx context.Context) map[string]string { return maps.Clone(callHeaders(ctx)) }
