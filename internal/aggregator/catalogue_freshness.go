package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
	"github.com/giantswarm/muster/v5/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// Catalogue freshness after a backend redeploy (issue #1440).
//
// A per-session server's tools are cached per session in the capability
// store, listed when the session connected, and re-listed afterwards only
// through a live pooled connection: by the capability poll, by a list_changed
// notification, or by the poll's listing recovering a session the backend had
// forgotten. A session that holds no pooled connection -- the pool is in
// memory and emptied by a muster restart, an hour idle, a token failure --
// keeps its entry (in Valkey, for 30 days) without anything re-listing it. A
// backend redeployed with another tool schema is then described from the
// old process while the next call reaches the new one: the catalogue says
// the arguments are fine and the backend refuses them.
//
// muster sees a redeploy without watching the backend's pods, through the
// protocol: the new process does not know the sessions the old one issued,
// so the next request on any connection to it meets a 404 and the client
// re-establishes the session (internal/mcpserver, session recovery). That is
// the sign a new process answers behind the address, for in-cluster and
// off-cluster backends alike, and the one this file acts on. Three more signs
// stand in where no session is lost: a listing that answers differently
// from the entry before it (a stateless backend has no session to lose), a
// call the backend refuses as invalid params (the caller followed a stale
// schema), and a pool miss on a call (the connection is new, the entry is
// not).
//
// Every sign marks the server rolled (ServerInfo.MarkRolled), re-lists the
// connection it arrived on, and leaves every other session's entry to its
// next read: a session whose entry predates the mark is re-listed, through
// its pooled connection or the one a call would open, before its catalogue
// is served (freshenSessionView). So describe_tool and filter_tools follow a
// redeploy as soon as any connection to the backend has met it, the stale
// entries of the other sessions cost one listing each, and nothing is listed
// for a session that never reads.

// sessionRelistTimeout bounds the re-listing done on a session's read of its
// catalogue: a backend that does not answer must not hold the read for its
// whole spec.timeout, and the entry stays served meanwhile.
const sessionRelistTimeout = 15 * time.Second

// sessionRecoveryObserver is implemented by the clients whose backend keeps an
// MCP session: the streamable-http transports of internal/mcpserver.
type sessionRecoveryObserver interface {
	OnSessionRecovered(func())
}

// observeSharedRecovery re-lists a shared-client server whenever its
// connection recovers a lost MCP session: the health probe meets the new
// process within one probe interval, so the shared catalogue follows a
// redeploy in well under a minute. The handler runs on a goroutine of the
// client's, so the re-listing is done in it, coalesced with any other in
// flight.
func (a *AggregatorServer) observeSharedRecovery(serverName string, client MCPClient) {
	observer, ok := client.(sessionRecoveryObserver)
	if !ok {
		return
	}
	observer.OnSessionRecovered(func() {
		logging.Info("Aggregator", "Backend of %s changed (its connection recovered a lost MCP session): re-listing", serverName)
		_, _, _ = a.notifRefreshGroup.Do(nonOAuthRefreshKey(serverName), func() (any, error) {
			a.refreshNonOAuthCapabilities(serverName, refreshByRecovery)
			return nil, nil
		})
	})
}

// observeSessionRecovery marks a per-session server rolled whenever one of
// its pooled connections recovers a lost MCP session, and re-lists that
// session through the recovered connection, in the handler like
// observeSharedRecovery; every other session follows on its next read.
func (a *AggregatorServer) observeSessionRecovery(serverName, sessionID string, client MCPClient) {
	observer, ok := client.(sessionRecoveryObserver)
	if !ok {
		return
	}
	observer.OnSessionRecovered(func() {
		if info, ok := a.registry.GetServerInfo(serverName); ok && info.MarkRolled() {
			logging.Info("Aggregator", "Backend of %s changed (session %s recovered a lost MCP session): entries listed before are re-listed on their next read",
				serverName, logging.TruncateIdentifier(sessionID))
		}
		if a.connPool == nil || !a.connPool.Holds(sessionID, serverName, client) {
			return
		}
		_, _, _ = a.notifRefreshGroup.Do(sessionRefreshKey(sessionID, serverName), func() (any, error) {
			a.relistSession(serverName, sessionID, client, refreshByRecovery, 0)
			return nil, nil
		})
	})
}

