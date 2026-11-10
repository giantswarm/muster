package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// ErrSubscriptionsListenUnsupported is returned by Initialize on a client in
// listen mode (UseSubscriptionsListen) whose server does not speak protocol
// revision 2026-07-28: subscriptions/listen does not exist before it.
var ErrSubscriptionsListenUnsupported = errors.New("the server does not serve subscriptions/listen (MCP " + mcp.ProtocolVersion20260728 + ")")

// ResourceListener is implemented by the clients that can hold a
// subscriptions/listen stream open to their server: the streamable-http ones
// in listen mode.
type ResourceListener interface {
	MCPClient
	// UseSubscriptionsListen puts the client, before Initialize, in listen
	// mode: it negotiates protocol revision 2026-07-28 and opens no
	// standalone notification stream.
	UseSubscriptionsListen()
	// ListenResources opens a subscriptions/listen stream for the resource
	// URIs and blocks until ctx ends or the server closes the stream. The
	// server's notifications arrive at the OnNotification handler, its
	// notifications/subscriptions/acknowledged first.
	ListenResources(ctx context.Context, uris []string) error
}

var (
	_ ResourceListener = (*StreamableHTTPClient)(nil)
	_ ResourceListener = (*DynamicAuthClient)(nil)
)

// UseSubscriptionsListen puts the client in listen mode; see ResourceListener.
func (b *baseMCPClient) UseSubscriptionsListen() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listenMode = true
}

// handshakeOptions returns the transport options of a streamable-http
// connection. Only a backend known to speak a revision before 2026-07-28
// (legacyBackend) gets the standalone GET stream, which pins the connection
// to the initialize handshake; every other connection probes server/discover
// first, and one in listen mode must speak 2026-07-28. Caller holds b.mu.
func (b *baseMCPClient) handshakeOptions() []transport.StreamableHTTPCOption {
	if b.listenMode || !b.legacyBackend {
		return nil
	}
	// SA1019: mcp-go v1 deprecates WithContinuousListening because
	// protocol revision 2026-07-28 removed the standalone GET stream in
	// favour of Client.Listen. A backend on an earlier revision delivers its
	// out-of-band notifications only on that stream.
	return []transport.StreamableHTTPCOption{transport.WithContinuousListening()} //nolint:staticcheck // SA1019: see comment above
}

// checkListenHandshake refuses a listen-mode connection that did not
// negotiate 2026-07-28. Caller holds b.mu.
func (b *baseMCPClient) checkListenHandshake(url string, result *mcp.InitializeResult) error {
	if !b.listenMode || mcp.IsModernProtocol(result.ProtocolVersion) {
		return nil
	}
	return fmt.Errorf("%s answered with protocol %s: %w", url, result.ProtocolVersion, ErrSubscriptionsListenUnsupported)
}

// checkListenInitializeError refuses a listen-mode connection whose server
// rejected 2026-07-28 at server/discover: mcp-go returns the server's
// UnsupportedProtocolVersionError rather than falling back to the initialize
// handshake. Caller holds b.mu.
func (b *baseMCPClient) checkListenInitializeError(url string, err error) error {
	if !b.listenMode || !mcp.IsUnsupportedProtocolVersion(err) {
		return nil
	}
	return fmt.Errorf("%s: %w: %w", url, ErrSubscriptionsListenUnsupported, err)
}

// ListenResources opens a subscriptions/listen stream; see ResourceListener.
// The lock is not held while the stream is open, so Close can end it.
func (b *baseMCPClient) ListenResources(ctx context.Context, uris []string) error {
	b.mu.RLock()
	if err := b.checkConnected(); err != nil {
		b.mu.RUnlock()
		return err
	}
	if !b.listenMode {
		b.mu.RUnlock()
		return errors.New("the client is not in listen mode")
	}
	c := b.client
	b.mu.RUnlock()

	listener, ok := c.(interface {
		Listen(context.Context, mcp.SubscriptionFilter) error
	})
	if !ok {
		return fmt.Errorf("%T cannot listen", c)
	}
	return listener.Listen(ctx, mcp.SubscriptionFilter{ResourceSubscriptions: uris})
}
