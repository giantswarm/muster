package mcpserver

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// MCPClient defines the interface for MCP client implementations.
// All transport types (stdio, SSE, streamable-http) implement this interface,
// enabling polymorphic usage and easier testing with mocks.
type MCPClient interface {
	// Initialize establishes the connection and performs protocol handshake
	Initialize(ctx context.Context) error
	// Close cleanly shuts down the client connection
	Close() error
	// ListTools returns all available tools from the server
	ListTools(ctx context.Context) ([]mcp.Tool, error)
	// CallTool executes a specific tool and returns the result
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error)
	// ListResources returns all available resources from the server
	ListResources(ctx context.Context) ([]mcp.Resource, error)
	// ReadResource retrieves a specific resource
	ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error)
	// ListPrompts returns all available prompts from the server
	ListPrompts(ctx context.Context) ([]mcp.Prompt, error)
	// GetPrompt retrieves a specific prompt
	GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error)
	// Ping checks if the server is responsive
	Ping(ctx context.Context) error
	// OnNotification registers a handler that is called when the server
	// sends a JSON-RPC notification (e.g. notifications/tools/list_changed).
	// The handler is wired to the underlying mcp-go client during Initialize.
	OnNotification(handler func(mcp.JSONRPCNotification))
	// NegotiatedProtocolVersion returns the MCP revision the server answered
	// with during the handshake, or "" before a successful Initialize:
	// 2026-07-28, or the earlier revision of a server that does not speak it.
	NegotiatedProtocolVersion() string
	// ServerCapabilities returns the capabilities the server declared in its
	// initialize result, or the zero value before a successful Initialize. A
	// caller lists only what the server declared: the protocol reserves
	// resources/list and prompts/list for servers that offer them.
	ServerCapabilities() mcp.ServerCapabilities
}

// Compile-time interface compliance checks
var (
	_ MCPClient = (*StdioClient)(nil)
	_ MCPClient = (*SSEClient)(nil)
	_ MCPClient = (*StreamableHTTPClient)(nil)
	_ MCPClient = (*DynamicAuthClient)(nil)
)

// baseMCPClient provides common functionality for all MCP client implementations.
// It implements the shared MCP protocol operations that are identical across
// different transport types (stdio, SSE, streamable-http).
type baseMCPClient struct {
	client    client.MCPClient
	mu        sync.RWMutex
	connected bool

	// negotiatedProtocolVersion and serverCapabilities hold the
	// protocolVersion and the capabilities from the initialize response.
	// mcp-go keeps them in private fields with no accessor, so each transport
	// records them here as it completes the handshake (recordHandshake).
	negotiatedProtocolVersion string
	serverCapabilities        mcp.ServerCapabilities
	serverInfo                mcp.Implementation

	// Session recovery, see client_session_recovery.go. reconnect is set by
	// the transports whose backend keeps an MCP session (streamable-http); it
	// is called with mu held for writing, after the dead client was closed,
	// and performs a fresh handshake into client/connected/hadSession. nil
	// for transports that have no session to lose (stdio, SSE).
	reconnect func(ctx context.Context) error
	// hadSession records whether the server issued an Mcp-Session-Id during
	// the handshake. Only a stateful server can lose a session.
	hadSession bool
	// reconnectPending is set while a recovery attempt has failed and left
	// the client disconnected, so the next operation tries again instead of
	// failing with "client not connected" until something restarts the service.
	reconnectPending bool
	// sessionGeneration counts recovery handshake attempts, successful or not;
	// a caller that failed on an older generation finds the recovery already
	// attempted and acts on its result instead of repeating it.
	sessionGeneration uint64
	// recoveryErr is why the last recovery handshake failed, nil once one
	// succeeds. Callers that find the generation advanced report it together
	// with their own failure, the same as the caller that ran the handshake.
	recoveryErr error
	// timeout is the one budget every operation on the client runs under:
	// each request, and the recovery handshake a lost session triggers on
	// the way, share it (see operationContext). Zero means DefaultTimeout.
	// The factory and the aggregator's per-session construction sites set it
	// from the server's spec.timeout through setTimeout, for every transport,
	// so the CRD's value governs a tool call end to end; the caller's context
	// alone may carry no deadline. A changed spec.timeout reaches a live
	// client through SetBudget, so it is read and written atomically.
	timeout atomic.Int64
	// maxCallDuration bounds a tool call whose server keeps reporting
	// progress: up to it, each progress notification restarts timeout (see
	// callContext). Zero means DefaultMaxCallDuration; at or below timeout,
	// progress extends nothing. Atomic like timeout.
	maxCallDuration atomic.Int64

	notifMu      sync.Mutex
	notifHandler func(mcp.JSONRPCNotification)
	// recoveredHandler is told, from a goroutine of its own, every time a
	// lost MCP session was re-established by a fresh handshake
	// (OnSessionRecovered).
	recoveredHandler func()

	// progress routes the server's notifications/progress to the callers of
	// the tool calls in flight (see progress.go).
	progress progressRouter

	// listenMode makes a streamable-http client negotiate protocol revision
	// 2026-07-28 for subscriptions/listen (client_listen.go).
	listenMode bool

	// legacyBackend records that the backend answered the last handshake on
	// a revision before 2026-07-28 (client_protocol.go).
	legacyBackend bool
	// stopListChanged ends the subscriptions/listen stream for list changes
	// of a 2026-07-28 connection (startListChangedStream).
	stopListChanged context.CancelFunc
	// resyncOnListening makes the next acknowledgement of the list-changes
	// stream tell the OnSessionRecovered handler (startListChangedStream).
	resyncOnListening atomic.Bool
}

