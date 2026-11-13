package mcpserver

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// Protocol era of a backend connection.
//
// muster asks every backend for MCP 2026-07-28: Initialize asks for no
// version, so mcp-go probes server/discover. A backend that does not answer
// the probe as a 2026-07-28 server is spoken to on the revision it answers
// the initialize handshake with, 2025-11-25 at most, and the client remembers
// it for that connection (legacyBackend): a streamable-http one connects again
// to get the standalone GET stream that revision delivers notifications on. A
// connection that is closed or loses its session forgets it, so a backend
// upgraded behind the address is probed again.
// A client in listen mode does not fall back: it negotiates the revision
// itself (client_listen.go).
//
// A 2026-07-28 backend answers a tool call that needs the person with an
// input_required result. The client returns it unchanged (manual round trips),
// declares on the call the elicitation capability of the client muster calls
// for (WithCallerCapabilities), and attaches the inputResponses and
// requestState of a retry (WithRoundTrip). A 2025-11-25 backend fixes the
// capabilities at its handshake, where muster declares none: such a backend
// asks nothing of the person through muster.

// listChangedRetryMin and listChangedRetryMax bound the wait before a dropped
// subscriptions/listen stream for list changes is opened again.
const (
	listChangedRetryMin = time.Second
	listChangedRetryMax = 30 * time.Second
)

type callerCapabilitiesKey struct{}

// WithCallerCapabilities returns a context whose tool calls declare, to a
// 2026-07-28 backend, the forwarded capabilities (ForwardedCapabilities) of
// the client muster calls the tool for. A tool call whose context carries
// none declares none: the backend cannot ask anything of a caller muster
// knows nothing about.
func WithCallerCapabilities(ctx context.Context, capabilities mcp.ClientCapabilities) context.Context {
	return context.WithValue(ctx, callerCapabilitiesKey{}, ForwardedCapabilities(capabilities))
}

func callerCapabilitiesFrom(ctx context.Context) mcp.ClientCapabilities {
	capabilities, _ := ctx.Value(callerCapabilitiesKey{}).(mcp.ClientCapabilities)
	return capabilities
}

// ForwardedCapabilities is what muster declares to a backend of the
// capabilities its caller declared: elicitation only, the one input request
// muster passes through. Sampling and roots, deprecated in 2026-07-28
// (SEP-2577), would let a backend run the caller's model or read its
// workspace, and are not forwarded. Experimental capabilities and extensions
// describe what the caller does with muster, not what muster relays.
func ForwardedCapabilities(capabilities mcp.ClientCapabilities) mcp.ClientCapabilities {
	return mcp.ClientCapabilities{Elicitation: capabilities.Elicitation}
}

type roundTripKey struct{}

// WithRoundTrip returns a context whose tool call to a 2026-07-28 backend
// carries the inputResponses and requestState of a retry of an input_required
// answer of that backend.
func WithRoundTrip(ctx context.Context, roundTrip mcp.MultiRoundTripParams) context.Context {
	return context.WithValue(ctx, roundTripKey{}, roundTrip)
}

func roundTripFrom(ctx context.Context) mcp.MultiRoundTripParams {
	roundTrip, _ := ctx.Value(roundTripKey{}).(mcp.MultiRoundTripParams)
	return roundTrip
}

// initialize runs the handshake on a new mcp-go client: 2026-07-28 unless the
// backend is known not to speak it, in which case the initialize handshake
// asks for the newest revision it negotiates. A backend that refuses
// 2026-07-28 at server/discover and lists only earlier revisions gets that
// handshake too, which mcp-go does not fall back to by itself on that answer.
// Caller holds b.mu for writing.
func (b *baseMCPClient) initialize(ctx context.Context, mcpClient *client.Client, clientInfo mcp.Implementation) (*mcp.InitializeResult, error) {
	client.WithManualInputRoundTrips()(mcpClient)
	request := mcp.InitializeRequest{Params: mcp.InitializeParams{ClientInfo: clientInfo}}
	if b.legacyBackend {
		return b.initializeLegacy(ctx, mcpClient, request)
	}
	result, err := mcpClient.Initialize(ctx, request)
	if err == nil || b.listenMode || !mcp.IsUnsupportedProtocolVersion(err) {
		return result, err
	}
	return b.initializeLegacy(ctx, mcpClient, request)
}

