package mock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
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

// unverifiedClaims decodes the payload of a JWT-shaped header value without
// checking its signature, or returns nil for any other value. An echo_headers
// tool reports them so a scenario can say whose token a header carried (its
// sub and iss) without matching the raw token; what the backend trusts is
// decided elsewhere.
func unverifiedClaims(value string) *tokenClaims {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return &claims
}
