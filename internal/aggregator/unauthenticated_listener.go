package aggregator

import (
	"log/slog"
	"net"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// warnUnauthenticatedListener warns when a muster without inbound OAuth
// listens beyond loopback: it serves one person, so every client that reaches
// it on MCP 2026-07-28 shares that person's sign-ins and tools.
func warnUnauthenticatedListener(host string) {
	if isLoopbackListener(host) {
		return
	}
	logging.WarnWithAttrs("Aggregator", "muster listens beyond loopback without inbound OAuth: it serves one person, and on MCP 2026-07-28 every client shares that person's sign-ins and tools. Enable muster.oauth.server, or listen on localhost",
		slog.String("host", host))
}

// isLoopbackListener reports whether a listen host accepts loopback
// connections only. An empty host listens on every interface.
func isLoopbackListener(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