// OnSessionRecovered registers a handler called after the client lost its
// MCP session and re-established it with a fresh handshake (session
// recovery, client_session_recovery.go), or, on a 2026-07-28 connection,
// after its dropped list-changes stream was opened again and acknowledged
// (startListChangedStream). Either happens when a new process took over
// behind the backend's address: the handler is where a caller re-reads what
// that process serves. It runs on a goroutine of its own, so it may use the
// client.
func (b *baseMCPClient) OnSessionRecovered(handler func()) {
	b.notifMu.Lock()
	b.recoveredHandler = handler
	b.notifMu.Unlock()
}

// notifyRecovered hands a successful recovery to the registered handler.
func (b *baseMCPClient) notifyRecovered() {
	b.notifMu.Lock()
	handler := b.recoveredHandler
	b.notifMu.Unlock()
	if handler != nil {
		go handler()
	}
}

// setTimeout sets the budget every operation on the client runs under (see
// timeout); the transports' WithTimeout setters and the factory go through it.
func (b *baseMCPClient) setTimeout(timeout time.Duration) {
	b.timeout.Store(int64(timeout))
}

// setMaxCallDuration sets how long progress may keep a tool call going (see
// maxCallDuration); the transports' WithMaxCallDuration setters and the
// factory go through it.
func (b *baseMCPClient) setMaxCallDuration(maxCallDuration time.Duration) {
	b.maxCallDuration.Store(int64(maxCallDuration))
}

// SetBudget replaces the client's timeout and maxCallDuration while it is in
// use: the server's spec.timeout or spec.progress.maxDuration changed and the
// connection stays. Every operation started after it runs under the new
// budget; one in flight keeps the budget it started with. Safe for
// concurrent use.
func (b *baseMCPClient) SetBudget(timeout, maxCallDuration time.Duration) {
	b.setTimeout(timeout)
	b.setMaxCallDuration(maxCallDuration)
}

// checkConnected verifies the client is connected and returns an error if not.
// This is a helper for consistent error handling across all MCP operations.
// Note: Caller must hold at least a read lock on mu.
func (b *baseMCPClient) checkConnected() error {
	if !b.connected || b.client == nil {
		return errNotConnected
	}
	return nil
}

// closeClient performs the common close logic
func (b *baseMCPClient) closeClient() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// An explicit close ends the session for good: no operation after it may
	// reconnect on the caller's behalf.
	b.reconnectPending = false
	b.hadSession = false

	if !b.connected || b.client == nil {
		return nil
	}

	err := b.client.Close()
	b.connected = false
	b.client = nil
	b.clearHandshake()

	return err
}

// recordHandshake keeps what the server answered the initialize with. Called
// by each transport with mu held for writing, as it completes the handshake.
func (b *baseMCPClient) recordHandshake(result *mcp.InitializeResult) {
	b.negotiatedProtocolVersion = result.ProtocolVersion
	b.serverCapabilities = result.Capabilities
	b.serverInfo = result.ServerInfo
}

// clearHandshake forgets the last initialize result: the connection it
// described is gone, and the process behind the address may speak another
// revision now. Called with mu held for writing.
func (b *baseMCPClient) clearHandshake() {
	b.stopListChangedStream()
	b.legacyBackend = false
	b.negotiatedProtocolVersion = ""
	b.serverCapabilities = mcp.ServerCapabilities{}
	b.serverInfo = mcp.Implementation{}
}

// NegotiatedProtocolVersion returns the MCP revision the server answered with
// during the handshake, or "" when the client is not connected.
func (b *baseMCPClient) NegotiatedProtocolVersion() string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.connected {
		return ""
	}
	return b.negotiatedProtocolVersion
}

// ServerCapabilities returns the capabilities the server declared during the
// handshake, or the zero value when the client is not connected.
func (b *baseMCPClient) ServerCapabilities() mcp.ServerCapabilities {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.connected {
		return mcp.ServerCapabilities{}
	}
	return b.serverCapabilities
}

// ServerImplementation returns the serverInfo of the initialize result
// (name, version, icons), or the zero value when the client is not connected.
func (b *baseMCPClient) ServerImplementation() mcp.Implementation {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.connected {
		return mcp.Implementation{}
	}
	return b.serverInfo
}

// listTools returns all available tools from the server
func (b *baseMCPClient) listTools(ctx context.Context) ([]mcp.Tool, error) {
	return withSessionRecovery(b, ctx, func(ctx context.Context) ([]mcp.Tool, error) { return b.listToolsOnce(ctx) })
}

