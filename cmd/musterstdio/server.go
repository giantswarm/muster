package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// BridgeIface is the surface Server consumes from Bridge. It exists so tests
// can substitute a fake without spawning a real child process.
type BridgeIface interface {
	Send(ctx context.Context, frame []byte) ([]byte, error)
	IsHealthy() bool
	Stop(timeout time.Duration) error
}

// Config configures a Server.
type Config struct {
	// Bridge handles JSON-RPC framing to the child. Required.
	Bridge BridgeIface
	// ListenAddr is the host:port for the main HTTP listener (POST /mcp and
	// GET /healthz when HealthAddr is empty).
	ListenAddr string
	// HealthAddr, when non-empty, hosts a second listener that serves only
	// GET /healthz.
	HealthAddr string
	// MaxRequestBytes caps the size of incoming POST /mcp bodies. Zero uses
	// a 1 MiB default.
	MaxRequestBytes int64
	// SendTimeout caps how long a single POST /mcp waits on the child.
	// Zero uses a 60s default.
	SendTimeout time.Duration
	// Logger receives structured diagnostics. Required.
	Logger *slog.Logger
}

// Server exposes the shim's HTTP surface.
type Server struct {
	cfg Config

	main   *http.Server
	health *http.Server

	mainAddr   net.Addr
	healthAddr net.Addr

	inFlight atomic.Int64

	startOnce sync.Once
	stopOnce  sync.Once
	stopped   chan struct{}
}

// NewServer constructs a Server. It validates Config but does not bind any
// listener until Start is called.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Bridge == nil {
		return nil, errors.New("server: bridge is required")
	}
	if cfg.ListenAddr == "" {
		return nil, errors.New("server: listen address is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxRequestBytes == 0 {
		cfg.MaxRequestBytes = 1 << 20
	}
	if cfg.SendTimeout == 0 {
		cfg.SendTimeout = 60 * time.Second
	}
	return &Server{cfg: cfg, stopped: make(chan struct{})}, nil
}

// Start binds the listeners and serves in background goroutines. Errors from
// the listeners are routed through ctx done via shutdown.
func (s *Server) Start(ctx context.Context) error {
	var startErr error
	s.startOnce.Do(func() {
		mainListener, err := net.Listen("tcp", s.cfg.ListenAddr)
		if err != nil {
			startErr = fmt.Errorf("listen on %s: %w", s.cfg.ListenAddr, err)
			return
		}
		s.mainAddr = mainListener.Addr()
		mainMux := http.NewServeMux()
		mainMux.HandleFunc("/mcp", s.handleMCP)
		if s.cfg.HealthAddr == "" {
			mainMux.HandleFunc("/healthz", s.handleHealth)
		}
		s.main = &http.Server{
			Handler:           mainMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			if err := s.main.Serve(mainListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.cfg.Logger.Error("main listener exited", "err", err)
			}
		}()

		if s.cfg.HealthAddr != "" {
			healthListener, err := net.Listen("tcp", s.cfg.HealthAddr)
			if err != nil {
				_ = s.main.Close()
				startErr = fmt.Errorf("listen on %s: %w", s.cfg.HealthAddr, err)
				return
			}
			s.healthAddr = healthListener.Addr()
			healthMux := http.NewServeMux()
			healthMux.HandleFunc("/healthz", s.handleHealth)
			s.health = &http.Server{
				Handler:           healthMux,
				ReadHeaderTimeout: 5 * time.Second,
			}
			go func() {
				if err := s.health.Serve(healthListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					s.cfg.Logger.Error("health listener exited", "err", err)
				}
			}()
		}
	})
	return startErr
}

// Shutdown stops accepting new requests, waits for in-flight requests to
// drain (bounded by ctx), and closes the listeners. Subsequent calls are
// no-ops.
func (s *Server) Shutdown(ctx context.Context) error {
	var err error
	s.stopOnce.Do(func() {
		defer close(s.stopped)
		if s.main != nil {
			err = s.main.Shutdown(ctx)
		}
		if s.health != nil {
			if e := s.health.Shutdown(ctx); err == nil {
				err = e
			}
		}
	})
	return err
}

// InFlight returns the count of POST /mcp requests currently being processed.
func (s *Server) InFlight() int64 { return s.inFlight.Load() }

// Addr returns the address the main HTTP listener is bound to. Returns nil
// before Start succeeds.
func (s *Server) Addr() net.Addr { return s.mainAddr }

// HealthAddr returns the address the separate /healthz listener is bound to,
// or nil if /healthz is served on the main listener.
func (s *Server) HealthAddr() net.Addr { return s.healthAddr }

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.cfg.Bridge.IsHealthy() {
		writeJSONRPCError(w, http.StatusServiceUnavailable, "child unavailable")
		return
	}

	s.inFlight.Add(1)
	defer s.inFlight.Add(-1)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBytes))
	if err != nil {
		writeJSONRPCError(w, http.StatusBadRequest, fmt.Sprintf("read request body: %s", err))
		return
	}
	if len(body) == 0 {
		writeJSONRPCError(w, http.StatusBadRequest, "empty request body")
		return
	}
	if !json.Valid(body) {
		writeJSONRPCError(w, http.StatusBadRequest, "request body is not valid JSON")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.SendTimeout)
	defer cancel()

	resp, err := s.cfg.Bridge.Send(ctx, body)
	if err != nil {
		switch {
		case errors.Is(err, errInvalidFrame):
			writeJSONRPCError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, errBridgeUnavailable):
			writeJSONRPCError(w, http.StatusServiceUnavailable, err.Error())
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			writeJSONRPCError(w, http.StatusGatewayTimeout, err.Error())
		default:
			writeJSONRPCError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.cfg.Bridge.IsHealthy() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unhealthy"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func writeJSONRPCError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    -32000,
			"message": msg,
		},
		"id": nil,
	})
	_, _ = w.Write(body)
}
