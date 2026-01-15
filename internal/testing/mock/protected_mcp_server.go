package mock

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

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

	// Register tools if provided
	for _, toolConfig := range s.config.Tools {
		handler := NewToolHandler(toolConfig, nil, s.config.Debug)
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

	// Wrap with OAuth protection middleware
	return &oauthProtectionMiddleware{
		handler:       underlyingHandler,
		oauthServer:   s.config.OAuthServer,
		issuer:        s.GetIssuer(),
		requiredScope: s.config.RequiredScope,
		debug:         s.config.Debug,
	}, nil
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
	// Check for Authorization header
	auth := r.Header.Get("Authorization")
	if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
		if m.debug {
			fmt.Fprintf(os.Stderr, "🔒 No/invalid Authorization header, returning 401\n")
		}
		m.sendAuthChallenge(w)
		return
	}

	token := strings.TrimPrefix(auth, "Bearer ")

	// Validate token with the OAuth server
	if m.oauthServer != nil {
		if !m.oauthServer.ValidateToken(token) {
			if m.debug {
				fmt.Fprintf(os.Stderr, "🔒 Token validation failed, returning 401\n")
			}
			m.sendAuthChallenge(w)
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

func (m *oauthProtectionMiddleware) sendAuthChallenge(w http.ResponseWriter) {
	// Send WWW-Authenticate header per RFC 9728
	authHeader := fmt.Sprintf(`Bearer realm="%s"`, m.issuer)
	if m.requiredScope != "" {
		authHeader += fmt.Sprintf(`, scope="%s"`, m.requiredScope)
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