func (b *baseMCPClient) listToolsOnce(ctx context.Context) ([]mcp.Tool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	result, err := b.client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	return result.Tools, nil
}

// callTool executes a specific tool and returns the result
func (b *baseMCPClient) callTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	opCtx, cancel, timeout := b.callContext(ctx)
	defer cancel()
	return recovering(b, opCtx, ctx, timeout, func(ctx context.Context) (*mcp.CallToolResult, error) { return b.callToolOnce(ctx, name, args) })
}

func (b *baseMCPClient) callToolOnce(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	params := mcp.CallToolParams{
		Name:                 name,
		Arguments:            args,
		MultiRoundTripParams: roundTripFrom(ctx),
	}
	if reporter := progressReporterFrom(ctx); reporter != nil {
		token, done := b.progress.open(reporter, callBudgetFrom(ctx))
		defer done()
		params.Meta = &mcp.Meta{ProgressToken: token}
	}

	ctx = client.WithRequestClientCapabilities(ctx, callerCapabilitiesFrom(ctx))
	result, err := b.client.CallTool(ctx, mcp.CallToolRequest{Params: params})
	if err != nil {
		return nil, fmt.Errorf("failed to call tool: %w", err)
	}

	return result, nil
}

// listResources returns all available resources from the server
func (b *baseMCPClient) listResources(ctx context.Context) ([]mcp.Resource, error) {
	return withSessionRecovery(b, ctx, func(ctx context.Context) ([]mcp.Resource, error) { return b.listResourcesOnce(ctx) })
}

func (b *baseMCPClient) listResourcesOnce(ctx context.Context) ([]mcp.Resource, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	result, err := b.client.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	return result.Resources, nil
}

// readResource retrieves a specific resource
func (b *baseMCPClient) readResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	return withSessionRecovery(b, ctx, func(ctx context.Context) (*mcp.ReadResourceResult, error) { return b.readResourceOnce(ctx, uri) })
}

func (b *baseMCPClient) readResourceOnce(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	result, err := b.client.ReadResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{
			URI: uri,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read resource: %w", err)
	}

	return result, nil
}

// listPrompts returns all available prompts from the server
func (b *baseMCPClient) listPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	return withSessionRecovery(b, ctx, func(ctx context.Context) ([]mcp.Prompt, error) { return b.listPromptsOnce(ctx) })
}

func (b *baseMCPClient) listPromptsOnce(ctx context.Context) ([]mcp.Prompt, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	result, err := b.client.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}

	return result.Prompts, nil
}

// getPrompt retrieves a specific prompt
func (b *baseMCPClient) getPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	return withSessionRecovery(b, ctx, func(ctx context.Context) (*mcp.GetPromptResult, error) { return b.getPromptOnce(ctx, name, args) })
}

func (b *baseMCPClient) getPromptOnce(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	// Convert args to map[string]string as required by the API
	stringArgs := make(map[string]string)
	for k, v := range args {
		if str, ok := v.(string); ok {
			stringArgs[k] = str
		} else {
			stringArgs[k] = fmt.Sprintf("%v", v)
		}
	}

	result, err := b.client.GetPrompt(ctx, mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Name:      name,
			Arguments: stringArgs,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt: %w", err)
	}

	return result, nil
}

// ping checks if the server is responsive
func (b *baseMCPClient) ping(ctx context.Context) error {
	_, err := withSessionRecovery(b, ctx, func(ctx context.Context) (struct{}, error) { return struct{}{}, b.pingOnce(ctx) })
	return err
}

func (b *baseMCPClient) pingOnce(ctx context.Context) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return err
	}

	return b.client.Ping(ctx)
}

// onNotification stores the handler every server notification except
// notifications/progress is passed to; the progress router takes those.
func (b *baseMCPClient) onNotification(handler func(mcp.JSONRPCNotification)) {
	b.notifMu.Lock()
	b.notifHandler = handler
	b.notifMu.Unlock()
}

// wireNotificationHandler registers dispatchNotification on the underlying
// mcp-go client. Must be called while holding b.mu (write lock) and after
// b.client has been assigned, once per underlying client.
func (b *baseMCPClient) wireNotificationHandler() {
	if b.client != nil {
		b.client.OnNotification(b.dispatchNotification)
	}
}

// dispatchNotification routes progress to the call it belongs to, tells the
// OnSessionRecovered handler when a reopened list-changes stream is
// acknowledged, and passes every other notification to the stored handler.
func (b *baseMCPClient) dispatchNotification(notification mcp.JSONRPCNotification) {
	if notification.Method == string(mcp.MethodNotificationProgress) {
		b.progress.route(notification)
		return
	}

	if notification.Method == mcp.MethodNotificationSubscriptionsAcknowledged && b.resyncOnListening.CompareAndSwap(true, false) {
		b.notifyRecovered()
	}

	b.notifMu.Lock()
	handler := b.notifHandler
	b.notifMu.Unlock()

	if handler != nil {
		handler(notification)
	}
}
