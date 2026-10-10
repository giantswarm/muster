package ssetest

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
)

// holdHeader marks the response stream the server holds open.
const holdHeader = "X-Ssetest-Hold"

// NewHoldingServer starts a streamable HTTP MCP server and returns its URL.
// It answers every request at once, except that tools/call answers on an SSE
// stream it keeps open until the client closes that stream's body: the stream
// then ends inside the transport's post-close drain, while the client's
// reader is blocked in Read. One tools/call per server.
//
// It replaces http.DefaultTransport, which mcp-go's and muster's clients
// build on, for the rest of the test with a keep-alive transport that
// drives that interleaving, so a test using it must not run in parallel and
// must build its client after the call.
func NewHoldingServer(t *testing.T) string {
	t.Helper()
	end := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveMCP(w, r, end)
	}))
	t.Cleanup(srv.Close)
	// A held stream nobody closed ends before the server shuts down.
	var endOnce sync.Once
	endStream := func() { endOnce.Do(func() { close(end) }) }
	t.Cleanup(endStream)

	base := http.DefaultTransport.(*http.Transport).Clone()
	dial := base.DialContext
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &signalConn{Conn: conn, reading: make(chan struct{}), peeked: make(chan struct{})}, nil
	}
	t.Cleanup(base.CloseIdleConnections)

	orig := http.DefaultTransport
	http.DefaultTransport = &holdTransport{next: base, endStream: endStream}
	t.Cleanup(func() { http.DefaultTransport = orig })
	return srv.URL
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params struct {
		ProtocolVersion string `json:"protocolVersion"`
	} `json:"params"`
}

func serveMCP(w http.ResponseWriter, r *http.Request, end <-chan struct{}) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var msg rpcMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(msg.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var result any
	switch msg.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "ssetest")
		result = map[string]any{
			"protocolVersion": msg.Params.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "ssetest", "version": "1.0.0"},
		}
	case "tools/call":
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "answered"}}}
	default:
		result = map[string]any{}
	}
	answer, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})

	w.Header().Set("Content-Type", "text/event-stream")
	if msg.Method == "tools/call" {
		w.Header().Set(holdHeader, "1")
	}
	_, _ = io.WriteString(w, "event: message\ndata: "+string(answer)+"\n\n")
	w.(http.Flusher).Flush()
	if msg.Method == "tools/call" {
		select {
		case <-end:
		case <-r.Context().Done():
		}
	}
}

// signalConn closes reading at the first Read after arm, when the reader of
// the held stream blocks in Read, and peeked at the Read after that one
// returned: the transport's read loop, done draining the body, waits for the
// connection's next response.
type signalConn struct {
	net.Conn
	armed    atomic.Bool
	once     sync.Once
	reading  chan struct{}
	returned atomic.Bool
	peekOnce sync.Once
	peeked   chan struct{}
}

func (c *signalConn) Read(p []byte) (int, error) {
	first := false
	if c.armed.Load() {
		if c.returned.Load() {
			c.peekOnce.Do(func() { close(c.peeked) })
		}
		c.once.Do(func() {
			first = true
			close(c.reading)
		})
	}
	n, err := c.Conn.Read(p)
	if first {
		c.returned.Store(true)
	}
	return n, err
}

// holdTransport hands the held stream's body out wrapped by holdBody.
//
// mcp-go cancels a call's request right after its reader's closer goroutine
// closes the answer's body; whichever reaches the transport first decides
// whether the connection is discarded or drained. holdTransport passes a
// held request's cancellation on only once its body was closed: the order
// that holds a call.
type holdTransport struct {
	next      http.RoundTripper
	endStream func()
}

func (t *holdTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var conn *signalConn
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		conn, _ = info.Conn.(*signalConn)
	}}
	ctx, cancel := context.WithCancel(httptrace.WithClientTrace(context.WithoutCancel(req.Context()), trace))
	release := make(chan struct{})
	context.AfterFunc(req.Context(), func() {
		<-release
		cancel()
	})

	resp, err := t.next.RoundTrip(req.WithContext(ctx))
	if err != nil || resp.Header.Get(holdHeader) == "" || conn == nil {
		close(release)
		return resp, err
	}
	resp.Body = &holdBody{ReadCloser: resp.Body, conn: conn, closed: release, endStream: t.endStream, readerDone: make(chan struct{})}
	return resp, nil
}

// holdBody arms its connection once the answer was read. Its first Close,
// mcp-go's closer goroutine, waits for the reader to block in Read, closes the
// body and ends the stream: the end arrives inside the transport's post-close
// drain. A first Close with no Read in flight closes at once: no Read follows
// it. A later Close, SendRequest's, comes once the drain is
// done or the reader returned.
type holdBody struct {
	io.ReadCloser
	conn       *signalConn
	closed     chan struct{}
	endStream  func()
	first      atomic.Bool
	inRead     atomic.Int32
	readerOnce sync.Once
	readerDone chan struct{}
}

func (b *holdBody) Read(p []byte) (int, error) {
	b.inRead.Add(1)
	defer b.inRead.Add(-1)
	armed := b.conn.armed.Load()
	n, err := b.ReadCloser.Read(p)
	if armed {
		b.readerOnce.Do(func() { close(b.readerDone) })
	}
	if n > 0 {
		b.conn.armed.Store(true)
	}
	return n, err
}

func (b *holdBody) Close() error {
	if !b.first.CompareAndSwap(false, true) {
		select {
		case <-b.conn.peeked:
		case <-b.readerDone:
		}
		return b.ReadCloser.Close()
	}
	if b.inRead.Load() > 0 {
		<-b.conn.reading
	}
	err := b.ReadCloser.Close()
	close(b.closed)
	b.endStream()
	return err
}
