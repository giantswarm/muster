package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// version is overridden by build-time ldflags so the shim reports the muster
// release it was built from.
var version = "dev"

// Options aggregates the CLI flag values for the shim. Each field has a
// dedicated flag bound in Run.
type Options struct {
	// ChildCommand is the executable to spawn (path or PATH-resolvable name).
	ChildCommand string
	// ChildArgs are arguments passed to the child, in order.
	ChildArgs sliceFlag
	// ChildEnv contains KEY=VALUE pairs prepended to the child's environment.
	ChildEnv sliceFlag
	// ListenPort is the TCP port for the main HTTP listener.
	ListenPort int
	// HealthPort, when non-zero and different from ListenPort, exposes
	// /healthz on its own listener. Zero means "serve on the main port".
	HealthPort int
	// ShutdownTimeout bounds how long Run waits for in-flight requests and
	// the child to drain after a shutdown signal.
	ShutdownTimeout time.Duration
	// StartupTimeout bounds how long Run waits for the child to come up and
	// the HTTP listener to bind before declaring failure.
	StartupTimeout time.Duration
	// MaxRequestBytes caps the POST /mcp request body size.
	MaxRequestBytes int64
	// PrintVersion makes the binary print the version string and exit.
	PrintVersion bool
}

// Run is the testable entry point: it parses flags, spawns the child, starts
// the HTTP server, and blocks until ctx is canceled or a fatal error occurs.
func Run(ctx context.Context, args []string, stderr io.Writer, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(stderr, nil))
	}
	opts, err := parseFlags(args, stderr)
	if err != nil {
		return err
	}
	if opts.PrintVersion {
		_, _ = fmt.Fprintln(stderr, "musterstdio "+version)
		return nil
	}
	if opts.ChildCommand == "" {
		return errors.New("flag --child-command is required")
	}

	bridge := NewBridge(BridgeOptions{
		Command: opts.ChildCommand,
		Args:    opts.ChildArgs,
		Env:     parseEnvPairs(opts.ChildEnv),
		Logger:  logger,
	})
	startCtx, cancelStart := context.WithTimeout(ctx, opts.StartupTimeout)
	if err := bridge.Start(startCtx); err != nil {
		cancelStart()
		return fmt.Errorf("start child: %w", err)
	}
	cancelStart()

	srv, err := NewServer(Config{
		Bridge:          bridge,
		ListenAddr:      fmt.Sprintf("0.0.0.0:%d", opts.ListenPort),
		HealthAddr:      healthAddr(opts),
		MaxRequestBytes: opts.MaxRequestBytes,
		Logger:          logger,
	})
	if err != nil {
		_ = bridge.Stop(opts.ShutdownTimeout)
		return fmt.Errorf("construct server: %w", err)
	}
	if err := srv.Start(ctx); err != nil {
		_ = bridge.Stop(opts.ShutdownTimeout)
		return fmt.Errorf("start server: %w", err)
	}

	readyArgs := []any{
		"version", version,
		"listen_addr", srv.Addr().String(),
		"child", opts.ChildCommand,
	}
	if hAddr := srv.HealthAddr(); hAddr != nil {
		readyArgs = append(readyArgs, "health_addr", hAddr.String())
	}
	logger.Info("musterstdio ready", readyArgs...)

	childExited := make(chan error, 1)
	go func() { childExited <- bridge.Wait() }()

	var shutdownErr error
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received; draining")
	case err := <-childExited:
		logger.Error("child exited unexpectedly", "err", err)
		shutdownErr = fmt.Errorf("child exited before shutdown: %w", err)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), opts.ShutdownTimeout)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil && shutdownErr == nil {
		shutdownErr = fmt.Errorf("shutdown server: %w", err)
	}
	if err := bridge.Stop(opts.ShutdownTimeout); err != nil && shutdownErr == nil {
		shutdownErr = fmt.Errorf("stop child: %w", err)
	}
	return shutdownErr
}

func parseFlags(args []string, stderr io.Writer) (Options, error) {
	var opts Options
	opts.ListenPort = 8080
	opts.ShutdownTimeout = 30 * time.Second
	opts.StartupTimeout = 10 * time.Second
	opts.MaxRequestBytes = 1 << 20

	fs := flag.NewFlagSet("musterstdio", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ChildCommand, "child-command", "", "executable to spawn as the stdio MCP child")
	fs.Var(&opts.ChildArgs, "child-arg", "argument passed to the child (repeatable, in order)")
	fs.Var(&opts.ChildEnv, "child-env", "KEY=VALUE environment entry prepended to the child (repeatable)")
	fs.IntVar(&opts.ListenPort, "listen-port", opts.ListenPort, "TCP port for the main HTTP listener")
	fs.IntVar(&opts.HealthPort, "health-port", 0, "TCP port for the optional separate /healthz listener (0 = serve on listen-port)")
	fs.DurationVar(&opts.ShutdownTimeout, "shutdown-timeout", opts.ShutdownTimeout, "maximum time to wait for in-flight requests and the child to drain")
	fs.DurationVar(&opts.StartupTimeout, "startup-timeout", opts.StartupTimeout, "maximum time to wait for the child to come up")
	fs.Int64Var(&opts.MaxRequestBytes, "max-request-bytes", opts.MaxRequestBytes, "maximum POST /mcp request body size")
	fs.BoolVar(&opts.PrintVersion, "version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return Options{}, fmt.Errorf("parse flags: %w", err)
	}
	if fs.NArg() > 0 {
		return Options{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	if opts.ListenPort < 0 || opts.ListenPort > 65535 {
		return Options{}, fmt.Errorf("invalid --listen-port %d", opts.ListenPort)
	}
	if opts.HealthPort < 0 || opts.HealthPort > 65535 {
		return Options{}, fmt.Errorf("invalid --health-port %d", opts.HealthPort)
	}
	return opts, nil
}

func healthAddr(opts Options) string {
	if opts.HealthPort == 0 || opts.HealthPort == opts.ListenPort {
		return ""
	}
	return "0.0.0.0:" + strconv.Itoa(opts.HealthPort)
}

func parseEnvPairs(pairs []string) map[string]string {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		out[k] = v
	}
	return out
}

// sliceFlag implements flag.Value for repeatable string flags.
type sliceFlag []string

func (s *sliceFlag) String() string {
	if s == nil {
		return ""
	}
	return strings.Join(*s, ",")
}

func (s *sliceFlag) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := Run(ctx, os.Args[1:], os.Stderr, logger); err != nil {
		logger.Error("musterstdio exited with error", "err", err)
		os.Exit(1)
	}
}
