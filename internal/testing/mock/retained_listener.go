package mock

import (
	"net"
	"sync/atomic"
	"time"
)

// retainedListener is the TCP socket a mock server keeps bound while the
// http.Server behind it is replaced.
//
// http.Server.Shutdown closes the listener it serves on, and a port released
// for even a moment is up for grabs: the mocks listen on ports the kernel
// assigns (":0"), the same ephemeral range it hands to every outgoing
// connection on the host, and under --parallel 50 an instance or a client
// dialling out takes the freed port before the mock listens again
// ("failed to listen again on port 45841: bind: address already in use").
// Close therefore takes the socket out of service without releasing it: the
// accept deadline is moved into the past so the serve loop's pending Accept
// returns, and Accept reports net.ErrClosed until reopen clears the deadline
// for the next server. Connections arriving in between wait in the kernel's
// accept queue, as they do for a pod replaced behind its Service. release
// closes the socket for good.
type retainedListener struct {
	*net.TCPListener
	closed atomic.Bool
}

// retainListener wraps a listener net.Listen("tcp", ...) returned.
func retainListener(l net.Listener) *retainedListener {
	return &retainedListener{TCPListener: l.(*net.TCPListener)}
}

// Accept hands out connections while the listener is in service and reports
// net.ErrClosed after Close, so http.Server.Serve returns instead of treating
// the past deadline as a temporary error to retry.
func (l *retainedListener) Accept() (net.Conn, error) {
	conn, err := l.TCPListener.Accept()
	if err != nil && l.closed.Load() {
		return nil, net.ErrClosed
	}
	return conn, err
}

// Close takes the listener out of service; the socket stays bound.
func (l *retainedListener) Close() error {
	l.closed.Store(true)
	return l.SetDeadline(time.Now())
}

// reopen puts the listener back in service for a new server.
func (l *retainedListener) reopen() error {
	if err := l.SetDeadline(time.Time{}); err != nil {
		return err
	}
	l.closed.Store(false)
	return nil
}

// release closes the socket and frees the port.
func (l *retainedListener) release() error {
	l.closed.Store(true)
	return l.TCPListener.Close()
}
