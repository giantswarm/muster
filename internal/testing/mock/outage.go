package mock

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

// outageGate makes a mock server answer a number of requests with a fixed HTTP
// status before serving normally again -- the shape of a gateway in front of a
// healthy server timing out (504) for a while, as opposed to the server being
// gone (connection refused, see HTTPServer.Stop). The gate sits in front of
// the whole handler chain, so an OAuth-protected server under an outage
// answers the status instead of its 401 challenge, exactly as a proxy that
// never reached it would.
type outageGate struct {
	mu        sync.Mutex
	status    int
	remaining int
	// pings makes an armed gate answer MCP pings too. By default they pass
	// through uncounted, see gates.
	pings bool
}

// set arms the gate: the next requests requests are answered with status. A
// requests of 0 disarms it. With pings, the orchestrator's health probes are
// answered with the status as well, the way a gateway that fails every
// request would answer them; without it they pass through, so an outage of N
// requests fails exactly N connection attempts whatever the probe interval.
func (g *outageGate) set(status, requests int, pings bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.status = status
	g.remaining = requests
	g.pings = pings
}

// take consumes one request from the gate and reports the status to answer
// with, or 0 when the gate is not armed.
func (g *outageGate) take() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.remaining <= 0 {
		return 0
	}
	g.remaining--
	return g.status
}

// remainingRequests reports how many requests the gate will still answer.
func (g *outageGate) remainingRequests() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.remaining
}

// gates reports whether an armed gate answers r. Two requests are never
// connection attempts, and counting either would make an outage of N
// requests fail N-1 attempts: a session termination (HTTP DELETE), which is
// the client tidying up the session it had before the outage and which
// muster's client sends when the service restarts; and, unless the gate was
// armed with pings, an MCP ping, which the orchestrator's health probe sends
// to every connected server on its interval (1s in the harness) and which
// would otherwise land in the outage window of any scenario that arms the
// gate on a Connected server. The body is inspected only while the gate is
// armed.
func (g *outageGate) gates(r *http.Request) bool {
	if r.Method == http.MethodDelete {
		return false
	}
	g.mu.Lock()
	armed, pings := g.remaining > 0, g.pings
	g.mu.Unlock()
	if !armed {
		return false
	}
	return pings || !isMCPPing(r)
}

// wrap returns next guarded by the gate.
func (g *outageGate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.gates(r) {
			if status := g.take(); status != 0 {
				http.Error(w, http.StatusText(status), status)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isMCPPing reports whether r carries a JSON-RPC ping. The body is read and
// put back so the handler behind the gate still sees it.
func isMCPPing(r *http.Request) bool {
	if r.Method != http.MethodPost || r.Body == nil {
		return false
	}
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return false
	}
	var rpc struct {
		Method string `json:"method"`
	}
	return json.Unmarshal(body, &rpc) == nil && rpc.Method == string(mcp.MethodPing)
}

// SetOutage makes the server answer its next requests requests with the given
// HTTP status and serve normally afterwards; requests of 0 ends an outage
// early. The server keeps listening, so muster sees an HTTP response, not a
// refused connection. With pings the orchestrator's health probes are answered
// with the status too; without, they pass through uncounted.
func (s *HTTPServer) SetOutage(status, requests int, pings bool) {
	s.outage.set(status, requests, pings)
}

// OutageRemaining reports how many requests the current outage still covers.
func (s *HTTPServer) OutageRemaining() int {
	return s.outage.remainingRequests()
}

// SetOutage makes the server answer its next requests requests with the given
// HTTP status -- before the OAuth middleware, so no 401 challenge is sent --
// and serve normally afterwards; requests of 0 ends an outage early. With
// pings the orchestrator's health probes are answered with the status too.
func (s *ProtectedMCPServer) SetOutage(status, requests int, pings bool) {
	s.outage.set(status, requests, pings)
}

// OutageRemaining reports how many requests the current outage still covers.
func (s *ProtectedMCPServer) OutageRemaining() int {
	return s.outage.remainingRequests()
}
