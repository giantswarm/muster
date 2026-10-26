package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
)

// DefinitionHeaders is the part of an MCPServer's spec.headers a session-scoped
// client sends: every entry but Authorization, in a fresh map. The session's
// credential -- the person's grant, their forwarded login token, a token
// exchanged for it -- is the Authorization header of that connection, and a
// definition never names it for a session. The server-level client the client
// factory builds keeps the whole set: for it an Authorization entry of
// spec.headersFrom is the documented way to reach a server with one shared
// token.
//
// The result is nil when nothing is left, so callers can test it with len.
func DefinitionHeaders(headers map[string]string) map[string]string {
	var out map[string]string
	for k, v := range headers {
		if strings.EqualFold(k, "Authorization") {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(headers))
		}
		out[http.CanonicalHeaderKey(k)] = v
	}
	return out
}

// ResolveHeaders returns the headers a connection to a server sends: its
// spec.headers, and each spec.headersFrom entry with its value read from the
// Secret it names in the server's namespace. An entry of headersFrom wins over
// a spec.headers entry of the same name, in any case.
//
// owner is the MCPServer the headers are for: a Secret is read only when it is
// bound to that MCPServer (api.SecretCredentialsHandler), and a refusal fails
// the whole call, so no header of a refused Secret is ever sent.
//
// muster calls it when it builds a connection's client, so a value lives in
// that client alone: never in the definition, the service data or the output
// of a core tool. The reference has no namespace, so the Secret is always in
// owner.Namespace, the server's own ("default" when empty, as in filesystem
// mode).
//
// headers is returned unchanged when headersFrom is empty.
func ResolveHeaders(ctx context.Context, owner api.SecretOwner, headers map[string]string, headersFrom []api.MCPServerHeaderFrom) (map[string]string, error) {
	if len(headersFrom) == 0 {
		return headers, nil
	}
	secrets := api.GetSecretCredentialsHandler()
	if secrets == nil {
		return nil, fmt.Errorf("headersFrom needs a Secret reader, which only Kubernetes mode has")
	}
	out := make(map[string]string, len(headers)+len(headersFrom))
	for k, v := range headers {
		out[k] = v
	}
	for _, h := range headersFrom {
		ref := &api.ClientCredentialsSecretRef{Name: h.ValueFrom.Name}
		value, err := secrets.LoadMCPServerSecretKey(ctx, owner, api.SecretFieldHeadersFrom, ref, h.ValueFrom.Key)
		if err != nil {
			return nil, &HeaderSecretError{Header: h.Name, Err: err}
		}
		for k := range out {
			if strings.EqualFold(k, h.Name) {
				delete(out, k)
			}
		}
		// A Secret written with a trailing newline (echo, a file from an
		// editor) would put it into the header value, which net/http refuses.
		v := string(value)
		if trimmed, ok := strings.CutSuffix(v, "\n"); ok {
			v = strings.TrimSuffix(trimmed, "\r")
		}
		out[h.Name] = v
	}
	return out, nil
}

// HeaderSecretError reports that the Secret of a spec.headersFrom entry could
// not be read: it does not exist, is not readable or lacks the key. GitOps
// applies an MCPServer and its Secret in any order, so the server is retried
// with backoff until the Secret exists.
type HeaderSecretError struct {
	// Header is the name of the header the Secret's value is for.
	Header string
	Err    error
}

func (e *HeaderSecretError) Error() string {
	// No "Secret:" in the text: the status sanitizer (reconciler.SanitizeErrorMessage)
	// redacts the word after it as a value.
	return fmt.Sprintf("header %s: its Secret could not be read (%v)", e.Header, e.Err)
}

func (e *HeaderSecretError) Unwrap() error { return e.Err }
