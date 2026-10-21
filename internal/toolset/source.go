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

// SourceFromRequest reads the toolset the request declares in the
// X-Muster-Toolset header. A request of a bound Session also carries the
// toolset of its Session binding (ContextWithBound); the header then only
// narrows it.
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

type boundContextKey struct{}

// ContextWithBound stores the toolset bound to the caller's Session, the
// selectors of its Session binding as stored. nil binds none (the request
// stays unscoped unless it declares a header).
func ContextWithBound(ctx context.Context, selectors []string) context.Context {
	if selectors == nil {
		return ctx
	}
	return context.WithValue(ctx, boundContextKey{}, selectors)
}

// BoundFromContext parses the toolset bound to the caller's Session. present
// is false when the Session binds none. A bound toolset that fails to parse
// returns the error, which callers surface on every meta-tool call.
func BoundFromContext(ctx context.Context) (ts Toolset, present bool, err error) {
	selectors, ok := ctx.Value(boundContextKey{}).([]string)
	if !ok {
		return Toolset{}, false, nil
	}
	ts, err = ParseInline(selectors)
	return ts, true, err
}

// Scoped reports whether the request is bounded by any toolset: one bound to
// its Session or one it declares.
func Scoped(ctx context.Context) bool {
	if _, ok := ctx.Value(boundContextKey{}).([]string); ok {
		return true
	}
	return SourceFromContext(ctx).Present
}

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
// tool handler receives. Evaluation is per request by construction: the
// header is never remembered on the MCP session.
func HTTPContextFunc(ctx context.Context, r *http.Request) context.Context {
	return ContextWithSource(ctx, SourceFromRequest(r))
}

// FromContext parses the toolset the request declared in its header, without
// the bound one (BoundFromContext). present is false when the request carried
// none. A present toolset that fails to parse returns the error, which
// callers surface on every meta-tool call.
func FromContext(ctx context.Context) (ts Toolset, present bool, err error) {
	src := SourceFromContext(ctx)
	if !src.Present {
		return Toolset{}, false, nil
	}
	ts, err = ParseHeader(src.Raw)
	return ts, true, err
}
