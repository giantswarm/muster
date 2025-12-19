package mock

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

// HTTPTransportType represents the type of HTTP transport for mock servers
type HTTPTransportType string

const (
	// HTTPTransportStreamableHTTP uses streamable HTTP protocol
	HTTPTransportStreamableHTTP HTTPTransportType = "streamable-http"
	// HTTPTransportSSE uses Server-Sent Events protocol
	HTTPTransportSSE HTTPTransportType = "sse"
)

// HTTPServer wraps a mock MCP server with HTTP transport capabilities.
// It can serve either SSE or streamable-http transport types.
type HTTPServer struct {
	mockServer    *Server
	httpServer    *http.Server
	sseServer     *server.SSEServer
	listener      net.Listener
	transport     HTTPTransportType
	port          int
	mu            sync.RWMutex
	running       bool
	debug         bool
	shutdownError error
}

// NewHTTPServer creates a new HTTP mock server from an existing mock server
func NewHTTPServer(mockServer *Server, transport HTTPTransportType, debug bool) *HTTPServer {
	return &HTTPServer{
		mockServer: mockServer,
		transport:  transport,
		debug:      debug,
	}
}

// NewHTTPServerFromConfig creates a new HTTP mock server from a config file
func NewHTTPServerFromConfig(configPath string, transport HTTPTransportType, debug bool) (*HTTPServer, error) {
	mockServer, err := NewServerFromFile(configPath, debug)
	if err != nil {
		return nil, fmt.Errorf("failed to create mock server from config: %w", err)
	}

	return NewHTTPServer(mockServer, transport, debug), nil
}

// Start starts the HTTP server on a dynamically allocated port.
// Returns the port number the server is listening on.
func (s *HTTPServer) Start(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return s.port, nil
	}

	// Find an available port by listening on :0
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("failed to find available port: %w", err)
	}

	s.listener = listener
	s.port = listener.Addr().(*net.TCPAddr).Port

	if s.debug {
		fmt.Fprintf(os.Stderr, "🌐 Starting mock HTTP server (%s) on port %d\n", s.transport, s.port)
	}

	// Create the appropriate transport handler
	var handler http.Handler
	switch s.transport {
	case HTTPTransportSSE:
		baseURL := fmt.Sprintf("http://localhost:%d", s.port)
		s.sseServer = server.NewSSEServer(
			s.mockServer.mcpServer,
			server.WithBaseURL(baseURL),
			server.WithSSEEndpoint("/sse"),
			server.WithMessageEndpoint("/message"),
			server.WithKeepAlive(true),
			server.WithKeepAliveInterval(30*time.Second),
		)
		handler = s.sseServer

	case HTTPTransportStreamableHTTP:
		fallthrough
	default:
		handler = server.NewStreamableHTTPServer(s.mockServer.mcpServer)
	}

	s.httpServer = &http.Server{
		Handler: handler,
	}

	// Start serving in background
	go func() {
		if err := s.httpServer.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			s.mu.Lock()
			s.shutdownError = err
			s.mu.Unlock()
			if s.debug {
				fmt.Fprintf(os.Stderr, "❌ Mock HTTP server error: %v\n", err)
			}
		}
	}()

	s.running = true

	if s.debug {
		fmt.Fprintf(os.Stderr, "✅ Mock HTTP server started on port %d with %s transport\n", s.port, s.transport)
	}

	return s.port, nil
}

// StartOnPort starts the HTTP server on a specific port.
// This is useful when you need to control the port (e.g., for deterministic testing).
func (s *HTTPServer) StartOnPort(ctx context.Context, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		if s.port == port {
			return nil
		}
		return fmt.Errorf("server already running on port %d", s.port)
	}

	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	s.listener = listener
	s.port = port

	if s.debug {
		fmt.Fprintf(os.Stderr, "🌐 Starting mock HTTP server (%s) on port %d\n", s.transport, s.port)
	}

	// Create the appropriate transport handler
	var handler http.Handler
	switch s.transport {
	case HTTPTransportSSE:
		baseURL := fmt.Sprintf("http://localhost:%d", s.port)
		s.sseServer = server.NewSSEServer(
			s.mockServer.mcpServer,
			server.WithBaseURL(baseURL),
			server.WithSSEEndpoint("/sse"),
			server.WithMessageEndpoint("/message"),
			server.WithKeepAlive(true),
			server.WithKeepAliveInterval(30*time.Second),
		)
		handler = s.sseServer

	case HTTPTransportStreamableHTTP:
		fallthrough
	default:
		handler = server.NewStreamableHTTPServer(s.mockServer.mcpServer)
	}

	s.httpServer = &http.Server{
		Handler: handler,
	}

	// Start serving in background
	go func() {
		if err := s.httpServer.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			s.mu.Lock()
			s.shutdownError = err
			s.mu.Unlock()
			if s.debug {
				fmt.Fprintf(os.Stderr, "❌ Mock HTTP server error: %v\n", err)
			}
		}
	}()

	s.running = true

	if s.debug {
		fmt.Fprintf(os.Stderr, "✅ Mock HTTP server started on port %d with %s transport\n", s.port, s.transport)
	}

	return nil
}

// Stop gracefully shuts down the HTTP server
func (s *HTTPServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	if s.debug {
		fmt.Fprintf(os.Stderr, "🛑 Stopping mock HTTP server on port %d\n", s.port)
	}

	// Use shutdown context with timeout
	shutdownCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		shutdownCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			// Force close if graceful shutdown fails
			s.httpServer.Close()
			if s.debug {
				fmt.Fprintf(os.Stderr, "⚠️  Force closed mock HTTP server: %v\n", err)
			}
		}
	}

	s.running = false
	s.httpServer = nil
	s.sseServer = nil

	if s.debug {
		fmt.Fprintf(os.Stderr, "✅ Mock HTTP server stopped\n")
	}

	return nil
}

// Port returns the port the server is listening on
func (s *HTTPServer) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// IsRunning returns whether the server is currently running
func (s *HTTPServer) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Transport returns the transport type used by the server
func (s *HTTPServer) Transport() HTTPTransportType {
	return s.transport
}

// Endpoint returns the full endpoint URL for the server
func (s *HTTPServer) Endpoint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.running {
		return ""
	}

	switch s.transport {
	case HTTPTransportSSE:
		return fmt.Sprintf("http://localhost:%d/sse", s.port)
	case HTTPTransportStreamableHTTP:
		fallthrough
	default:
		return fmt.Sprintf("http://localhost:%d/mcp", s.port)
	}
}

// GetError returns any error that occurred during server operation
func (s *HTTPServer) GetError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.shutdownError
}

// WaitForReady waits for the server to be ready to accept connections
func (s *HTTPServer) WaitForReady(ctx context.Context) error {
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
