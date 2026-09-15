package testing

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// apiServerProxy is the TCP relay the harness puts between a Kubernetes-mode
// instance and the shared envtest API server. muster's kubeconfig names the
// proxy; the proxy dials the API server. Closing it is the only way to take
// the API server away from one instance while the others keep theirs -- an
// envtest control plane cannot pause -- and it is what
// test_set_apiserver_reachable and pre_configuration.apiserver.reachable_after
// operate.
//
// The relay is byte-for-byte, so TLS runs end to end between muster and the
// API server; the serving certificate covers 127.0.0.1, which is what the
// kubeconfig points at.
type apiServerProxy struct {
	listenAddr string
	targetAddr string

	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{}
	// generation counts opens; an accept loop that outlives its listener
	// (closed under it) must not touch the connections of the next open.
	generation int
	wg         sync.WaitGroup
}

func newAPIServerProxy(listenAddr, targetAddr string) *apiServerProxy {
	return &apiServerProxy{
		listenAddr: listenAddr,
		targetAddr: targetAddr,
		conns:      make(map[net.Conn]struct{}),
	}
}

// addr is the address muster is configured with.
func (p *apiServerProxy) addr() string {
	return p.listenAddr
}

// reachable reports whether the proxy accepts connections right now.
func (p *apiServerProxy) reachable() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.listener != nil
}

// open starts accepting connections and relaying them to the API server.
// Opening an open proxy is a no-op.
func (p *apiServerProxy) open() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener != nil {
		return nil
	}
	listener, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", p.listenAddr, err)
	}
	p.listener = listener
	p.generation++
	p.wg.Add(1)
	go p.accept(listener, p.generation)
	return nil
}

// close stops accepting and severs every relayed connection: to muster the
// API server is gone, its watches end and its next request is refused.
// Closing a closed proxy is a no-op.
func (p *apiServerProxy) close() {
	p.mu.Lock()
	listener := p.listener
	p.listener = nil
	conns := p.conns
	p.conns = make(map[net.Conn]struct{})
	p.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	for conn := range conns {
		_ = conn.Close()
	}
}

// shutdown closes the proxy and waits for its accept loop to return, so a
// destroyed instance leaves no goroutine behind.
func (p *apiServerProxy) shutdown() {
	p.close()
	p.wg.Wait()
}

func (p *apiServerProxy) accept(listener net.Listener, generation int) {
	defer p.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		if !p.track(conn, generation) {
			_ = conn.Close()
			return
		}
		go p.relay(conn, generation)
	}
}

// track records an accepted connection under the current generation; it
// reports false when the proxy was closed (or reopened) meanwhile.
func (p *apiServerProxy) track(conn net.Conn, generation int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener == nil || p.generation != generation {
		return false
	}
	p.conns[conn] = struct{}{}
	return true
}

func (p *apiServerProxy) untrack(conn net.Conn, generation int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.generation == generation {
		delete(p.conns, conn)
	}
}

// relay copies bytes both ways between an accepted connection and the API
// server until either side closes; a close of the proxy closes both.
func (p *apiServerProxy) relay(client net.Conn, generation int) {
	defer p.untrack(client, generation)
	defer func() { _ = client.Close() }()

	upstream, err := net.DialTimeout("tcp", p.targetAddr, 5*time.Second)
	if err != nil {
		return
	}
	if !p.track(upstream, generation) {
		_ = upstream.Close()
		return
	}
	defer p.untrack(upstream, generation)
	defer func() { _ = upstream.Close() }()

	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if tcp, ok := dst.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}
	go pipe(upstream, client)
	go pipe(client, upstream)
	<-done
	<-done
}

// openAfter opens the proxy once delay has passed, unless ctx ends first --
// the API server that is reachable only some time after muster serve started.
func (p *apiServerProxy) openAfter(ctx context.Context, delay time.Duration, onOpen func(error)) {
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		onOpen(p.open())
	}()
}
