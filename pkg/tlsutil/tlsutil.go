package tlsutil

import (
	"crypto/x509"
	"fmt"
	"os"
)

// LoadCAPool reads PEM-encoded certificates from path and returns a pool
// containing the system trust roots plus those certificates. It is the single
// place that turns an operator-provided CA file into an outbound trust pool, so
// every consumer (the process-wide http.DefaultTransport install and the
// explicit CA pools handed to mcp-oauth's permissive JWKS / token-exchange
// clients) verifies against an identical pool.
//
// A missing/unreadable file or unparseable PEM is an error: muster's outbound
// dependencies should never silently fall back to the unaugmented system pool
// when the operator asked for an internal CA.
func LoadCAPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path) //nolint:gosec // path comes from operator-provided flag
	if err != nil {
		return nil, fmt.Errorf("read extra CA file %s: %w", path, err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no PEM certificates parsed from %s", path)
	}
	return pool, nil
}
