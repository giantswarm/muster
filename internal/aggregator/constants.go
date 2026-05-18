package aggregator

import "time"

const (
	// defaultValkeyKeyPrefix is the key prefix used by all Valkey-backed
	// aggregator stores when the caller does not specify one.
	defaultValkeyKeyPrefix = "muster:"

	// httpReadHeaderTimeout caps how long the HTTP servers wait for request
	// headers. Bounds Slowloris-style request-header attacks.
	httpReadHeaderTimeout = 30 * time.Second
)
