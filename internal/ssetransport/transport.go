package ssetransport

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"sync"
	"time"
)

// closeGrace is how long a Close waits for an in-flight Read before it cancels
// the request. A server ends a finished stream right after its last event, so
// the reader usually sees EOF well within it, and the connection stays reusable.
const closeGrace = 100 * time.Millisecond

var errBodyClosed = errors.New("ssetransport: read on closed response body")

// Wrap returns next, or http.DefaultTransport when next is nil, with every SSE
// response body made safe to close while a Read is in flight.
func Wrap(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &transport{next: next}
}

// Client returns a copy of client, or of mcp-go's default client (no timeout,
// the default transport) when client is nil, whose transport is wrapped by
// Wrap. The caller's client is not modified.
func Client(client *http.Client) *http.Client {
	var out http.Client
	if client != nil {
		out = *client
	}
	out.Transport = Wrap(out.Transport)
	return &out
}

type transport struct {
	next http.RoundTripper
}

// RoundTrip sends req under a context of its own, which an SSE body's Close
// cancels to make the transport discard the connection.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	resp, err := t.next.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return resp, err
	}
	if isEventStream(resp.Header.Get("Content-Type")) {
		resp.Body = &sseBody{body: resp.Body, cancel: cancel}
	} else {
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	}
	return resp, nil
}

func isEventStream(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "text/event-stream"
}

// cancelOnClose releases the request's context when its body is closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// sseBody never closes the transport's body while a Read on it is in flight.
type sseBody struct {
	body   io.ReadCloser
	cancel context.CancelFunc

	mu      sync.Mutex
	closed  bool
	reading chan struct{} // closed when the in-flight Read returns; nil when none is
}

func (b *sseBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, errBodyClosed
	}
	reading := make(chan struct{})
	b.reading = reading
	b.mu.Unlock()

	n, err := b.body.Read(p)

	b.mu.Lock()
	b.reading = nil
	b.mu.Unlock()
	close(reading)
	return n, err
}

// Close never blocks on an in-flight Read: it hands the transport's body to a
// goroutine that closes it once the Read has returned.
func (b *sseBody) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	reading := b.reading
	b.mu.Unlock()

	if reading == nil {
		err := b.body.Close()
		b.cancel()
		return err
	}
	go func() {
		timer := time.NewTimer(closeGrace)
		defer timer.Stop()
		select {
		case <-reading:
		case <-timer.C:
			b.cancel()
			<-reading
		}
		_ = b.body.Close()
		b.cancel()
	}()
	return nil
}
