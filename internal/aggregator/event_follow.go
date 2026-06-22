package aggregator

import (
	"context"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// eventFollowNotificationMethod is the JSON-RPC notification method the
// aggregator uses to push a single new event to a following client. The
// `muster events --follow` CLI listens for this method.
const eventFollowNotificationMethod = "notifications/muster/event"

// eventFollow holds the cancel function for one active server-pushed event
// stream. A pointer identity lets goroutines remove only their own entry.
type eventFollow struct {
	cancel context.CancelFunc
}

// startEventFollow begins (or restarts) a server-pushed event stream for the
// MCP session in ctx. The caller returns the events seen so far synchronously;
// this method pushes every subsequent matching event to the same client as an
// MCP notification, sourced from a real watch (Kubernetes watch or filesystem
// fsnotify) — there is no timer polling.
//
// The stream lives until the session disconnects (OnUnregisterSession hook),
// the client starts a new follow (which restarts it), the aggregator shuts
// down, or the client can no longer be reached.
func (a *AggregatorServer) startEventFollow(ctx context.Context, options api.EventQueryOptions) {
	session := mcpserver.ClientSessionFromContext(ctx)
	if session == nil {
		// No client session to push to (e.g. an internal tool call); the
		// initial query result the caller already returned is all we can do.
		logging.Warn("Aggregator", "event follow requested but no client session in context; cannot stream")
		return
	}
	sessionID := session.SessionID()
	logging.Debug("Aggregator", "event follow requested for session %s", logging.TruncateIdentifier(sessionID))

	eventManager := api.GetEventManager()
	if eventManager == nil {
		return
	}

	// Replace any existing follow for this session.
	a.stopEventFollow(sessionID)

	streamCtx, cancel := context.WithCancel(a.refreshContext())
	ch, err := eventManager.WatchEvents(streamCtx, options)
	if err != nil {
		cancel()
		logging.Warn("Aggregator", "Failed to start event follow for session %s: %v",
			logging.TruncateIdentifier(sessionID), err)
		return
	}

	follow := &eventFollow{cancel: cancel}
	a.eventFollowsMu.Lock()
	a.eventFollows[sessionID] = follow
	a.eventFollowsMu.Unlock()

	logging.Debug("Aggregator", "Started event follow stream for session %s",
		logging.TruncateIdentifier(sessionID))

	go func() {
		defer a.removeEventFollow(sessionID, follow)
		for {
			select {
			case <-streamCtx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				params := map[string]any{
					"timestamp":     ev.Timestamp.Format("2006-01-02 15:04:05"),
					"resource_type": ev.InvolvedObject.Kind,
					"resource_name": ev.InvolvedObject.Name,
					"namespace":     ev.Namespace,
					"reason":        ev.Reason,
					"message":       ev.Message,
					"type":          ev.Type,
				}
				logging.Debug("Aggregator", "Pushing event %s/%s %s to session %s",
					ev.InvolvedObject.Kind, ev.InvolvedObject.Name, ev.Reason, logging.TruncateIdentifier(sessionID))
				if err := a.mcpServer.SendNotificationToSpecificClient(sessionID, eventFollowNotificationMethod, params); err != nil {
					// The client is gone or unreachable; stop streaming.
					logging.Debug("Aggregator", "Stopping event follow for session %s: %v",
						logging.TruncateIdentifier(sessionID), err)
					return
				}
			}
		}
	}()
}

// stopEventFollow cancels and removes any active follow stream for the session.
// Safe to call for sessions without an active follow.
func (a *AggregatorServer) stopEventFollow(sessionID string) {
	a.eventFollowsMu.Lock()
	follow, ok := a.eventFollows[sessionID]
	if ok {
		delete(a.eventFollows, sessionID)
	}
	a.eventFollowsMu.Unlock()
	if ok {
		follow.cancel()
	}
}

// removeEventFollow removes the map entry only if it still points to follow
// (i.e. it hasn't been replaced by a newer stream) and cancels it. Called by a
// stream goroutine on exit.
func (a *AggregatorServer) removeEventFollow(sessionID string, follow *eventFollow) {
	a.eventFollowsMu.Lock()
	if a.eventFollows[sessionID] == follow {
		delete(a.eventFollows, sessionID)
	}
	a.eventFollowsMu.Unlock()
	follow.cancel()
}

// eventFollowOptions extracts the streaming filter options from the core_events
// tool arguments. Only the simple string filters are honored for follow; time
// windows are irrelevant because a watch only ever surfaces new events.
func eventFollowOptions(args map[string]interface{}) api.EventQueryOptions {
	options := api.EventQueryOptions{}
	if v, ok := args["resourceType"].(string); ok {
		options.ResourceType = v
	}
	if v, ok := args["resourceName"].(string); ok {
		options.ResourceName = v
	}
	if v, ok := args["namespace"].(string); ok {
		options.Namespace = v
	}
	if v, ok := args["eventType"].(string); ok {
		options.EventType = v
	}
	return options
}
