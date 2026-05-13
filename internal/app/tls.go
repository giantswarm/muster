package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"github.com/giantswarm/muster/pkg/logging"
)

// installExtraCAFile reads PEM-encoded certificates from path, appends them to
// the system trust pool, and replaces http.DefaultTransport's TLSClientConfig
// so every outbound HTTP call that uses the default client (MCP backends,
// token exchange, OAuth proxy) trusts the additional CAs.
//
// This is the single place that augments outbound trust at startup. Callers
// that construct their own *http.Transport (e.g. an OAuth client with a
// caller-provided caFile) opt out of the augmented pool by design — they have
// explicit TLS configuration of their own.
//
// A missing/unreadable file or unparseable PEM is fatal: muster's outbound
// dependencies should never silently fall back to the unaugmented pool when
// the operator asked for an internal CA.
func installExtraCAFile(path string) error {
	pem, err := os.ReadFile(path) //nolint:gosec // path comes from operator-provided flag
	if err != nil {
		return fmt.Errorf("read extra CA file %s: %w", path, err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return fmt.Errorf("no PEM certificates parsed from %s", path)
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// DefaultTransport is always *http.Transport in the stdlib; this guard
		// only triggers if something else replaced it before bootstrap, in
		// which case we cannot safely mutate it.
		return fmt.Errorf("http.DefaultTransport is %T, not *http.Transport", http.DefaultTransport)
	}
	cloned := transport.Clone()
	if cloned.TLSClientConfig == nil {
		cloned.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	cloned.TLSClientConfig.RootCAs = pool
	http.DefaultTransport = cloned

	logging.Info("Bootstrap", "Installed extra CA file %s into the system trust pool", path)
	return nil
}
