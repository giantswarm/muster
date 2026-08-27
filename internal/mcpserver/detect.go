package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// TransportUnknown is reported when neither transport probe produced a usable
// signal (server unreachable, or the responses fit neither transport).
const TransportUnknown = "unknown"

// DefaultDetectTimeout bounds a whole DetectTransport run when the caller
// does not pass an explicit timeout.
const DefaultDetectTimeout = 10 * time.Second

// TransportDetectionResult is the outcome of probing a remote MCP server URL
// to determine which transport it speaks. Detection never fails hard: an
// unreachable or unclassifiable server yields Transport "unknown" so callers
// (e.g. a registration wizard) can fall back to manual selection.
type TransportDetectionResult struct {
	// URL is the probed endpoint.
	URL string `json:"url"`

	// Transport is the detected transport: "streamable-http", "sse", or
	// "unknown" when detection was inconclusive.
	Transport string `json:"transport"`

	// Reachable reports whether the server answered either probe at the
	// HTTP level (including with a 401 challenge).
	Reachable bool `json:"reachable"`

	// RequiresAuth reports that a probe was answered with a 401 challenge,
	// meaning the server exists but wants OAuth before the handshake.
	RequiresAuth bool `json:"requiresAuth"`

	// ServerName and ServerVersion carry the server implementation info
	// from a successful initialize handshake, when one completed.
	ServerName    string `json:"serverName,omitempty"`
	ServerVersion string `json:"serverVersion,omitempty"`

	// Detail is a human-readable explanation of how the verdict was reached.
	Detail string `json:"detail"`
}

// probeOutcome captures one transport probe's classification.
type probeOutcome struct {
	// ok means the probe completed the transport-specific handshake.
	ok bool
	// authRequired means the server answered with a 401 challenge.
	authRequired bool
	// serverInfo from a successful initialize, when available.
	serverInfo *mcp.Implementation
	// err is the raw failure for the Detail message.
	err error
}

// DetectTransport probes url to determine whether it speaks the
// streamable-http or the legacy SSE MCP transport.
//
// The streamable-http probe runs first: a successful initialize POST is
// definitive for streamable-http, and probing order matters because a
// streamable-http server may hold a GET stream open without ever sending
// the endpoint event the SSE probe waits for. The SSE probe is equally
// definitive when it completes, since only SSE servers emit the endpoint
// event. A 401 challenge identifies a live MCP endpoint without revealing
// the transport by itself; when that is the only signal, streamable-http
// is reported because the WWW-Authenticate challenge flow is part of the
// modern spec generation that also introduced streamable-http.
func DetectTransport(ctx context.Context, url string, headers map[string]string, timeout time.Duration) *TransportDetectionResult {
	if timeout <= 0 {
		timeout = DefaultDetectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := &TransportDetectionResult{
		URL:       url,
		Transport: TransportUnknown,
	}

	streamable := probeStreamableHTTP(ctx, url, headers)
	if streamable.ok {
		result.Transport = string(api.MCPServerTypeStreamableHTTP)
		result.Reachable = true
		result.Detail = "initialize handshake succeeded over streamable-http"
		setServerInfo(result, streamable.serverInfo)
		return result
	}

	sse := probeSSE(ctx, url, headers)
	if sse.ok {
		result.Transport = string(api.MCPServerTypeSSE)
		result.Reachable = true
		result.Detail = "server sent the SSE endpoint event"
		setServerInfo(result, sse.serverInfo)
		return result
	}

	switch {
	case streamable.authRequired:
		// The POST initialize was answered with a 401 challenge and the SSE
		// probe found nothing better: report streamable-http, pending auth.
		result.Transport = string(api.MCPServerTypeStreamableHTTP)
		result.Reachable = true
		result.RequiresAuth = true
		result.Detail = "server answered with a 401 challenge; assuming streamable-http until authentication completes"
	case sse.authRequired:
		// The streamable-http probe failed outright but the SSE GET drew a
		// 401 challenge, so the endpoint responds to the SSE connection style.
		result.Transport = string(api.MCPServerTypeSSE)
		result.Reachable = true
		result.RequiresAuth = true
		result.Detail = "SSE connection answered with a 401 challenge; assuming sse until authentication completes"
	default:
		result.Detail = fmt.Sprintf("no probe succeeded (streamable-http: %v; sse: %v)", streamable.err, sse.err)
	}

	logging.Debug("TransportDetect", "Detection for %s: transport=%s reachable=%t requiresAuth=%t",
		url, result.Transport, result.Reachable, result.RequiresAuth)
	return result
}

func setServerInfo(result *TransportDetectionResult, info *mcp.Implementation) {
	if info == nil {
		return
	}
	result.ServerName = info.Name
	result.ServerVersion = info.Version
}

// probeStreamableHTTP attempts a streamable-http initialize against url.
// Unlike the aggregator's StreamableHTTPClient it does not enable continuous
// listening — the probe is a short-lived handshake, not a connection.
func probeStreamableHTTP(ctx context.Context, url string, headers map[string]string) probeOutcome {
	var opts []transport.StreamableHTTPCOption
	if len(headers) > 0 {
		opts = append(opts, transport.WithHTTPHeaders(headers))
	}

	mcpClient, err := client.NewStreamableHttpClient(url, opts...)
	if err != nil {
		return probeOutcome{err: err}
	}
	defer func() { _ = mcpClient.Close() }()

	if err := mcpClient.Start(ctx); err != nil {
		return classifyProbeError(ctx, err, url)
	}

	initResult, err := mcpClient.Initialize(ctx, probeInitializeRequest())
	if err != nil {
		return classifyProbeError(ctx, err, url)
	}

	return probeOutcome{ok: true, serverInfo: &initResult.ServerInfo}
}

// probeSSE attempts an SSE connection against url. mcp-go's SSE transport
// Start only returns nil once the server has sent the SSE endpoint event,
// which only SSE MCP servers emit — so a successful Start alone identifies
// the transport, and the initialize afterwards just enriches the result.
func probeSSE(ctx context.Context, url string, headers map[string]string) probeOutcome {
	var opts []transport.ClientOption
	if len(headers) > 0 {
		opts = append(opts, transport.WithHeaders(headers))
	}

	mcpClient, err := client.NewSSEMCPClient(url, opts...)
	if err != nil {
		return probeOutcome{err: err}
	}
	defer func() { _ = mcpClient.Close() }()

	if err := mcpClient.Start(ctx); err != nil {
		return classifyProbeError(ctx, err, url)
	}

	initResult, err := mcpClient.Initialize(ctx, probeInitializeRequest())
	if err != nil {
		if outcome := classifyProbeError(ctx, err, url); outcome.authRequired {
			return outcome
		}
		// The endpoint event already identified the transport; a failed
		// handshake afterwards doesn't change the verdict.
		return probeOutcome{ok: true}
	}

	return probeOutcome{ok: true, serverInfo: &initResult.ServerInfo}
}

func classifyProbeError(ctx context.Context, err error, url string) probeOutcome {
	if authErr := CheckForAuthRequiredError(ctx, err, url); authErr != nil {
		return probeOutcome{authRequired: true, err: err}
	}
	return probeOutcome{err: err}
}

func probeInitializeRequest() mcp.InitializeRequest {
	return mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: api.ClientProtocolVersion,
			ClientInfo: mcp.Implementation{
				Name:    "muster-transport-probe",
				Version: clientVersion,
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	}
}
