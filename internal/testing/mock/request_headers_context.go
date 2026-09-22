package mock

import (
	"context"
	"net/http"
)

// requestHeadersKey carries the HTTP request headers a mock backend was called
// with from the HTTP layer down to the tool handler, so an echo_headers tool
// can report the ones it is asked about.
type requestHeadersKey struct{}

// withRequestHeaders is the server's HTTPContextFunc: it stashes a copy of the
// request headers on the context every handler of that request sees.
func withRequestHeaders(ctx context.Context, r *http.Request) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, requestHeadersKey{}, r.Header.Clone())
}

// requestHeadersFrom returns the headers stashed by withRequestHeaders, or nil.
func requestHeadersFrom(ctx context.Context) http.Header {
	headers, _ := ctx.Value(requestHeadersKey{}).(http.Header)
	return headers
}
