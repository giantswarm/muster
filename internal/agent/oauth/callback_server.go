package oauth

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"sync"
	"time"
)

// DefaultCallbackPort is the default port for the local OAuth callback server.
const DefaultCallbackPort = 3000

// CallbackTimeout is how long to wait for the OAuth callback.
const CallbackTimeout = 10 * time.Minute

//go:embed templates/callback_success.html
var callbackSuccessHTML string

//go:embed templates/callback_error.html
var callbackErrorHTML string

// CallbackResult represents the result of an OAuth callback.
type CallbackResult struct {
	// Code is the authorization code from the OAuth provider.
	Code string

	// State is the state parameter to verify against the original request.
	State string

	// Error is the error code if the authorization failed.
	Error string

	// ErrorDescription is a human-readable error description.
	ErrorDescription string
}

// IsError returns true if the callback result represents an error.
func (r *CallbackResult) IsError() bool {
	return r.Error != ""
}

// CallbackServer is a temporary local HTTP server for receiving OAuth callbacks.
// It starts, waits for a single callback, then shuts down.
type CallbackServer struct {
	port      int
	server    *http.Server
	listener  net.Listener
	resultCh  chan *CallbackResult
	errorCh   chan error
	once      sync.Once
	serverURL string
}

// NewCallbackServer creates a new callback server on the specified port.
// If port is 0, a random available port will be used.
func NewCallbackServer(port int) *CallbackServer {
	if port == 0 {
		port = DefaultCallbackPort
	}

	return &CallbackServer{
		port:     port,
		resultCh: make(chan *CallbackResult, 1),
		errorCh:  make(chan error, 1),
	}
}

// Start starts the callback server and begins listening for the OAuth callback.
// The server will automatically stop when the context is cancelled.
// Returns the callback URL to use in the OAuth authorization request.
func (s *CallbackServer) Start(ctx context.Context) (string, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("failed to start callback server on %s: %w", addr, err)
	}

	s.listener = listener
	s.port = listener.Addr().(*net.TCPAddr).Port
	s.serverURL = fmt.Sprintf("http://localhost:%d", s.port)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", s.handleCallback)

	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start serving in a goroutine
	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			select {
			case s.errorCh <- err:
			default:
			}
		}
	}()

	// Monitor context for cancellation and stop server when cancelled
	go func() {
		<-ctx.Done()
		s.Stop()
	}()

	return s.serverURL + "/callback", nil
}

// WaitForCallback waits for the OAuth callback or timeout.
// Returns the callback result or an error if the callback fails or times out.
func (s *CallbackServer) WaitForCallback(ctx context.Context) (*CallbackResult, error) {
	select {
	case result := <-s.resultCh:
		return result, nil
	case err := <-s.errorCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// handleCallback handles the OAuth callback request.
func (s *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	// Only handle once - use sync.Once to ensure idempotency
	var handled bool
	s.once.Do(func() {
		handled = true
		s.processCallback(w, r)
	})

	if !handled {
		http.Error(w, "Callback already processed", http.StatusBadRequest)
	}
}

// processCallback processes the OAuth callback request.
// This is called exactly once via sync.Once.
func (s *CallbackServer) processCallback(w http.ResponseWriter, r *http.Request) {
	// Set security headers
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")

	// Parse query parameters
	query := r.URL.Query()
	result := &CallbackResult{
		Code:             query.Get("code"),
		State:            query.Get("state"),
		Error:            query.Get("error"),
		ErrorDescription: query.Get("error_description"),
	}

	// Render appropriate HTML response
	var tmpl *template.Template
	var data interface{}

	if result.IsError() {
		tmpl = template.Must(template.New("error").Parse(callbackErrorHTML))
		data = map[string]string{
			"Error":       result.Error,
			"Description": result.ErrorDescription,
		}
	} else {
		tmpl = template.Must(template.New("success").Parse(callbackSuccessHTML))
		data = map[string]string{}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}

	// Send result to channel
	select {
	case s.resultCh <- result:
	default:
	}

	// Schedule server shutdown after giving time for response to be sent
	go func() {
		time.Sleep(1 * time.Second)
		s.Stop()
	}()
}

// Stop gracefully shuts down the callback server.
func (s *CallbackServer) Stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(ctx)
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

// GetRedirectURI returns the redirect URI for OAuth configuration.
func (s *CallbackServer) GetRedirectURI() string {
	return s.serverURL + "/callback"
}

// GetPort returns the port the server is listening on.
func (s *CallbackServer) GetPort() int {
	return s.port
}
