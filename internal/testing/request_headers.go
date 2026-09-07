package testing

import "context"

type requestHeadersKey struct{}

// WithRequestHeaders returns a context carrying HTTP headers the test client
// adds to every request it sends under that context. A step's headers are
// stashed this way so they ride on exactly that step's requests — same client,
// same MCP session, different headers per request.
func WithRequestHeaders(ctx context.Context, headers map[string]string) context.Context {
	if len(headers) == 0 {
		return ctx
	}
	return context.WithValue(ctx, requestHeadersKey{}, headers)
}

// requestHeadersFromContext is the client's transport.HTTPHeaderFunc: it
// returns the headers stashed by WithRequestHeaders, if any.
func requestHeadersFromContext(ctx context.Context) map[string]string {
	headers, _ := ctx.Value(requestHeadersKey{}).(map[string]string)
	return headers
}