func (b *baseMCPClient) initializeLegacy(ctx context.Context, mcpClient *client.Client, request mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	client.WithProtocolVersion(mcp.LATEST_LEGACY_PROTOCOL_VERSION)(mcpClient)
	request.Params.ProtocolVersion = mcp.LATEST_LEGACY_PROTOCOL_VERSION
	return mcpClient.Initialize(ctx, request)
}

// fellBackToLegacy records that a backend answered the handshake on a
// revision before 2026-07-28, and reports whether it was not known to before.
// Caller holds b.mu for writing.
func (b *baseMCPClient) fellBackToLegacy(result *mcp.InitializeResult) bool {
	if mcp.IsModernProtocol(result.ProtocolVersion) || b.legacyBackend {
		return false
	}
	b.legacyBackend = true
	return true
}

// listChangedFilter opts in to the list changes the server declared it sends.
func listChangedFilter(capabilities mcp.ServerCapabilities) mcp.SubscriptionFilter {
	return mcp.SubscriptionFilter{
		ToolsListChanged:     capabilities.Tools != nil && capabilities.Tools.ListChanged,
		PromptsListChanged:   capabilities.Prompts != nil && capabilities.Prompts.ListChanged,
		ResourcesListChanged: capabilities.Resources != nil && capabilities.Resources.ListChanged,
	}
}

// startListChangedStream holds a subscriptions/listen stream open to a
// 2026-07-28 backend for the list changes it declared, the revision's
// replacement of the standalone GET stream: the notifications reach the
// OnNotification handler. A dropped stream is opened again after a wait that
// doubles up to listChangedRetryMax. What the backend changed while the
// stream was down reached nobody, and a stream drops when another process
// takes over behind the address, so once the backend acknowledges the
// reopened stream the client runs server/discover again (rediscover) and the
// OnSessionRecovered handler is told, as after a recovered 2025-11-25
// session. A client in listen mode holds its own stream,
// and an earlier revision delivers notifications without one. Caller holds
// b.mu for writing, after recordHandshake.
func (b *baseMCPClient) startListChangedStream(mcpClient *client.Client, server string) {
	filter := listChangedFilter(b.serverCapabilities)
	if b.listenMode || filter.IsEmpty() || !mcp.IsModernProtocol(b.negotiatedProtocolVersion) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.stopListChanged = cancel
	go func() {
		wait := listChangedRetryMin
		for {
			opened := time.Now()
			err := mcpClient.Listen(ctx, filter)
			if ctx.Err() != nil {
				return
			}
			if time.Since(opened) > listChangedRetryMax {
				wait = listChangedRetryMin
			}
			logging.Debug("MCPClient", "list-changes stream to %s ended (%v); opening it again in %s", server, err, wait)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = min(2*wait, listChangedRetryMax)
			b.resyncOnListening.Store(true)
		}
	}()
}

// stopListChangedStream ends the list-changes stream of the connection that
// is going away. Caller holds b.mu for writing.
func (b *baseMCPClient) stopListChangedStream() {
	if b.stopListChanged != nil {
		b.stopListChanged()
		b.stopListChanged = nil
	}
}

// rediscover runs server/discover on a 2026-07-28 connection whose reopened
// list-changes stream was acknowledged, and records the result as the
// connection's handshake: the process now behind the address may declare
// another serverInfo. The OnSessionRecovered handler is told afterwards,
// also when the discover failed.
func (b *baseMCPClient) rediscover() {
	defer b.notifyRecovered()

	b.mu.RLock()
	current := b.client
	protocolVersion := b.negotiatedProtocolVersion
	b.mu.RUnlock()
	discoverer, ok := current.(interface {
		Discover(context.Context, mcp.DiscoverRequest) (*mcp.DiscoverResult, error)
	})
	if !ok {
		return
	}

	ctx, cancel, _ := b.operationContext(context.Background())
	defer cancel()
	result, err := discoverer.Discover(ctx, mcp.DiscoverRequest{})
	if err != nil {
		logging.Debug("MCPClient", "server/discover after the list-changes stream reopened failed: %v", err)
		return
	}

	handshake := &mcp.InitializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    result.Capabilities,
		Instructions:    result.Instructions,
	}
	if info := result.GetResultMeta().ServerInfo(); info != nil {
		handshake.ServerInfo = *info
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.connected && b.client == current {
		b.recordHandshake(handshake)
	}
}
