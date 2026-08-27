package mcpserver

import (
	"context"
	"log/slog"
)

// mcpTransportLogger returns the logger handed to mcp-go's streamable-http
// transport for one client. Without it the transport logs through the bare
// process default: its once-per-second listener retry errors then appear as
// context-free ERROR lines that cannot be attributed to a server.
//
// The returned logger tags every record with the server URL and caps the
// level at WARN: the transport's errors are retry noise from a loop that
// either recovers by itself or is torn down by muster (see authLossDetector)
// — the conditions worth alerting on are logged by muster at the state
// transition, not once per retry.
func mcpTransportLogger(serverURL string) *slog.Logger {
	return slog.New(warnCapHandler{slog.Default().Handler()}).With(
		slog.String("subsystem", "mcp-transport"),
		slog.String("server_url", serverURL),
	)
}

// warnCapHandler demotes records above WARN to WARN and delegates everything
// else unchanged.
type warnCapHandler struct {
	slog.Handler
}

func (h warnCapHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if level > slog.LevelWarn {
		level = slog.LevelWarn
	}
	return h.Handler.Enabled(ctx, level)
}

func (h warnCapHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level > slog.LevelWarn {
		capped := slog.NewRecord(record.Time, slog.LevelWarn, record.Message, record.PC)
		record.Attrs(func(attr slog.Attr) bool {
			capped.AddAttrs(attr)
			return true
		})
		record = capped
	}
	return h.Handler.Handle(ctx, record)
}

func (h warnCapHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return warnCapHandler{h.Handler.WithAttrs(attrs)}
}

func (h warnCapHandler) WithGroup(name string) slog.Handler {
	return warnCapHandler{h.Handler.WithGroup(name)}
}
