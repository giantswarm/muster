package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/giantswarm/muster/internal/template"

	"github.com/mark3labs/mcp-go/server"
)

// ProtectedMCPServerConfig configures an OAuth-protected mock MCP server
type ProtectedMCPServerConfig struct {
	// Name is the name of this MCP server
	Name string

	// OAuthServer is the mock OAuth server to validate tokens against
	OAuthServer *OAuthServer

	// Issuer is the expected token issuer (used for WWW-Authenticate header)
	Issuer string

	// RequiredScope is the OAuth scope required to access this server
	RequiredScope string

	// Tools are the tools to expose when authenticated
	Tools []ToolConfig

	// Transport is the HTTP transport type (sse or streamable-http)
	Transport HTTPTransportType

	// Debug enables debug logging
	Debug bool
}

// ProtectedMCPServer is a mock MCP server that requires OAuth authentication
type ProtectedMCPServer struct {
	config     ProtectedMCPServerConfig
	mockServer *Server
	httpServer *http.Server
	listener   net.Listener
	port       int
	running    bool
	mu         sync.RWMutex
}

// NewProtectedMCPServer creates a new OAuth-protected mock MCP server
func NewProtectedMCPServer(config ProtectedMCPServerConfig) (*ProtectedMCPServer, error) {
	if config.Transport == "" {
		config.Transport = HTTPTransportStreamableHTTP
	}

	return &ProtectedMCPServer{
		config: config,
	}, nil
}

// Start starts the protected MCP server on a random available port
func (s *ProtectedMCPServer) Start(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return s.port, nil
	}

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("failed to listen: %w", err)
	}

	s.listener = listener
	s.port = listener.Addr().(*net.TCPAddr).Port

	// Create the protected HTTP handler
	handler, err := s.createProtectedHandler()
	if err != nil {
		listener.Close()
		return 0, fmt.Errorf("failed to create handler: %w", err)
	}

	s.httpServer = &http.Server{Handler: handler}

	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			if s.config.Debug {
				fmt.Fprintf(os.Stderr, "Protected MCP server error: %v\n", err)
			}
		}
	}()

	s.running = true

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔒 Protected MCP server %s started on port %d\n", s.config.Name, s.port)
	}

	return s.port, nil
}

// Stop stops the protected MCP server
func (s *ProtectedMCPServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔒 Stopping protected MCP server %s on port %d\n", s.config.Name, s.port)
	}

	err := s.httpServer.Shutdown(ctx)
	s.running = false
	return err
}

// Port returns the port the server is listening on
func (s *ProtectedMCPServer) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// IsRunning returns whether the server is currently running
func (s *ProtectedMCPServer) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Endpoint returns the MCP endpoint URL
func (s *ProtectedMCPServer) Endpoint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.running {
		return ""
	}

	switch s.config.Transport {
	case HTTPTransportSSE:
		return fmt.Sprintf("http://localhost:%d/sse", s.port)
	default:
		return fmt.Sprintf("http://localhost:%d/mcp", s.port)
	}
}

// GetIssuer returns the OAuth issuer for this server
func (s *ProtectedMCPServer) GetIssuer() string {
	if s.config.Issuer != "" {
		return s.config.Issuer
	}
	if s.config.OAuthServer != nil {
		return s.config.OAuthServer.GetIssuerURL()
	}
	return ""
}

