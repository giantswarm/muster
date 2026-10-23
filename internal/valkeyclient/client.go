package valkeyclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/config"
)

const (
	// OperationTimeout bounds every Valkey command muster sends, layered on
	// the caller's context (the shorter wins), and the dial and response wait
	// of each connection. A Valkey in the same cluster answers in about a
	// millisecond.
	OperationTimeout = 300 * time.Millisecond

	// unavailableFor is how long commands fail at once after one failed to
	// reach Valkey. It spans the few commands one request sends in a row, so
	// only the first of them waits for OperationTimeout; the next command
	// after it tries Valkey again.
	unavailableFor = 250 * time.Millisecond
)

// ErrUnavailable is the error of a command that was not sent because an
// earlier one failed to reach Valkey within the last unavailableFor.
var ErrUnavailable = errors.New("valkey unavailable: an earlier command did not reach it")

// New creates a client for cfg. The client dials on creation, so an
// unreachable Valkey is an error here. The stores never read through
// valkey-go's client-side cache (no DoCache), so the cache and the CLIENT
// TRACKING subscription it opens per connection are off.
func New(cfg config.ValkeyConfig) (valkey.Client, error) {
	opts := valkey.ClientOption{
		InitAddress:  []string{cfg.URL},
		DisableCache: true,
		Password:     cfg.Password,
		SelectDB:     cfg.DB,
		Dialer: net.Dialer{
			Timeout:   OperationTimeout,
			KeepAlive: valkey.DefaultTCPKeepAlive,
		},
		ConnWriteTimeout: OperationTimeout,
	}
	if cfg.TLSEnabled {
		opts.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: cfg.TLSServerName,
		}
	}

	client, err := valkey.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("valkey connect: %w", err)
	}
	return newBounded(client, time.Now), nil
}

// bounded runs Do and DoMulti, the commands muster's stores send, under a
// per-command deadline, and fails them at once for unavailableFor after one
// did not reach Valkey. The streaming, caching and dedicated-connection
// methods pass through unbounded: no store uses them.
type bounded struct {
	valkey.Client
	now func() time.Time
	// unavailableUntil is the Unix nanosecond time until which commands fail
	// with ErrUnavailable without being sent.
	unavailableUntil atomic.Int64
}

func newBounded(client valkey.Client, now func() time.Time) *bounded {
	return &bounded{Client: client, now: now}
}

func (b *bounded) Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
	if b.unavailable() {
		return valkey.NewErrorResult(ErrUnavailable)
	}
	opCtx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	result := b.Client.Do(opCtx, cmd)
	b.record(ctx, result.NonValkeyError())
	return result
}

func (b *bounded) DoMulti(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
	if b.unavailable() {
		results := make([]valkey.ValkeyResult, len(multi))
		for i := range results {
			results[i] = valkey.NewErrorResult(ErrUnavailable)
		}
		return results
	}
	opCtx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	results := b.Client.DoMulti(opCtx, multi...)
	for _, r := range results {
		if err := r.NonValkeyError(); err != nil {
			b.record(ctx, err)
			break
		}
	}
	return results
}

func (b *bounded) unavailable() bool {
	return b.now().UnixNano() < b.unavailableUntil.Load()
}

// record marks Valkey unavailable after a command failed to reach it. A
// caller that gave up (its own context ended) says nothing about Valkey, and
// an answer Valkey gave (a ValkeyError, nil included) is no failure.
func (b *bounded) record(ctx context.Context, err error) {
	if err == nil || ctx.Err() != nil {
		return
	}
	b.unavailableUntil.Store(b.now().Add(unavailableFor).UnixNano())
}
