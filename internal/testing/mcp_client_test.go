package testing

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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

// TestHarnessHTTPClientReleasesAClosedStreamAtOnce reproduces the frame that
// held a muster test call for 90s past its deadline (#1206): an SSE response
// body closed while its reader is blocked in Read, and the stream ending
// inside the transport's post-close drain. On a reused connection the reader
// and a second Close then wait until the idle connection is reaped; the
// harness's client must release them when the stream ends.
func TestHarnessHTTPClientReleasesAClosedStreamAtOnce(t *testing.T) {
	end := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		<-end
	}))
	defer srv.Close()

	// The connection reports when the reader, past the event, reads the
	// socket again: from then on it is blocked in Read.
	var armed atomic.Bool
	var once sync.Once
	reading := make(chan struct{})
	client := newHarnessHTTPClient()
	transport := client.Transport.(*http.Transport)
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return readSignalConn{Conn: conn, armed: &armed, once: &once, reading: reading}, nil
	}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// The reader is mcp-go's readSSE loop: it takes the event, then blocks
	// in Read for the next one.
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
		armed.Store(true)
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
	<-reading

	// readSSE's closer goroutine closes the body when the call's context
	// ends, and the stream ends right after, inside the transport's drain.
	_ = resp.Body.Close()
	close(end)

	select {
	case <-readerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the reader of a closed stream is still blocked 5s after the stream ended: a reused connection holds it until its idle timeout")
	}

	// mcp-go's SendRequest closes the body once more when the call returns.
	closed := make(chan struct{})
	go func() {
		_ = resp.Body.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("closing the body again blocks: the call would not return before the idle connection is reaped")
	}
}
