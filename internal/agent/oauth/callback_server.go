package oauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/giantswarm/pagekit"

	"github.com/giantswarm/muster/v5/internal/netdiag"
	"github.com/giantswarm/muster/v5/internal/signinpage"
)

// DefaultCallbackPort is the default port for the local OAuth callback server.
const DefaultCallbackPort = 3000

// CallbackTimeout is how long to wait for the OAuth callback.
const CallbackTimeout = 10 * time.Minute

// CallbackResult represents the result of an OAuth callback.
type CallbackResult struct {
	// Code is the authorization code from the OAuth provider.
	Code string

	// State is the state parameter to verify against the original request.
	State string

	// Iss is the RFC 9207 issuer identifier returned in the authorization
	// response. Non-conforming servers omit it; callers must treat empty
	// as "not advertised" rather than "did not match".
	Iss string

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
// It starts, waits for a single callback, shows the sign-in's outcome, then
// shuts down.
type CallbackServer struct {
	port      int
	server    *http.Server
	listeners []net.Listener
	resultCh  chan *CallbackResult
	errorCh   chan error
	once      sync.Once
	serverURL string

	// finishOnce sets outcomeStatus and outcome, the page every callback
	// request shows, and then closes finished.
	finishOnce    sync.Once
	finished      chan struct{}
	outcomeStatus int
	outcome       pagekit.Page
}

const (
	// callbackWriteTimeout bounds writing a callback response.
	callbackWriteTimeout = 30 * time.Second
	// outcomeWait bounds how long a callback request waits for the sign-in's
	// outcome, leaving time to write the page within callbackWriteTimeout.
	outcomeWait = callbackWriteTimeout - 10*time.Second
	// stopAfterPage is how long the server stays up after the first
	// callback's page, for a caller that does not stop it.
	stopAfterPage = time.Second
)

// NewCallbackServer creates a new callback server on the specified port.
// If port is 0, DefaultCallbackPort is used.
func NewCallbackServer(port int) *CallbackServer {
	if port == 0 {
		port = DefaultCallbackPort
	}

	return &CallbackServer{
		port:     port,
		resultCh: make(chan *CallbackResult, 1),
		errorCh:  make(chan error, 1),
		finished: make(chan struct{}),
	}
}

// callbackHosts are the loopback addresses the callback listens on. The
// redirect URI names localhost, and a browser resolves localhost to either
// family, IPv6 first on most systems: a listener on one family only lets a
// process on the other family's loopback receive the callback.
var callbackHosts = []string{"127.0.0.1", "::1"}

// PortInUseError reports that another socket holds the callback port on one
// of the loopback addresses, so the browser's callback would not reach this
// process.
type PortInUseError struct {
	// Addr is the address the bind failed on.
	Addr string
	// Port is the callback port.
	Port int
	// Holders lists the sockets on the port with their owning process, where
	// visible; empty when the system does not expose them.
	Holders string
	// Err is the bind error.
	Err error
}

func (e *PortInUseError) Error() string {
	msg := fmt.Sprintf("callback port %d is already in use on %s", e.Port, e.Addr)
	if e.Holders != "" {
		msg += " by:\n  " + strings.ReplaceAll(strings.TrimRight(e.Holders, "\n"), "\n", "\n  ")
	}
	return msg
}

func (e *PortInUseError) Unwrap() error { return e.Err }

// Start starts the callback server and begins listening for the OAuth callback
// on the port on both loopback addresses. A host without IPv6 loopback listens
// on 127.0.0.1 alone. The port taken on either address is a *PortInUseError.
// The server will automatically stop when the context is cancelled.
// Returns the callback URL to use in the OAuth authorization request.
func (s *CallbackServer) Start(ctx context.Context) (string, error) {
	for _, host := range callbackHosts {
		addr := net.JoinHostPort(host, strconv.Itoa(s.port))
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			if host != callbackHosts[0] && !isAddrInUse(err) {
				// No IPv6 loopback here: localhost resolves to 127.0.0.1 only.
				continue
			}
			s.Stop()
			if isAddrInUse(err) {
				return "", &PortInUseError{Addr: addr, Port: s.port, Holders: netdiag.PortOccupants(s.port), Err: err}
			}
			return "", fmt.Errorf("failed to start callback server on %s: %w", addr, err)
		}
		s.listeners = append(s.listeners, listener)
	}

	s.serverURL = fmt.Sprintf("http://localhost:%d", s.port)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", s.handleCallback)

	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      callbackWriteTimeout,
		IdleTimeout:       60 * time.Second,
	}

	// Start serving in a goroutine per listener
	for _, listener := range s.listeners {
		go func(l net.Listener) {
			if err := s.server.Serve(l); err != nil && err != http.ErrServerClosed {
				select {
				case s.errorCh <- err:
				default:
				}
			}
		}(listener)
	}

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

// handleCallback handles the OAuth callback request. The page waits for the
// sign-in's outcome, which the client reports with Finish once it has checked
// the response and exchanged the code.
func (s *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	// Only the first callback reaches the client. Browsers and redirect pages
	// may load the callback twice; a later load shows the same outcome.
	first := false
	s.once.Do(func() {
		first = true
		s.processCallback(r)
	})

	timer := time.NewTimer(outcomeWait)
	defer timer.Stop()
	select {
	case <-s.finished:
		pagekit.Render(w, s.outcomeStatus, s.outcome)
	case <-timer.C:
		pagekit.Render(w, http.StatusAccepted, signinpage.Pending("Muster"))
	case <-r.Context().Done():
	}

	if first {
		time.AfterFunc(stopAfterPage, s.Stop)
	}
}

// processCallback hands the callback's parameters to the client. This is
// called exactly once via sync.Once.
func (s *CallbackServer) processCallback(r *http.Request) {
	query := r.URL.Query()
	result := &CallbackResult{
		Code:             query.Get("code"),
		State:            query.Get("state"),
		Iss:              query.Get("iss"),
		Error:            query.Get("error"),
		ErrorDescription: query.Get("error_description"),
	}

	select {
	case s.resultCh <- result:
	default:
	}
}

// Finish records the sign-in's outcome as the page the callback shows. Only
// the first call counts.
func (s *CallbackServer) Finish(status int, page pagekit.Page) {
	s.finishOnce.Do(func() {
		s.outcomeStatus = status
		s.outcome = page
		close(s.finished)
	})
}

// Stop gracefully shuts down the callback server. A callback page still
// waiting for an outcome reports the sign-in as cancelled.
func (s *CallbackServer) Stop() {
	s.Finish(http.StatusBadRequest, signinpage.Failed("The sign-in was cancelled", ""))
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(ctx)
	}
	for _, listener := range s.listeners {
		_ = listener.Close()
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
