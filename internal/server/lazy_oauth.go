package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	oauth "github.com/giantswarm/mcp-oauth"

	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
)

const (
	oidcInitialBackoff = 1 * time.Second
	oidcMaxBackoff     = 30 * time.Second
)

// LazyOAuthHTTPServer wraps OAuthHTTPServer construction behind a background retry loop.
// The HTTP handler surface is available immediately but returns 503 until OIDC discovery
// against the upstream Dex/OIDC issuer succeeds. All non-OAuth paths (MCP aggregation,
// reconcilers) are unaffected and start immediately.
type LazyOAuthHTTPServer struct {
	cfg        config.OAuthServerConfig
	mcpHandler http.Handler
	debug      bool
	serverOpts []oauth.ServerOption

	mu       sync.RWMutex
	inner    *OAuthHTTPServer
	innerMux http.Handler

	ready  chan struct{}
	done   chan struct{} // closed when discoveryLoop exits
	ctx    context.Context
	cancel context.CancelFunc

	onAuthenticated func(ctx context.Context, sessionID string)
}

// NewLazyOAuthHTTPServer creates a lazy OAuth HTTP server that starts a background
// goroutine to perform OIDC discovery with exponential backoff. It always returns
// successfully; the caller gets a handler that serves 503 until discovery succeeds.
func NewLazyOAuthHTTPServer(ctx context.Context, cfg config.OAuthServerConfig, mcpHandler http.Handler, debug bool, opts ...oauth.ServerOption) *LazyOAuthHTTPServer {
	lctx, cancel := context.WithCancel(ctx)
	l := &LazyOAuthHTTPServer{
		cfg:        cfg,
		mcpHandler: mcpHandler,
		debug:      debug,
		serverOpts: opts,
		ready:      make(chan struct{}),
		done:       make(chan struct{}),
		ctx:        lctx,
		cancel:     cancel,
	}
	go l.discoveryLoop()
	return l
}

// discoveryLoop retries NewOAuthHTTPServer with exponential backoff until it succeeds
// or the context is cancelled.
func (l *LazyOAuthHTTPServer) discoveryLoop() {
	defer close(l.done)
	backoff := oidcInitialBackoff

	for {
		inner, err := NewOAuthHTTPServer(l.cfg, l.mcpHandler, l.debug, l.serverOpts...)
		if err == nil {
			l.mu.Lock()
			l.inner = inner
			if l.onAuthenticated != nil {
				inner.SetOnAuthenticated(l.onAuthenticated)
			}
			l.innerMux = inner.CreateMux()
			l.mu.Unlock()

			close(l.ready)
			logging.Info("OAuth", "OIDC discovery succeeded — OAuth server is ready (provider=%s)", l.cfg.Provider)
			return
		}

		logging.Warn("OAuth", "OIDC discovery failed, retrying in %s: %v", backoff, err)

		select {
		case <-l.ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > oidcMaxBackoff {
			backoff = oidcMaxBackoff
		}
	}
}

// SetOnAuthenticated stores the callback and forwards it to the inner server once ready.
// Safe to call before or after discovery completes.
func (l *LazyOAuthHTTPServer) SetOnAuthenticated(fn func(context.Context, string)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onAuthenticated = fn
	if l.inner != nil {
		l.inner.SetOnAuthenticated(fn)
	}
}

// ValidateTokenWithSubject returns a middleware that delegates to the inner server once
// OIDC discovery has succeeded. Before that it returns 503.
func (l *LazyOAuthHTTPServer) ValidateTokenWithSubject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.RLock()
		inner := l.inner
		l.mu.RUnlock()
		if inner == nil {
			writeOIDCPending(w)
			return
		}
		inner.ValidateTokenWithSubject(next).ServeHTTP(w, r)
	})
}

// CreateMux returns an http.Handler that proxies to the inner mux once ready.
// Before OIDC discovery succeeds, /health returns a degraded-status JSON body
// and all other paths return 503.
func (l *LazyOAuthHTTPServer) CreateMux() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.RLock()
		mux := l.innerMux
		l.mu.RUnlock()

		if mux != nil {
			mux.ServeHTTP(w, r)
			return
		}

		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"degraded","reason":"oidc-discovery-pending"}`))
			return
		}

		writeOIDCPending(w)
	})
}

// Shutdown stops the discovery loop and shuts down the inner server if it was created.
// It waits for the background goroutine to exit so the caller can be sure no new inner
// server connections will be opened after Shutdown returns.
func (l *LazyOAuthHTTPServer) Shutdown(ctx context.Context) error {
	l.cancel()
	select {
	case <-l.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	l.mu.RLock()
	inner := l.inner
	l.mu.RUnlock()
	if inner != nil {
		return inner.Shutdown(ctx)
	}
	return nil
}

// RefreshSession forces an in-process upstream provider token refresh for the given
// token family. Returns an error if OIDC discovery has not yet completed.
func (l *LazyOAuthHTTPServer) RefreshSession(ctx context.Context, familyID string) error {
	l.mu.RLock()
	inner := l.inner
	l.mu.RUnlock()
	if inner == nil {
		return fmt.Errorf("OIDC discovery not yet complete, cannot refresh session")
	}
	return inner.RefreshSession(ctx, familyID)
}

// WaitReady blocks until OIDC discovery succeeds or the context is cancelled.
// Intended for tests and health-check endpoints that need to synchronise on readiness.
func (l *LazyOAuthHTTPServer) WaitReady(ctx context.Context) error {
	select {
	case <-l.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeOIDCPending(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "30")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"service_unavailable","error_description":"OIDC discovery in progress, please retry"}`))
}
