package mock

import (
	"net/http"
	"sync"
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
}

// set arms the gate: the next requests requests are answered with status. A
// requests of 0 disarms it.
func (g *outageGate) set(status, requests int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.status = status
	g.remaining = requests
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

// wrap returns next guarded by the gate. A session termination (HTTP DELETE)
// passes through: it is the client tidying up the session it had before the
// outage, not a connection attempt, and muster's client sends it when the
// service restarts. Counting it would make an outage of N requests fail N-1
// attempts.
func (g *outageGate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			if status := g.take(); status != 0 {
				http.Error(w, http.StatusText(status), status)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// SetOutage makes the server answer its next requests requests with the given
// HTTP status and serve normally afterwards; requests of 0 ends an outage
// early. The server keeps listening, so muster sees an HTTP response, not a
// refused connection.
func (s *HTTPServer) SetOutage(status, requests int) {
	s.outage.set(status, requests)
}

// OutageRemaining reports how many requests the current outage still covers.
func (s *HTTPServer) OutageRemaining() int {
	return s.outage.remainingRequests()
}

// SetOutage makes the server answer its next requests requests with the given
// HTTP status -- before the OAuth middleware, so no 401 challenge is sent --
// and serve normally afterwards; requests of 0 ends an outage early.
func (s *ProtectedMCPServer) SetOutage(status, requests int) {
	s.outage.set(status, requests)
}

// OutageRemaining reports how many requests the current outage still covers.
func (s *ProtectedMCPServer) OutageRemaining() int {
	return s.outage.remainingRequests()
}
