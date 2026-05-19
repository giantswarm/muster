package aggregator

import "time"

// httpReadHeaderTimeout caps how long the HTTP servers wait for request
// headers. Bounds Slowloris-style request-header attacks.
const httpReadHeaderTimeout = 30 * time.Second