// createProtectedHandler wraps the MCP handler with OAuth validation
func (s *ProtectedMCPServer) createProtectedHandler() (http.Handler, error) {
	// Create the underlying MCP server
	mcpServer := server.NewMCPServer(
		fmt.Sprintf("protected-%s", s.config.Name),
		"1.0.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
		server.WithPromptCapabilities(false),
	)

	// Create template engine for response rendering
	templateEngine := template.New()

	// Register tools if provided
	for _, toolConfig := range s.config.Tools {
		handler := NewToolHandler(toolConfig, templateEngine, s.config.Debug)
		if handler != nil {
			mcpServer.AddTool(handler.createMCPTool(), handler.createMCPHandler())
		}
	}

	var underlyingHandler http.Handler
	switch s.config.Transport {
	case HTTPTransportSSE:
		baseURL := fmt.Sprintf("http://localhost:%d", s.port)
		underlyingHandler = server.NewSSEServer(
			mcpServer,
			server.WithBaseURL(baseURL),
			server.WithSSEEndpoint("/sse"),
			server.WithMessageEndpoint("/message"),
		)
	default:
		underlyingHandler = server.NewStreamableHTTPServer(mcpServer)
	}

	// Create OAuth protection middleware
	protectedHandler := &oauthProtectionMiddleware{
		handler:       underlyingHandler,
		oauthServer:   s.config.OAuthServer,
		issuer:        s.GetIssuer(),
		requiredScope: s.config.RequiredScope,
		debug:         s.config.Debug,
	}

	// Create a mux to handle special endpoints
	mux := http.NewServeMux()

	// Serve OAuth protected resource metadata (RFC 9728)
	// This tells clients where to find the authorization server
	// Format matches real mcp-kubernetes (mcp-oauth library)
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		resourceURL := fmt.Sprintf("http://localhost:%d", s.port)
		metadata := map[string]interface{}{
			"resource":                 resourceURL,
			"authorization_servers":    []string{s.GetIssuer()},
			"bearer_methods_supported": []string{"header"},
		}
		if s.config.RequiredScope != "" {
			metadata["scopes_supported"] = []string{s.config.RequiredScope}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metadata)
	})

	// Pass all other requests to the protected handler
	mux.Handle("/", protectedHandler)

	return mux, nil
}

// oauthProtectionMiddleware validates OAuth tokens before passing to MCP handler
type oauthProtectionMiddleware struct {
	handler       http.Handler
	oauthServer   *OAuthServer
	issuer        string
	requiredScope string
	debug         bool
}

func (m *oauthProtectionMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check for Authorization header and extract Bearer token
	auth := r.Header.Get("Authorization")
	if auth == "" {
		if m.debug {
			fmt.Fprintf(os.Stderr, "🔒 Missing Authorization header, returning 401\n")
		}
		m.sendAuthChallenge(w, "invalid_token", "Missing Authorization header")
		return
	}

	token := ExtractBearerToken(auth)
	if token == "" {
		if m.debug {
			fmt.Fprintf(os.Stderr, "🔒 Invalid Authorization header format, returning 401\n")
		}
		m.sendAuthChallenge(w, "invalid_token", "Authorization header must use Bearer scheme")
		return
	}

	// Validate token with the OAuth server
	if m.oauthServer != nil {
		if !m.oauthServer.ValidateToken(token) {
			if m.debug {
				fmt.Fprintf(os.Stderr, "🔒 Token validation failed, returning 401\n")
			}
			m.sendAuthChallenge(w, "invalid_token", "The access token is invalid or expired")
			return
		}

		// Check scope if required
		if m.requiredScope != "" {
			tokenInfo := m.oauthServer.GetTokenInfo(token)
			if tokenInfo != nil && !strings.Contains(tokenInfo.Scope, m.requiredScope) {
				if m.debug {
					fmt.Fprintf(os.Stderr, "🔒 Token missing required scope %s, returning 403\n", m.requiredScope)
				}
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte("insufficient_scope"))
				return
			}
		}
	}

	if m.debug {
		fmt.Fprintf(os.Stderr, "🔒 Token validated, passing request to MCP handler\n")
	}

	// Token valid - pass through to MCP handler
	m.handler.ServeHTTP(w, r)
}

func (m *oauthProtectionMiddleware) sendAuthChallenge(w http.ResponseWriter, errorCode, errorDesc string) {
	// Send WWW-Authenticate header per RFC 9728
	// Format matches real mcp-kubernetes (mcp-oauth library):
	// Bearer resource_metadata=".../.well-known/oauth-protected-resource", error="...", error_description="..."
	resourceMetadataURL := fmt.Sprintf("%s/.well-known/oauth-protected-resource", m.issuer)
	authHeader := fmt.Sprintf(`Bearer resource_metadata="%s"`, resourceMetadataURL)

	if errorCode != "" {
		authHeader += fmt.Sprintf(`, error="%s"`, errorCode)
	}
	if errorDesc != "" {
		authHeader += fmt.Sprintf(`, error_description="%s"`, errorDesc)
	}

	w.Header().Set("WWW-Authenticate", authHeader)
	w.WriteHeader(http.StatusUnauthorized)
}

// WaitForReady waits for the server to be ready to accept connections
func (s *ProtectedMCPServer) WaitForReady(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if s.IsRunning() {
				// Try to connect to verify it's really ready
				conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", s.Port()), 1*time.Second)
				if err == nil {
					conn.Close()
					return nil
				}
			}
		}
	}
}

// GetName returns the server name
func (s *ProtectedMCPServer) GetName() string {
	return s.config.Name
}
