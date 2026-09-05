package mcpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
	"github.com/giantswarm/muster/pkg/observability"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpotel "github.com/mark3labs/mcp-go/otel"
	"go.opentelemetry.io/otel"
)

// StreamableHTTPClient implements the MCPClient interface using StreamableHTTP transport.
// It connects to remote MCP servers using HTTP with streaming support.
type StreamableHTTPClient struct {
	baseMCPClient
	url        string
	headers    map[string]string
	headerFunc transport.HTTPHeaderFunc // Dynamic header function called on each request

	// meta holds the spec.meta entries merged into params._meta of every
	// outbound request. Consulted only when httpClientFunc is nil, because a
	// client that builds its own transport chain injects them there.
	meta map[string]string

	// httpClientFunc, when set, supplies the HTTP client the transport uses
	// instead of mcp-go's default. It is called on each connect attempt, under
	// the client lock, so a per-connection credential can be resolved with the
	// connect context. See newSigV4Client, its only caller today.
	httpClientFunc func(context.Context) (*http.Client, error)
}

// WithMeta sets the entries merged into the params._meta object of every
// outbound JSON-RPC request that carries params, and returns the client so a
// construction site reads as one expression.
func (c *StreamableHTTPClient) WithMeta(meta map[string]string) *StreamableHTTPClient {
	c.meta = meta
	return c
}

// NewStreamableHTTPClientWithHeaders creates a new StreamableHTTP-based MCP client with custom headers
func NewStreamableHTTPClientWithHeaders(url string, headers map[string]string) *StreamableHTTPClient {
	if headers == nil {
		headers = make(map[string]string)
	}
	return &StreamableHTTPClient{
		url:     url,
		headers: headers,
	}
}

// NewStreamableHTTPClientWithHeaderFunc creates a new StreamableHTTP-based MCP client
// with a dynamic header function that is called on every request. This enables token
// refresh by resolving the latest token at call time instead of baking in a static header.
func NewStreamableHTTPClientWithHeaderFunc(url string, headerFunc transport.HTTPHeaderFunc) *StreamableHTTPClient {
	return &StreamableHTTPClient{
		url:        url,
		headers:    make(map[string]string),
		headerFunc: headerFunc,
	}
}

// Initialize establishes the connection and performs protocol handshake
func (c *StreamableHTTPClient) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	logging.Debug("StreamableHTTPClient", "Creating StreamableHTTP client for URL: %s", c.url)

	// Build client options including headers if provided
	var opts []transport.StreamableHTTPCOption
	if c.headerFunc != nil {
		opts = append(opts, transport.WithHTTPHeaderFunc(c.headerFunc))
		logging.Debug("StreamableHTTPClient", "Configured dynamic header function")
	} else if len(c.headers) > 0 {
		opts = append(opts, transport.WithHTTPHeaders(c.headers))
		logging.Debug("StreamableHTTPClient", "Configured %d custom headers", len(c.headers))
	}

	var httpClient *http.Client
	if c.httpClientFunc != nil {
		built, err := c.httpClientFunc(ctx)
		if err != nil {
			return fmt.Errorf("failed to build HTTP client: %w", err)
		}
		httpClient = built
		logging.Debug("StreamableHTTPClient", "Configured custom HTTP client")
	} else if httpClient = metaHTTPClient(c.meta); httpClient != nil {
		logging.Debug("StreamableHTTPClient", "Configured %d meta entries", len(c.meta))
	}
	// Every connection records the backend's 401 challenge so a rejection can
	// be attributed (see challengeRecorder); with no client of its own this
	// is mcp-go's default client plus the recorder.
	challenges := &challengeRecorder{}
	opts = append(opts, transport.WithHTTPBasicClient(recordingHTTPClient(httpClient, challenges)))

	// Enable receiving server-pushed notifications outside active requests.
	// This opens a long-lived GET connection to the server per the MCP spec.
	// SA1019: mcp-go v1 deprecates WithContinuousListening because
	// protocol revision 2026-07-28 removed the standalone GET stream in
	// favour of Client.Listen. Keeping the option is the behaviour-
	// preserving choice here: it pins the connection to the legacy
	// handshake, which is exactly where mcp-go v0.58.0 already was, and
	// Client.Listen errors out unless the peer negotiated 2026-07-28.
	// Dropping it would instead silently cost every pre-2026-07-28
	// backend its out-of-band notifications. Adopting Client.Listen is a
	// deliberate protocol migration, tracked separately.
	opts = append(opts, transport.WithContinuousListening()) //nolint:staticcheck // SA1019: see comment above
	opts = append(opts, transport.WithHTTPLogger(mcpTransportLogger(c.url)))

	mcpClient, err := client.NewStreamableHttpClient(c.url, opts...)
	if err != nil {
		return fmt.Errorf("failed to create StreamableHTTP client: %w", err)
	}
	mcpotel.WithClientTracing(otel.Tracer(observability.TracerName))(mcpClient)

	// Start with a background context so the continuous GET listener goroutine
	// survives after the caller's initialization context (which may be short-lived) completes.
	// The listener goroutine is separately cancelled when the client is closed.
	if err := mcpClient.Start(context.Background()); err != nil {
		_ = mcpClient.Close()
		if authErr := CheckForAuthRequiredError(ctx, err, c.url); authErr != nil {
			authErr.Challenge = challenges.challenge()
			logging.Debug("StreamableHTTPClient", "Authentication required for URL: %s", c.url)
			return authErr
		}
		return fmt.Errorf("failed to start StreamableHTTP transport: %w", err)
	}

	initResult, err := mcpClient.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: api.ClientProtocolVersion,
			ClientInfo: mcp.Implementation{
				Name:    clientName,
				Version: clientVersion,
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	})
	if err != nil {
		_ = mcpClient.Close()

		// Check if this is a 401 authentication error
		if authErr := CheckForAuthRequiredError(ctx, err, c.url); authErr != nil {
			authErr.Challenge = challenges.challenge()
			logging.Debug("StreamableHTTPClient", "Authentication required for URL: %s", c.url)
			return authErr
		}

		return fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}

	c.client = mcpClient
	c.connected = true
	c.negotiatedProtocolVersion = initResult.ProtocolVersion
	c.wireNotificationHandler()

	logging.Debug("StreamableHTTPClient", "StreamableHTTP client initialized. Server: %s, Version: %s",
		initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	return nil
}

// Close cleanly shuts down the client connection
func (c *StreamableHTTPClient) Close() error {
	return c.closeClient()
}

// ListTools returns all available tools from the server
func (c *StreamableHTTPClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	return c.listTools(ctx)
}

// CallTool executes a specific tool and returns the result
func (c *StreamableHTTPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	return c.callTool(ctx, name, args)
}

// ListResources returns all available resources from the server
func (c *StreamableHTTPClient) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	return c.listResources(ctx)
}

// ReadResource retrieves a specific resource
func (c *StreamableHTTPClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	return c.readResource(ctx, uri)
}

// ListPrompts returns all available prompts from the server
func (c *StreamableHTTPClient) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	return c.listPrompts(ctx)
}

// GetPrompt retrieves a specific prompt
func (c *StreamableHTTPClient) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	return c.getPrompt(ctx, name, args)
}

// Ping checks if the server is responsive
func (c *StreamableHTTPClient) Ping(ctx context.Context) error {
	return c.ping(ctx)
}

// OnNotification registers a handler for server-pushed notifications.
func (c *StreamableHTTPClient) OnNotification(handler func(mcp.JSONRPCNotification)) {
	c.onNotification(handler)
}
