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

// DynamicAuthClient implements the MCPClient interface using StreamableHTTP transport
// with mcp-go's built-in OAuth handler for automatic bearer token injection and
// typed 401 error handling.
//
// Instead of manually injecting Authorization headers via WithHTTPHeaderFunc,
// this client delegates token management to mcp-go's WithHTTPOAuth transport option.
// The transport.TokenStore adapter bridges muster's session-scoped token management
// to mcp-go's OAuth handler, enabling:
//   - Automatic bearer token injection on every request
//   - Typed OAuthAuthorizationRequiredError on 401 (preserving error details)
//   - Transparent token refresh via the TokenStore
type DynamicAuthClient struct {
	baseMCPClient
	url          string
	tokenStore   transport.TokenStore
	scope        string
	clientID     string
	clientSecret string

	// meta holds the spec.meta entries merged into params._meta of every
	// outbound request.
	meta map[string]string

	// onAuthLost, when set, is invoked once when the connection's
	// authentication is observed lost (see authLossDetector). Immutable after
	// construction; set via WithAuthLossHandler.
	onAuthLost func(reason string)
}

// WithMeta sets the entries merged into the params._meta object of every
// outbound JSON-RPC request that carries params, and returns the client so a
// construction site reads as one expression.
func (c *DynamicAuthClient) WithMeta(meta map[string]string) *DynamicAuthClient {
	c.meta = meta
	return c
}

// WithAuthLossHandler registers a function invoked once when the client's
// authentication is observed lost: the token store no longer holds a token,
// or the server keeps rejecting the held token with 401 (authLossThreshold
// consecutive failures either way). The handler runs on its own goroutine and
// is expected to retire this client — a lost grant never heals on its own, it
// needs a human to re-authenticate, so leaving the client running means
// mcp-go's continuous listener retries (and logs) every second forever.
//
// Returns the client so a construction site reads as one expression.
func (c *DynamicAuthClient) WithAuthLossHandler(fn func(reason string)) *DynamicAuthClient {
	c.onAuthLost = fn
	return c
}

// NewDynamicAuthClient creates a new StreamableHTTP-based MCP client with mcp-go's
// built-in OAuth handler. The TokenStore is queried on each HTTP request to get
// the current access token for bearer injection.
//
// Args:
//   - url: The MCP server URL
//   - tokenStore: Adapter providing OAuth tokens (implements transport.TokenStore)
//   - scope: The OAuth scope for this connection
//   - clientID: The OAuth client_id the tokens were issued under (CIMD URL or
//     DCR-registered id); mcp-go sends it on token refresh requests
//   - clientSecret: The client secret when the issuer registered muster as a
//     confidential client via DCR; empty for public clients
//
// Returns a new DynamicAuthClient ready for initialization.
func NewDynamicAuthClient(url string, tokenStore transport.TokenStore, scope, clientID, clientSecret string) *DynamicAuthClient {
	return &DynamicAuthClient{
		url:          url,
		tokenStore:   tokenStore,
		scope:        scope,
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

// Initialize establishes the connection and performs protocol handshake.
// Uses mcp-go's WithHTTPOAuth for automatic token injection and typed 401 handling.
func (c *DynamicAuthClient) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	logging.Debug("DynamicAuthClient", "Creating StreamableHTTP client for URL: %s with OAuth handler", c.url)

	// The detector watches both places a lost grant shows up: the token store
	// (token gone) and the wire (token rejected with 401). Nil when no handler
	// is registered — every observer method tolerates a nil detector.
	var detector *authLossDetector
	if c.onAuthLost != nil {
		detector = &authLossDetector{onAuthLost: c.onAuthLost}
	}

	var opts []transport.StreamableHTTPCOption
	if c.tokenStore != nil {
		tokenStore := c.tokenStore
		if detector != nil {
			tokenStore = &authObservingTokenStore{TokenStore: tokenStore, detector: detector}
		}
		opts = append(opts, transport.WithHTTPOAuth(transport.OAuthConfig{
			ClientID:     c.clientID,
			ClientSecret: c.clientSecret,
			TokenStore:   tokenStore,
			Scopes:       []string{c.scope},
		}))
	}

	// The OAuth handler is separate from the transport's HTTP client, so a
	// metadata-injecting client composes with bearer injection instead of
	// replacing it.
	httpClient := metaHTTPClient(c.meta)
	if httpClient != nil {
		logging.Debug("DynamicAuthClient", "Configured %d meta entries", len(c.meta))
	}
	if detector != nil {
		base := http.RoundTripper(http.DefaultTransport)
		if httpClient != nil {
			base = httpClient.Transport
		}
		// No timeout on the client, matching mcp-go's default: a timeout would
		// also cut the long-lived GET that WithContinuousListening opens.
		httpClient = &http.Client{Transport: &authObservingTransport{next: base, detector: detector}}
	}
	if httpClient != nil {
		opts = append(opts, transport.WithHTTPBasicClient(httpClient))
	}

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
			logging.Debug("DynamicAuthClient", "Authentication required for URL: %s", c.url)
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

		if authErr := CheckForAuthRequiredError(ctx, err, c.url); authErr != nil {
			logging.Debug("DynamicAuthClient", "Authentication required for URL: %s", c.url)
			return authErr
		}

		return fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}

	c.client = mcpClient
	c.connected = true
	c.negotiatedProtocolVersion = initResult.ProtocolVersion
	c.wireNotificationHandler()

	logging.Debug("DynamicAuthClient", "StreamableHTTP client initialized with OAuth handler. Server: %s, Version: %s",
		initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	return nil
}

// Close cleanly shuts down the client connection
func (c *DynamicAuthClient) Close() error {
	return c.closeClient()
}

// ListTools returns all available tools from the server
func (c *DynamicAuthClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	return c.listTools(ctx)
}

// CallTool executes a specific tool and returns the result
func (c *DynamicAuthClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	return c.callTool(ctx, name, args)
}

// ListResources returns all available resources from the server
func (c *DynamicAuthClient) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	return c.listResources(ctx)
}

// ReadResource retrieves a specific resource
func (c *DynamicAuthClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	return c.readResource(ctx, uri)
}

// ListPrompts returns all available prompts from the server
func (c *DynamicAuthClient) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	return c.listPrompts(ctx)
}

// GetPrompt retrieves a specific prompt
func (c *DynamicAuthClient) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	return c.getPrompt(ctx, name, args)
}

// Ping checks if the server is responsive
func (c *DynamicAuthClient) Ping(ctx context.Context) error {
	return c.ping(ctx)
}

// OnNotification registers a handler for server-pushed notifications.
func (c *DynamicAuthClient) OnNotification(handler func(mcp.JSONRPCNotification)) {
	c.onNotification(handler)
}
