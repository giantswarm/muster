package admin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Deps is the surface the admin package needs from the rest of muster. The
// aggregator package wires these callbacks up against its internal stores;
// tests inject fakes directly.
type Deps struct {
	// ListSessions returns summary rows for every known session.
	ListSessions func(ctx context.Context) ([]SessionSummary, error)

	// GetSessionDetail returns the detail view for a single session, or nil
	// + false when the session is unknown.
	GetSessionDetail func(ctx context.Context, sessionID string) (*SessionDetail, bool, error)

	// DeleteSession revokes auth state, clears capability caches, evicts
	// pooled connections, and clears upstream tokens for the session.
	DeleteSession func(ctx context.Context, sessionID string) error

	// ReconnectServer tears down all per-server state (auth, caps, pool,
	// upstream token) and immediately re-runs SSO so the server comes back
	// online with a fresh bearer. Used by the admin UI's per-server
	// "Reconnect" button.
	ReconnectServer func(ctx context.Context, sessionID, serverName string) error
}

// Config configures the admin listener.
type Config struct {
	BindAddress string // default "127.0.0.1"
	Port        int    // default 9999
}

// Server owns the admin HTTP listener.
type Server struct {
	cfg  Config
	deps Deps
	tmpl viewSet
	http *http.Server
}

// NewServer constructs an admin server. Call Start to begin serving.
func NewServer(cfg Config, deps Deps) (*Server, error) {
	if deps.ListSessions == nil || deps.GetSessionDetail == nil ||
		deps.DeleteSession == nil || deps.ReconnectServer == nil {
		return nil, errors.New("admin.NewServer: all Deps callbacks are required")
	}
	if cfg.BindAddress == "" {
		cfg.BindAddress = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 9999
	}

	tmpl, err := parseTemplates()
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	s := &Server{cfg: cfg, deps: deps, tmpl: tmpl}
	s.http = &http.Server{
		Addr:              net.JoinHostPort(cfg.BindAddress, fmt.Sprintf("%d", cfg.Port)),
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s, nil
}

// Addr reports the listener address, useful in tests.
func (s *Server) Addr() string { return s.http.Addr }

// Start begins serving in a goroutine and returns immediately. It returns an
// error only if the listener cannot be bound.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("admin listen %s: %w", s.http.Addr, err)
	}
	go func() {
		_ = s.http.Serve(ln)
	}()
	return nil
}

// Stop shuts down the admin listener with a brief grace period.
func (s *Server) Stop(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.http.Shutdown(shutdownCtx)
}

// routes builds the request router. Static assets live under /static/;
// everything else renders HTML templates.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/sessions", http.StatusSeeOther)
	})

	mux.HandleFunc("GET /sessions", s.handleList)
	mux.HandleFunc("GET /sessions/{id}", s.handleDetail)
	mux.HandleFunc("POST /sessions/{id}/delete", s.handleDelete)
	mux.HandleFunc("POST /sessions/{id}/servers/{name}/reconnect", s.handleReconnect)

	return mux
}
