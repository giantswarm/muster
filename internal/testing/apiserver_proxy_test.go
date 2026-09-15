package testing

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startEchoServer is the proxy's upstream in these tests: it answers every
// line it reads with the same line.
func startEchoServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				scanner := bufio.NewScanner(c)
				for scanner.Scan() {
					if _, err := fmt.Fprintln(c, scanner.Text()); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return listener.Addr().String()
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}

// roundTrip sends one line through conn and returns the answer.
func roundTrip(conn net.Conn, line string) (string, error) {
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintln(conn, line); err != nil {
		return "", err
	}
	answer, err := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimSpace(answer), err
}

func TestAPIServerProxyRelaysWhileOpen(t *testing.T) {
	upstream := startEchoServer(t)
	proxy := newAPIServerProxy(freeLoopbackAddr(t), upstream)
	t.Cleanup(proxy.shutdown)

	require.False(t, proxy.reachable(), "a new proxy is closed until opened")
	_, err := net.DialTimeout("tcp", proxy.addr(), time.Second)
	require.Error(t, err, "a closed proxy refuses connections")

	require.NoError(t, proxy.open())
	require.NoError(t, proxy.open(), "opening an open proxy is a no-op")
	require.True(t, proxy.reachable())

	conn, err := net.DialTimeout("tcp", proxy.addr(), time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	answer, err := roundTrip(conn, "hello api server")
	require.NoError(t, err)
	require.Equal(t, "hello api server", answer)
}

func TestAPIServerProxyCloseSeversConnectionsAndReopenRestores(t *testing.T) {
	upstream := startEchoServer(t)
	proxy := newAPIServerProxy(freeLoopbackAddr(t), upstream)
	t.Cleanup(proxy.shutdown)
	require.NoError(t, proxy.open())

	conn, err := net.DialTimeout("tcp", proxy.addr(), time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	answer, err := roundTrip(conn, "before")
	require.NoError(t, err)
	require.Equal(t, "before", answer)

	// The API server goes away: the established connection ends and new
	// ones are refused, the way a watch and its reconnect see an outage.
	proxy.close()
	proxy.close() // idempotent
	require.False(t, proxy.reachable())
	_, err = roundTrip(conn, "during")
	require.Error(t, err, "an established connection is severed by close")
	_, err = net.DialTimeout("tcp", proxy.addr(), time.Second)
	require.Error(t, err, "a closed proxy refuses new connections")

	// Back on the same address, as a kube-apiserver comes back on its own.
	require.NoError(t, proxy.open())
	require.True(t, proxy.reachable())
	again, err := net.DialTimeout("tcp", proxy.addr(), time.Second)
	require.NoError(t, err)
	defer func() { _ = again.Close() }()
	answer, err = roundTrip(again, "after")
	require.NoError(t, err)
	require.Equal(t, "after", answer)
}

func TestAPIServerProxyUnreachableUpstreamClosesTheConnection(t *testing.T) {
	// Nothing listens upstream: the proxy accepts and then ends the connection
	// -- muster sees a reset, not a hang.
	proxy := newAPIServerProxy(freeLoopbackAddr(t), freeLoopbackAddr(t))
	t.Cleanup(proxy.shutdown)
	require.NoError(t, proxy.open())

	conn, err := net.DialTimeout("tcp", proxy.addr(), time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = roundTrip(conn, "anyone there")
	require.Error(t, err)
}

func TestAPIServerProxyOpenAfterDelay(t *testing.T) {
	upstream := startEchoServer(t)
	proxy := newAPIServerProxy(freeLoopbackAddr(t), upstream)
	t.Cleanup(proxy.shutdown)

	opened := make(chan error, 1)
	proxy.openAfter(t.Context(), 50*time.Millisecond, func(err error) { opened <- err })
	require.False(t, proxy.reachable(), "the proxy stays closed until the delay passes")

	select {
	case err := <-opened:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the delayed open never ran")
	}
	require.True(t, proxy.reachable())
	conn, err := net.DialTimeout("tcp", proxy.addr(), time.Second)
	require.NoError(t, err)
	_ = conn.Close()
}

func TestAPIServerProxyOpenFailsOnTakenPort(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	proxy := newAPIServerProxy(taken.Addr().String(), startEchoServer(t))
	err = proxy.open()
	require.Error(t, err)
	require.False(t, proxy.reachable())
	var opErr *net.OpError
	require.True(t, errors.As(err, &opErr), "the listen error is wrapped, got %v", err)
}
