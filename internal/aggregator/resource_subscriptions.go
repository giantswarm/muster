package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Resource subscriptions are relayed per user.
//
// A client's subscriptions/listen (MCP 2026-07-28) naming resource URIs opens,
// for each backend serving some of them, a subscriptions/listen stream of the
// client's own: a connection of the session to that backend, made as the
// session (newSessionClient), never pooled or shared. The backend's
// notifications/resources/updated for a subscribed URI goes to that client's
// stream only, carrying the client's subscription ID and the URI as muster
// exposes it. When the client's stream ends, its backend streams end with it.
//
// Only the 2026-07-28 revision is relayed, at both ends: there is no legacy
// resources/subscribe path, so a legacy initialize is not told the server
// offers it (hideLegacySubscribe).

// resourceRelayRetry bounds the wait before a backend stream that ended while
// its client's stream is still open is opened again.
const (
	resourceRelayRetryMin = time.Second
	resourceRelayRetryMax = 30 * time.Second
)

// resourceListener is what a backend connection in listen mode offers.
type resourceListener interface {
	ListenResources(ctx context.Context, uris []string) error
}

// subscriptionsListenRequest is the part of a subscriptions/listen the relay
// reads.
type subscriptionsListenRequest struct {
	Method string                        `json:"method"`
	Params mcp.SubscriptionsListenParams `json:"params"`
}

// relaySubscriptionsListen is the OnRequestInitialization hook that sets up
// the relay of a subscriptions/listen before mcp-go serves it. A request it
// refuses -- an unknown URI, a backend the session may not reach, a backend
// that refuses the subscription -- is answered with the reason, and nothing
// stays open for it.
func (a *AggregatorServer) relaySubscriptionsListen(ctx context.Context, id any, message any) error {
	raw, ok := message.(json.RawMessage)
	if !ok || id == nil {
		return nil
	}
	var req subscriptionsListenRequest
	if json.Unmarshal(raw, &req) != nil || req.Method != string(mcp.MethodSubscriptionsListen) {
		return nil
	}
	uris := req.Params.Notifications.ResourceSubscriptions
	if len(uris) == 0 || !mcp.IsModernProtocol(req.Params.Meta.ProtocolVersion()) {
		// Nothing to relay, or a legacy request mcp-go refuses itself.
		return nil
	}
	session := mcpserver.ClientSessionFromContext(ctx)
	if session == nil {
		return errors.New("subscriptions/listen: no client session")
	}
	sessionID := getSessionIDFromContext(ctx)
	if sessionID == "" {
		return errors.New("subscriptions/listen: no authenticated session")
	}
	sub := getUserSubjectFromContext(ctx)

	plan, err := a.planResourceSubscriptions(ctx, sessionID, uris)
	if err != nil {
		return fmt.Errorf("subscriptions/listen: %w", err)
	}

	deliver := func(exposedURI string) {
		n := mcp.JSONRPCNotification{
			JSONRPC: mcp.JSONRPC_VERSION,
			Notification: mcp.Notification{
				Method: mcp.MethodNotificationResourceUpdated,
				Params: mcp.NotificationParams{
					Meta:             map[string]any{mcp.MetaKeySubscriptionID: id},
					AdditionalFields: map[string]any{"uri": exposedURI},
				},
			},
		}
		select {
		case session.NotificationChannel() <- n:
		default:
			logging.WarnWithAttrs("Aggregator", "Resource update dropped: the client's stream is full",
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
				slog.String("uri", exposedURI))
		}
	}

	servers := make([]string, 0, len(plan))
	for server := range plan {
		servers = append(servers, server)
	}
	sort.Strings(servers)

	var stops []func()
	stopAll := func() {
		for _, stop := range stops {
			stop()
		}
	}
	for _, server := range servers {
		stop, err := a.openResourceRelay(ctx, server, sessionID, sub, plan[server], deliver)
		if err != nil {
			stopAll()
			return fmt.Errorf("subscriptions/listen: %s: %w", server, err)
		}
		stops = append(stops, stop)
	}
	logging.InfoWithAttrs("Aggregator", "Resource subscriptions relayed",
		slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
		slog.String("subject", logging.TruncateIdentifier(sub)),
		slog.Any("servers", servers),
		slog.Int("uris", len(uris)))
	go func() {
		<-ctx.Done()
		stopAll()
	}()
	return nil
}