// sessionView reads what every server offered the session, in one store
// read, and brings the entries that predate a change seen on their server up
// to date before they are served.
func (a *AggregatorServer) sessionView(ctx context.Context, sessionID string) map[string]*oauthstore.Capabilities {
	caps := sessionCapabilities(ctx, a.capabilityStore, sessionID)
	a.freshenSessionView(ctx, sessionID, caps)
	return caps
}

// freshenSessionView re-lists, in place, every entry of caps that predates
// the change last seen on its server, at most capabilityPollConcurrency at a
// time. An entry whose server is down is left alone: it contributes nothing
// anyway. A listing that fails leaves the entry served as it is.
func (a *AggregatorServer) freshenSessionView(ctx context.Context, sessionID string, caps map[string]*oauthstore.Capabilities) {
	if a.capabilityStore == nil || len(caps) == 0 {
		return
	}
	var stale []string
	for serverName, cached := range caps {
		info, ok := a.registry.GetServerInfo(serverName)
		if !ok || cached == nil || !info.RequiresSessionAuth() || info.IsDown() || !info.PredatesRoll(cached.ListedAt) {
			continue
		}
		stale = append(stale, serverName)
	}
	if len(stale) == 0 {
		return
	}
	sort.Strings(stale)

	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, capabilityPollConcurrency)
	for _, serverName := range stale {
		wg.Add(1)
		slots <- struct{}{}
		go func(serverName string) {
			defer wg.Done()
			defer func() { <-slots }()
			if fresh := a.relistForRead(ctx, sessionID, serverName); fresh != nil {
				mu.Lock()
				caps[serverName] = fresh
				mu.Unlock()
			}
		}(serverName)
	}
	wg.Wait()
}

// relistForRead re-lists one session's stale entry through the session's
// connection -- the pooled one, else the one a tool call would open -- and
// returns the entry now stored. The listing is coalesced with any other
// re-listing of the pair in flight. When no listing can be made, the entry
// is stamped as judged against the change it predates and returned as nil:
// it is served as it is, and the next connection the session opens, or the
// poll through the pooled one, lists it, instead of every read trying.
func (a *AggregatorServer) relistForRead(ctx context.Context, sessionID, serverName string) *oauthstore.Capabilities {
	ctx, cancel := context.WithTimeout(ctx, sessionRelistTimeout)
	defer cancel()

	_, err, _ := a.notifRefreshGroup.Do(sessionRefreshKey(sessionID, serverName), func() (any, error) {
		client, err := a.sessionClientForRead(ctx, serverName, sessionID)
		if err != nil {
			return nil, err
		}
		return nil, a.refreshSessionCapabilities(ctx, serverName, sessionID, client, refreshByRead)
	})
	if err != nil {
		logging.Warn("Aggregator", "Session capability refresh (read): %s could not be re-listed for session %s, its cached entry is served: %v",
			serverName, logging.TruncateIdentifier(sessionID), err)
		a.stampJudged(ctx, sessionID, serverName)
		return nil
	}
	fresh, err := a.capabilityStore.Get(ctx, sessionID, serverName)
	if err != nil {
		return nil
	}
	return fresh
}

// sessionClientForRead returns the session's connection to a per-session
// server for a re-listing: the pooled one, else the one a tool call opens
// (getOrCreateClientForToolCall), which pools it.
func (a *AggregatorServer) sessionClientForRead(ctx context.Context, serverName, sessionID string) (MCPClient, error) {
	if a.connPool != nil {
		if client, ok := a.connPool.Get(sessionID, serverName); ok {
			return client, nil
		}
	}
	client, cleanup, err := a.getOrCreateClientForToolCall(ctx, serverName, sessionID, getUserSubjectFromContext(ctx))
	if err != nil {
		return nil, err
	}
	cleanup()
	return client, nil
}

