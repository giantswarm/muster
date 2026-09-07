package toolset

import (
	"context"
	"net/http"
)

// Source is where a request's toolset came from, before parsing. Present is
// false when the request declared no toolset at all (today's unscoped
// behaviour); a present but empty value is a declared, invalid toolset.
type Source struct {
	Present bool
	Raw     string
}

// SourceFromRequest is the single seam that decides which request attribute
// carries the toolset. Today that is the X-Muster-Toolset header; an actor
// claim from a future agent identity would be read here too, and nothing
// downstream (parsing, resolution, refusal, scenarios) would change.
func SourceFromRequest(r *http.Request) Source {
	values, ok := r.Header[http.CanonicalHeaderKey(HeaderName)]
	if !ok {
		return Source{}
	}
	raw := ""
	if len(values) > 0 {
		raw = values[0]
	}
	return Source{Present: true, Raw: raw}
}

type sourceContextKey struct{}

// ContextWithSource stores the request's toolset source in ctx.
func ContextWithSource(ctx context.Context, src Source) context.Context {
	return context.WithValue(ctx, sourceContextKey{}, src)
}

// SourceFromContext returns the toolset source stored by ContextWithSource.
func SourceFromContext(ctx context.Context) Source {
	src, _ := ctx.Value(sourceContextKey{}).(Source)
	return src
}

// HTTPContextFunc has the shape of mcp-go's server.HTTPContextFunc /
// SSEContextFunc: it stashes the request's toolset source in the context every
// tool handler receives. Evaluation is per request by construction — nothing
// is bound to the session.
func HTTPContextFunc(ctx context.Context, r *http.Request) context.Context {
	return ContextWithSource(ctx, SourceFromRequest(r))
}

// FromContext parses the toolset the request declared. present is false when
// the request carried none. A present toolset that fails to parse returns the
// error, which callers surface on every meta-tool call.
func FromContext(ctx context.Context) (ts Toolset, present bool, err error) {
	src := SourceFromContext(ctx)
	if !src.Present {
		return Toolset{}, false, nil
	}
	ts, err = ParseHeader(src.Raw)
	return ts, true, err
}