// planResourceSubscriptions groups the exposed URIs by the backend serving
// them: server -> backend URI -> exposed URI.
func (a *AggregatorServer) planResourceSubscriptions(ctx context.Context, sessionID string, uris []string) (map[string]map[string]string, error) {
	plan := make(map[string]map[string]string)
	for _, uri := range uris {
		server, original, err := a.resolveSubscribedResource(ctx, sessionID, uri)
		if err != nil {
			return nil, err
		}
		info, ok := a.registry.GetServerInfo(server)
		if !ok {
			return nil, fmt.Errorf("%s: server %s not found", uri, server)
		}
		if !info.RequiresSessionAuth() {
			return nil, fmt.Errorf("%s is served by %s, which muster reaches with its own identity; subscriptions are relayed only as the subscribing user", uri, server)
		}
		if plan[server] == nil {
			plan[server] = make(map[string]string)
		}
		plan[server][original] = uri
	}
	return plan, nil
}

// resolveSubscribedResource returns the backend serving an exposed resource
// URI and the URI the backend knows it by. A URI no listing named -- one a
// backend's resource template fills, such as a person's mailbox -- belongs to
// the one backend whose resources in the session's view share its scheme.
func (a *AggregatorServer) resolveSubscribedResource(ctx context.Context, sessionID, uri string) (string, string, error) {
	server, original, err := a.registry.ResolveResourceName(uri)
	if err == nil {
		return server, original, nil
	}
	i := strings.Index(uri, "://")
	if i <= 0 {
		return "", "", err
	}
	scheme := uri[:i+3]
	candidates := map[string]bool{}
	for _, r := range a.GetResourcesForSession(ctx, sessionID) {
		if strings.HasPrefix(r.Resource.URI, scheme) {
			candidates[r.Server] = true
		}
	}
	if len(candidates) != 1 {
		names := make([]string, 0, len(candidates))
		for name := range candidates {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return "", "", fmt.Errorf("unknown resource %s", uri)
		}
		return "", "", fmt.Errorf("resource %s: servers %s all serve %s URIs", uri, strings.Join(names, ", "), scheme)
	}
	for name := range candidates {
		return name, uri, nil
	}
	return "", "", fmt.Errorf("unknown resource %s", uri)
}

