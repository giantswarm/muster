package valkeyclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/config"
)

// OperationTimeout bounds every Valkey command muster sends, layered on the
// caller's context (the shorter wins), and the dial and response wait of each
// connection. A Valkey in the same cluster answers in milliseconds; two
// sequential commands that time out still leave an authenticated request
// within a second.
const OperationTimeout = 500 * time.Millisecond

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
	return bounded{Client: client, timeout: OperationTimeout}, nil
}

// bounded runs Do and DoMulti, the commands muster's stores send, under a
// per-command deadline. The streaming, caching and dedicated-connection
// methods pass through unbounded: no store uses them.
type bounded struct {
	valkey.Client
	timeout time.Duration
}

func (b bounded) Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.Client.Do(ctx, cmd)
}

func (b bounded) DoMulti(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.Client.DoMulti(ctx, multi...)
}