// stampJudged records on a session's entry that it was judged against the
// change its server last saw, so the next read serves it without listing
// again; its content is kept.
func (a *AggregatorServer) stampJudged(ctx context.Context, sessionID, serverName string) {
	info, ok := a.registry.GetServerInfo(serverName)
	if !ok {
		return
	}
	cached, err := a.capabilityStore.Get(ctx, sessionID, serverName)
	if err != nil || cached == nil {
		return
	}
	cached.ListedAt = info.RolledAt()
	if err := a.capabilityStore.Set(ctx, sessionID, serverName, cached); err != nil {
		logging.Debug("Aggregator", "Could not stamp the entry of %s for session %s: %v",
			serverName, logging.TruncateIdentifier(sessionID), err)
	}
}

// relistOnConnect re-lists a per-session server through the connection a
// tool call just opened for the session, when the session already holds an
// entry for the server: the entry came from an earlier connection, and the
// backend behind this one may be another process. A session without an
// entry is listed by the connect that gives it one.
func (a *AggregatorServer) relistOnConnect(ctx context.Context, serverName, sessionID string, client MCPClient) {
	if a.capabilityStore == nil {
		return
	}
	cached, err := a.capabilityStore.Get(ctx, sessionID, serverName)
	if err != nil || cached == nil {
		return
	}
	a.refreshSessionServer(serverName, sessionID, client, refreshByConnect)
}

// invalidParams reports whether a backend refused a call's arguments as
// invalid (JSON-RPC -32602), the answer a caller gets for following a schema
// the backend no longer serves.
func invalidParams(err error) bool {
	return errors.Is(err, mcp.ErrInvalidParams)
}

// refusedSessionArguments answers a per-session server's invalid-params
// refusal of a call: the session's entry is re-listed through the connection
// the call used, so the catalogue tells the truth from now on, and when the
// called tool's schema turns out to have changed, the refusal says so.
func (a *AggregatorServer) refusedSessionArguments(ctx context.Context, serverName, sessionID, toolName string, client MCPClient, refusal error) error {
	if a.capabilityStore == nil {
		return refusal
	}
	before, _ := a.capabilityStore.Get(ctx, sessionID, serverName)
	_, _, _ = a.notifRefreshGroup.Do(sessionRefreshKey(sessionID, serverName), func() (any, error) {
		return nil, a.refreshSessionCapabilities(ctx, serverName, sessionID, client, refreshByRefusal)
	})
	after, _ := a.capabilityStore.Get(ctx, sessionID, serverName)
	if before == nil || after == nil {
		return refusal
	}
	return withSchemaChangedHint(refusal, serverName, toolName, before.Tools, after.Tools)
}

// refusedSharedArguments is refusedSessionArguments for a shared-client
// server: the server's catalogue is re-listed through its client.
func (a *AggregatorServer) refusedSharedArguments(serverName, toolName string, refusal error) error {
	info, ok := a.registry.GetServerInfo(serverName)
	if !ok {
		return refusal
	}
	info.mu.RLock()
	before := append([]mcp.Tool(nil), info.Tools...)
	info.mu.RUnlock()
	_, _, _ = a.notifRefreshGroup.Do(nonOAuthRefreshKey(serverName), func() (any, error) {
		a.refreshNonOAuthCapabilities(serverName, refreshByRefusal)
		return nil, nil
	})
	info.mu.RLock()
	after := append([]mcp.Tool(nil), info.Tools...)
	info.mu.RUnlock()
	return withSchemaChangedHint(refusal, serverName, toolName, before, after)
}

// withSchemaChangedHint returns the refusal as it is when the called tool's
// input schema is the same after the re-listing as before it, and the
// refusal with a hint to describe the tool again when it changed. The tool
// is named as the backend knows it, with its server.
func withSchemaChangedHint(refusal error, serverName, toolName string, before, after []mcp.Tool) error {
	was, now := toolSchema(before, toolName), toolSchema(after, toolName)
	if was == "" || now == "" || was == now {
		return refusal
	}
	return fmt.Errorf("%w; the input schema of %s on %s changed since it was described (the backend was redeployed): describe_tool shows the current one",
		refusal, toolName, serverName)
}

// toolSchema returns the JSON of the named tool's input schema, or "" when
// the list does not carry the tool.
func toolSchema(tools []mcp.Tool, name string) string {
	for _, tool := range tools {
		if tool.Name == name {
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				return ""
			}
			return string(schema)
		}
	}
	return ""
}
