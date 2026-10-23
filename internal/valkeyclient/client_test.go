package valkeyclient

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/config"
)

// requestBound is what an authenticated request may spend on Valkey: an
// `initialize` sent while Valkey restarts answers or fails within it.
const requestBound = time.Second

// testClient runs the commands the session stores send on every
// authenticated request -- a single command and a pipeline -- on a context
// with no deadline of its own, as a request context has none, and fails the
// test when one takes longer than the request bound.
type testClient struct {
	valkey.Client
}

func newClient(t *testing.T, addr string) testClient {
	t.Helper()
	client, err := New(config.ValkeyConfig{URL: addr})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return testClient{client}
}

func (c testClient) get(t *testing.T) error {
	t.Helper()
	start := time.Now()
	err := c.Do(context.Background(), c.B().Get().Key("session").Build()).NonValkeyError()
	assert.Less(t, time.Since(start), requestBound, "GET stalled past the bound")
	return err
}

func (c testClient) multi(t *testing.T) error {
	t.Helper()
	start := time.Now()
	results := c.DoMulti(context.Background(),
		c.B().Expire().Key("session").Seconds(60).Build(),
		c.B().Expire().Key("capabilities").Seconds(60).Build())
	assert.Less(t, time.Since(start), requestBound, "pipeline stalled past the bound")
	for _, r := range results {
		if err := r.NonValkeyError(); err != nil {
			return err
		}
	}
	return nil
}

// The issue's acceptance test: Valkey is recreated -- stopped and started on
// the same address, as a restarted muster-valkey pod behind its Service -- and
// commands run throughout. None stalls past the bound; before and after the
// restart they succeed.
func TestNew_CommandsStayBoundedWhileValkeyIsRecreated(t *testing.T) {
	srv := miniredis.RunT(t)
	c := newClient(t, srv.Addr())

	for round := range 3 {
		require.NoError(t, c.get(t), "round %d: Valkey up", round)

		srv.Close()
		for range 3 {
			assert.Error(t, c.get(t), "round %d: Valkey down fails instead of waiting", round)
			assert.Error(t, c.multi(t), "round %d: Valkey down fails instead of waiting", round)
		}

		require.NoError(t, srv.Restart())
		require.NoError(t, c.get(t), "round %d: Valkey back", round)
		require.NoError(t, c.multi(t), "round %d: Valkey back", round)
	}
}

// A Valkey that accepts connections but does not answer -- a pod still
// loading its data, a connection to an endpoint that is going away -- fails
// the command within the bound as well.
func TestNew_CommandsStayBoundedWhenValkeyDoesNotAnswer(t *testing.T) {
	srv := miniredis.RunT(t)
	addr := srv.Addr()
	c := newClient(t, addr)
	require.NoError(t, c.get(t))

	srv.Close()
	silent, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = silent.Close() })
	go acceptAndHold(silent)

	assert.Error(t, c.get(t))
	assert.Error(t, c.multi(t))
}

// A caller's shorter deadline still wins over the bound.
func TestNew_CallerDeadlineWins(t *testing.T) {
	srv := miniredis.RunT(t)
	c := newClient(t, srv.Addr())
	srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err := c.Do(ctx, c.B().Get().Key("k").Build()).Error()
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestNew_FailsWhenValkeyIsNotThere(t *testing.T) {
	srv := miniredis.RunT(t)
	addr := srv.Addr()
	srv.Close()

	_, err := New(config.ValkeyConfig{URL: addr})
	assert.Error(t, err)
}

// acceptAndHold accepts connections on l and never answers on them. Closing
// l closes them and ends it.
func acceptAndHold(l net.Listener) {
	var held []net.Conn
	for {
		conn, err := l.Accept()
		if err != nil {
			for _, c := range held {
				_ = c.Close()
			}
			return
		}
		held = append(held, conn)
	}
}
