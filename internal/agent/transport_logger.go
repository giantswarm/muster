package agent

import (
	"context"
	"log/slog"
)

// sessionCloseMessages are the records mcp-go's streamable-http transport
// writes when its session-close DELETE fails. The close is best-effort
// teardown after the command's work is done: a close the server or a gateway
// on the way never answers runs into the transport's deadline, and an ERROR
// line for it would follow every successful command it happens to.
var sessionCloseMessages = map[string]bool{
	"failed to create close request": true,
	"failed to send close request":   true,
}

// transportLogger returns the logger handed to mcp-go's streamable-http
// transport: the process default, with the session-close failures demoted to
// DEBUG.
func transportLogger() *slog.Logger {
	return slog.New(closeDebugHandler{slog.Default().Handler()})
}

// closeDebugHandler demotes the session-close records to DEBUG and delegates
// everything else unchanged.
type closeDebugHandler struct {
	slog.Handler
}

func (h closeDebugHandler) Handle(ctx context.Context, record slog.Record) error {
	if !sessionCloseMessages[record.Message] {
		return h.Handler.Handle(ctx, record)
	}
	if !h.Enabled(ctx, slog.LevelDebug) {
		return nil
	}
	demoted := slog.NewRecord(record.Time, slog.LevelDebug, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		demoted.AddAttrs(attr)
		return true
	})
	return h.Handler.Handle(ctx, demoted)
}

func (h closeDebugHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return closeDebugHandler{h.Handler.WithAttrs(attrs)}
}

func (h closeDebugHandler) WithGroup(name string) slog.Handler {
	return closeDebugHandler{h.Handler.WithGroup(name)}
}
