package agent

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// recordingHandler keeps every slog record it is handed.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) levelOf(msg string) (slog.Level, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message == msg {
			return r.Level, true
		}
	}
	return 0, false
}

// unansweredCloseServer is an aggregator whose session DELETE never gets an
// answer: the connection is dropped, as a close that runs into the
// transport's deadline ends without one.
func unansweredCloseServer(t *testing.T) string {
	t.Helper()
	aggregator := server.NewStreamableHTTPServer(server.NewMCPServer("aggregator", "1.0.0"), server.WithStateful(true))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		aggregator.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp"
}

func TestCloseLogsUnansweredSessionCloseAtDebug(t *testing.T) {
	records := &recordingHandler{}
	saved := slog.Default()
	slog.SetDefault(slog.New(records))
	t.Cleanup(func() { slog.SetDefault(saved) })

	c := NewClient(unansweredCloseServer(t), nil, TransportStreamableHTTP)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	level, ok := records.levelOf("failed to send close request")
	if !ok {
		t.Fatal("the unanswered session close was not logged at all")
	}
	if level != slog.LevelDebug {
		t.Errorf("unanswered session close logged at %s, want DEBUG", level)
	}
}
