package mcpserver

import (
	"net/http"
	"strings"
)

// DefinitionHeaders is the part of an MCPServer's spec.headers a session-scoped
// client sends: every entry but Authorization, in a fresh map. The session's
// credential -- the person's grant, their forwarded login token, a token
// exchanged for it -- is the Authorization header of that connection, and a
// definition never names it for a session. The server-level client the client
// factory builds keeps the whole set: for it a static Authorization entry is
// the documented way to reach a server with one shared token.
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
