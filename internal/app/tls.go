package app

import (
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/giantswarm/muster/pkg/logging"
	"github.com/giantswarm/muster/pkg/tlsutil"
)

// installExtraCAFile reads PEM-encoded certificates from path, appends them to
// the system trust pool, and replaces http.DefaultTransport's TLSClientConfig
// so every outbound HTTP call that uses the default client (MCP backends,
// token exchange) trusts the additional CAs.
//
// This augments outbound trust for callers that ride http.DefaultTransport. It
// is NOT sufficient for the mcp-oauth OAuth server: its permissive JWKS /
// token-exchange clients no longer read http.DefaultTransport's RootCAs (v1+),
// so that CA trust is threaded in explicitly via config.OAuthServerConfig's
// ExtraCAFile — both paths build the pool from tlsutil.LoadCAPool so they
// verify against an identical pool. Any caller that constructs its own
// *http.Transport opts out of the augmented pool by design.
//
// A missing/unreadable file or unparseable PEM is fatal: muster's outbound
// dependencies should never silently fall back to the unaugmented pool when
// the operator asked for an internal CA.
func installExtraCAFile(path string) error {
	pool, err := tlsutil.LoadCAPool(path)
	if err != nil {
		return err
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
