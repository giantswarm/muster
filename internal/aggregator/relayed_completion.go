package aggregator

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// The completion of a backend URL elicitation muster relayed
// (backend_elicitation.go): the backend's notifications/elicitation/complete
// is passed on to the 2025-11-25 transport sessions that received the
// elicitation, and ends the wait of a 2026-07-28 retry that accepted it.

// observeRelayedCompletions passes the server's
// notifications/elicitation/complete, received on the client, to the
// elicitations muster relayed from the server (completeRelayedElicitation):
// those of the session when the client is the session's own connection, and
// those of every session for a shared client (sessionID "").
func (a *AggregatorServer) observeRelayedCompletions(serverName, sessionID string, client MCPClient) {
	if a == nil {
		return
	}
	if observer, ok := client.(internalmcp.ElicitationCompleteObserver); ok {
		observer.OnElicitationComplete(func(elicitationID string) {
			a.completeRelayedElicitation(serverName, sessionID, elicitationID)
		})
	}
}

// completeRelayedElicitation handles the server's completion of a URL
// elicitation, for the session or, with sessionID "", for every session it
// was relayed to: the transport sessions that received it as -32042 are sent
// notifications/elicitation/complete with the same elicitationId, and the
// 2026-07-28 retries waiting for it run. A completion of an elicitation muster
// did not relay, or of one that expired, does nothing.
func (a *AggregatorServer) completeRelayedElicitation(serverName, sessionID, elicitationID string) {
	transportIDs, relayed := a.relayedCompletions.complete(serverName, sessionID, elicitationID)
	if !relayed {
		return
	}
	logging.InfoWithAttrs("Aggregator", "A backend completed a relayed URL elicitation",
		slog.String("server", serverName))
	if a.mcpServer == nil {
		return
	}
	params := map[string]any{"elicitationId": elicitationID}
	for _, transportID := range transportIDs {
		if err := a.mcpServer.SendNotificationToSpecificClient(transportID, string(mcp.MethodNotificationElicitationComplete), params); err != nil {
			logging.Debug("Aggregator", "elicitation complete to session %s failed: %v",
				logging.TruncateIdentifier(transportID), err)
		}
	}
}

// relayedRetryWait is how long a 2026-07-28 retry that accepted a relayed
// elicitation waits for the backend's completion of it before it runs the
// call again. The client retries as soon as the person agreed to open the
// page, before they completed it. It is the sign-in's wait (signInRetryWait).
const relayedRetryWait = 5 * time.Minute

// relayedElicitationTTL is how long muster keeps a relayed elicitation for
// its completion, from the last time it relayed it.
const relayedElicitationTTL = 10 * time.Minute

// awaitRelayedCompletion waits, at most relayedRetryWait and while ctx
// lasts, until the backend completed every elicitation of the retry's
// requestState that this replica relayed to the session. It reports whether
// the wait ran out with an elicitation still not completed. A retry whose
// elicitations this replica did not relay -- on another replica, after a
// restart, or expired -- waits for nothing, as does a request without a
// session.
func (a *AggregatorServer) awaitRelayedCompletion(ctx context.Context, sessionID string, state relayedRequestState) (expired bool) {
	if sessionID == "" {
		return false
	}
	var pending []<-chan struct{}
	for _, elicitationID := range state.ElicitationIDs {
		if done := a.relayedCompletions.done(state.Relayed, sessionID, elicitationID); done != nil {
			pending = append(pending, done)
		}
	}
	if len(pending) == 0 {
		return false
	}
	wait, cancel := context.WithTimeout(ctx, relayedRetryWait)
	defer cancel()
	for _, done := range pending {
		select {
		case <-done:
		case <-wait.Done():
			return ctx.Err() == nil
		}
	}
	return false
}

// relayedCompletions remembers the backend URL elicitations muster relayed,
// per server, session and the backend's elicitationId, until the backend
// completes them or relayedElicitationTTL passes: the transport sessions that
// received each as -32042, and a channel closed on its completion.
//
// The zero value is ready to use. It is local to the replica, as the backend
// connection the completion arrives on is.
type relayedCompletions struct {
	mu      sync.Mutex
	entries map[relayedCompletionKey]*relayedCompletion
}

type relayedCompletionKey struct {
	server, sessionID, elicitationID string
}

type relayedCompletion struct {
	transportIDs []string
	completed    chan struct{}
	expiresAt    time.Time
}

func (c *relayedCompletion) isCompleted() bool {
	select {
	case <-c.completed:
		return true
	default:
		return false
	}
}

// add records that the server's elicitation was relayed to the session,
// through the transport session when it is not "" (-32042). An elicitation
// relayed again after its completion is a new one to complete. Nothing is
// recorded for a request without a session.
func (r *relayedCompletions) add(server, sessionID, elicitationID, transportID string) {
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for key, entry := range r.entries {
		if !now.Before(entry.expiresAt) {
			delete(r.entries, key)
		}
	}
	key := relayedCompletionKey{server: server, sessionID: sessionID, elicitationID: elicitationID}
	entry, ok := r.entries[key]
	if !ok || entry.isCompleted() {
		entry = &relayedCompletion{completed: make(chan struct{})}
		if r.entries == nil {
			r.entries = make(map[relayedCompletionKey]*relayedCompletion)
		}
		r.entries[key] = entry
	}
	entry.expiresAt = now.Add(relayedElicitationTTL)
	if transportID != "" && !slices.Contains(entry.transportIDs, transportID) {
		entry.transportIDs = append(entry.transportIDs, transportID)
	}
}

// complete marks the server's elicitation completed for the session, or
// for every session it was relayed to when sessionID is "", and returns the
// transport sessions to tell, each once, and whether muster relayed it at
// all. A server may give several sessions' elicitations the same id: a
// session's own connection completes only that session's.
func (r *relayedCompletions) complete(server, sessionID, elicitationID string) (transportIDs []string, relayed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for key, entry := range r.entries {
		if key.server != server || key.elicitationID != elicitationID || (sessionID != "" && key.sessionID != sessionID) ||
			!now.Before(entry.expiresAt) || entry.isCompleted() {
			continue
		}
		relayed = true
		close(entry.completed)
		transportIDs = append(transportIDs, entry.transportIDs...)
		entry.transportIDs = nil
	}
	return transportIDs, relayed
}

// done returns the channel closed when the backend completes the server's
// elicitation relayed to the session, or nil when this replica did not
// relay it or it expired.
func (r *relayedCompletions) done(server, sessionID, elicitationID string) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[relayedCompletionKey{server: server, sessionID: sessionID, elicitationID: elicitationID}]
	if !ok || !time.Now().Before(entry.expiresAt) {
		return nil
	}
	return entry.completed
}