// openResourceRelay opens the session's subscriptions/listen stream to one
// backend for its URIs (backend URI -> exposed URI) and returns once the
// backend acknowledged it. deliver is called with the exposed URI of each
// update. stop ends the stream and closes the connection. A stream the
// backend ends while ctx lives is opened again.
func (a *AggregatorServer) openResourceRelay(
	ctx context.Context,
	server, sessionID, sub string,
	uris map[string]string,
	deliver func(exposedURI string),
) (stop func(), err error) {
	info, ok := a.registry.GetServerInfo(server)
	if !ok {
		return nil, fmt.Errorf("server %s not found", server)
	}
	if err := a.requireSessionAuth(ctx, info, sessionID, sub); err != nil {
		return nil, err
	}
	sc, err := a.newSessionClient(ctx, info, sessionID, sub, true)
	if err != nil {
		return nil, err
	}
	client := sc.client
	listener, ok := client.(resourceListener)
	if !ok {
		_ = client.Close()
		return nil, internalmcp.ErrSubscriptionsListenUnsupported
	}

	acked := make(chan struct{}, 1)
	client.OnNotification(func(n mcp.JSONRPCNotification) {
		switch n.Method {
		case mcp.MethodNotificationSubscriptionsAcknowledged:
			select {
			case acked <- struct{}{}:
			default:
			}
		case mcp.MethodNotificationResourceUpdated:
			uri, _ := n.Params.AdditionalFields["uri"].(string)
			if exposed, ok := uris[uri]; ok {
				deliver(exposed)
			}
		}
	})

	backendURIs := make([]string, 0, len(uris))
	for uri := range uris {
		backendURIs = append(backendURIs, uri)
	}
	sort.Strings(backendURIs)

	listenCtx, cancel := context.WithCancel(ctx)
	// The relay lives only while the session may reach the server: a
	// sign-out, a lost or expired credential ends it (resourceRelays). It is
	// registered before the stream opens, so a revocation during the
	// handshake reaches it too.
	authorized := func() bool { return a.relayAuthorized(ctx, info, sessionID, sub) }
	unregister := a.relayRegistry().add(sessionID, server, authorized, cancel)
	ended := make(chan error, 1)
	listen := func() { ended <- listener.ListenResources(listenCtx, backendURIs) }
	go listen()

	fail := func(err error) (func(), error) {
		cancel()
		awaitRelayEnd(server, ended)
		unregister()
		_ = client.Close()
		return nil, err
	}
	timeout := info.Timeout
	if timeout <= 0 {
		timeout = internalmcp.DefaultTimeout
	}
	ackTimer := time.NewTimer(timeout)
	defer ackTimer.Stop()
	select {
	case <-acked:
	case err := <-ended:
		ended <- err // for fail's wait
		if err == nil {
			err = errors.New("the server closed the stream before acknowledging it")
		}
		return fail(err)
	case <-ackTimer.C:
		return fail(fmt.Errorf("no acknowledgement within %s", timeout))
	case <-listenCtx.Done():
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		return fail(errors.New("the session no longer reaches the server"))
	}
	if !authorized() {
		return fail(errors.New("the session no longer reaches the server"))
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer unregister()
		defer func() { _ = client.Close() }()
		a.keepResourceRelay(listenCtx, server, sessionID, listen, ended, authorized)
	}()
	return func() {
		cancel()
		wg.Wait()
	}, nil
}

// keepResourceRelay opens a backend stream again each time it ends while ctx
// lives and the session still reaches the server, waiting longer after each
// attempt up to resourceRelayRetryMax.
func (a *AggregatorServer) keepResourceRelay(ctx context.Context, server, sessionID string, listen func(), ended <-chan error, authorized func() bool) {
	wait := resourceRelayRetryMin
	for {
		select {
		case <-ctx.Done():
			awaitRelayEnd(server, ended)
			return
		case err := <-ended:
			if ctx.Err() != nil {
				return
			}
			logging.WarnWithAttrs("Aggregator", "Relayed resource subscription ended by the server, reopening",
				slog.String("server", server),
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
				slog.Any("error", err),
				slog.Duration("after", wait))
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if !authorized() {
				logging.InfoWithAttrs("Aggregator", "Relayed resource subscription not reopened: the session no longer reaches the server",
					slog.String("server", server),
					slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
				return
			}
			wait = min(2*wait, resourceRelayRetryMax)
			go listen()
		}
	}
}

// hideLegacySubscribe keeps a legacy initialize from advertising
// resources.subscribe: resource subscriptions are relayed for
// subscriptions/listen (2026-07-28) only, and a legacy resources/subscribe
// would be acknowledged and never answered with an update.
func hideLegacySubscribe(result *mcp.InitializeResult) {
	if result != nil && result.Capabilities.Resources != nil {
		result.Capabilities.Resources.Subscribe = false
	}
}

// awaitRelayEnd waits for a cancelled backend stream to return.
func awaitRelayEnd(server string, ended <-chan error) {
	if err := <-ended; err != nil && !errors.Is(err, context.Canceled) {
		logging.Debug("Aggregator", "Relayed resource subscription to %s ended: %v", server, err)
	}
}
