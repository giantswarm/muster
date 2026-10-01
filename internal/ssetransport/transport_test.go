package ssetransport

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// wait bounds every blocking step of these tests: far above the grace period
// and the transport's 50ms drain, far below the 90s idle-connection timeout.
const wait = 5 * time.Second

// readSignalConn closes reading once a Read starts after armed is set.
type readSignalConn struct {
	net.Conn
	armed   *atomic.Bool
	once    *sync.Once
	reading chan struct{}
}

func (c readSignalConn) Read(p []byte) (int, error) {
	if c.armed.Load() {
		c.once.Do(func() { close(c.reading) })
	}
	return c.Conn.Read(p)
}

// streamFixture is an SSE server that sends one event, then holds the stream
// open until end is closed, and a keep-alive client to it whose connections
// report when a reader blocks in Read after armed is set.
type streamFixture struct {
	url     string
	end     chan struct{}
	client  *http.Client
	armed   atomic.Bool
	reading chan struct{}
	dials   atomic.Int32
}

func newStreamFixture(t *testing.T, wrap func(http.RoundTripper) http.RoundTripper) *streamFixture {
	t.Helper()
	f := &streamFixture{end: make(chan struct{}), reading: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "{}")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-f.end:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	f.url = srv.URL

	var once sync.Once
	base := http.DefaultTransport.(*http.Transport).Clone()
	dial := base.DialContext
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		f.dials.Add(1)
		return readSignalConn{Conn: conn, armed: &f.armed, once: &once, reading: f.reading}, nil
	}
	t.Cleanup(base.CloseIdleConnections)
	f.client = &http.Client{Transport: wrap(base)}
	return f
}

// openStream starts the stream and mcp-go's readSSE loop on it: the reader
// takes the event, then blocks in Read for the next one. It returns the body
// and a channel closed when the reader returns.
func (f *streamFixture) openStream(t *testing.T) (io.ReadCloser, chan struct{}) {
	t.Helper()
	resp, err := f.client.Get(f.url)
	if err != nil {
		t.Fatal(err)
	}
	gotEvent := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		br := bufio.NewReader(resp.Body)
		for line := ""; line != "\n"; {
			var err error
			if line, err = br.ReadString('\n'); err != nil {
				return
			}
		}
		f.armed.Store(true)
		close(gotEvent)
		for {
			if _, err := br.ReadString('\n'); err != nil {
				return
			}
		}
	}()
	select {
	case <-gotEvent:
	case <-readerDone:
		t.Fatal("the stream ended before its event")
	}
	<-f.reading
	return resp.Body, readerDone
}

func waitFor(t *testing.T, done <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(wait):
		t.Fatal(failure)
	}
}

// closeAgain runs mcp-go's second Close, from SendRequest's defer, and
// reports when it returns.
func closeAgain(body io.Closer) chan struct{} {
	closed := make(chan struct{})
	go func() {
		_ = body.Close()
		close(closed)
	}()
	return closed
}

// TestStreamEndingInTheDrainReleasesTheCall reproduces the frame that holds
// an MCP call for the idle-connection timeout: mcp-go's readSSE closer closes
// the body while its reader is blocked in Read, and the stream ends right
// after, inside the transport's post-close drain. The reader, the second Close
// and the next request on the client must all return at once.
func TestStreamEndingInTheDrainReleasesTheCall(t *testing.T) {
	f := newStreamFixture(t, Wrap)
	body, readerDone := f.openStream(t)

	_ = body.Close()
	close(f.end)

	waitFor(t, readerDone, "the reader of a closed stream is still blocked after the stream ended")
	waitFor(t, closeAgain(body), "closing the body again blocks: the call would not return before the idle connection is reaped")

	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url+"/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("the next request on the client is held: %v", err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("the next response on the client is held: %v", err)
	}
	_ = resp.Body.Close()
}

// TestOpenStreamIsCancelledAfterTheGrace covers a closed stream the server
// keeps open: Close returns at once, and the request is cancelled, which
// releases the reader.
func TestOpenStreamIsCancelledAfterTheGrace(t *testing.T) {
	f := newStreamFixture(t, Wrap)
	defer close(f.end)
	body, readerDone := f.openStream(t)

	closed := closeAgain(body)
	select {
	case <-closed:
	case <-readerDone:
		t.Fatal("the reader returned before Close did")
	case <-time.After(wait):
		t.Fatal("Close blocks on the in-flight Read")
	}
	waitFor(t, readerDone, "the reader of a closed open stream is never released")
}

// TestClosedStreamWithoutAReaderClosesAtOnce covers the plain Close of a body
// nobody reads: it closes the transport's body before returning, and a later
// Read fails.
func TestClosedStreamWithoutAReaderClosesAtOnce(t *testing.T) {
	f := newStreamFixture(t, Wrap)
	defer close(f.end)
	resp, err := f.client.Get(f.url)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := resp.Body.Read(make([]byte, 1)); !errors.Is(err, errBodyClosed) {
		t.Fatalf("Read after Close = %v, want %v", err, errBodyClosed)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
}

// TestOtherResponsesKeepTheirConnection checks that a response read to its
// end leaves its connection to the next request.
func TestOtherResponsesKeepTheirConnection(t *testing.T) {
	f := newStreamFixture(t, Wrap)
	for range 2 {
		resp, err := f.client.Get(f.url + "/json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if got := f.dials.Load(); got != 1 {
		t.Fatalf("dialled %d connections for two requests, want 1", got)
	}
}

func TestClientWrapsACopy(t *testing.T) {
	orig := &http.Client{Timeout: time.Minute}
	got := Client(orig)
	if orig.Transport != nil {
		t.Fatal("Client modified the caller's client")
	}
	if got.Timeout != time.Minute {
		t.Fatalf("Timeout = %v, want the caller's", got.Timeout)
	}
	if _, ok := got.Transport.(*transport); !ok {
		t.Fatalf("Transport = %T, want the wrapper", got.Transport)
	}
	if _, ok := Client(nil).Transport.(*transport); !ok {
		t.Fatal("Client(nil) is not wrapped")
	}
}
