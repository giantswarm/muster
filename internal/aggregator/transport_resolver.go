package aggregator

import (
	"context"
	"fmt"
	"net/http"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// TransportResolver returns the HTTP client the broker should use for
// outbound calls (currently: the RFC 8693 token-exchange request) targeting
// a given MCPServer.
//
// Returning nil means "use the broker's default client" — the in-process
// adapter falls back to the standard HTTP transport.
//
// The resolver is wired into the in-process broker adapter at construction
// time. It is deliberately not on the [TokenBroker] port surface: HTTP
// transport is in-process-only and cannot cross a gRPC boundary.
type TransportResolver interface {
	HTTPClientFor(ctx context.Context, serverName string) (*http.Client, error)
}

// DefaultTransportResolver returns nil for every server, leaving the
// in-process adapter to use the broker's default HTTP client.
type DefaultTransportResolver struct{}

// HTTPClientFor implements [TransportResolver].
func (DefaultTransportResolver) HTTPClientFor(_ context.Context, _ string) (*http.Client, error) {
	return nil, nil
}

// teleportTransportResolver returns a Teleport-aware HTTP client for
// MCPServers whose auth config selects Teleport; otherwise nil. The
// per-server selection comes from the aggregator's registry, so the
// resolver holds a [ServerInfoLookup] to fetch the live server info.
type teleportTransportResolver struct {
	registry ServerInfoLookup
}

// ServerInfoLookup is the minimum aggregator-side surface the resolver
// needs to map a server name to its auth config. Matches the registry's
// existing signature so it satisfies the interface without an adapter.
type ServerInfoLookup interface {
	GetServerInfo(name string) (*ServerInfo, bool)
}

// NewTeleportTransportResolver constructs a resolver that returns a
// Teleport-aware HTTP client for servers configured with
// spec.auth.teleport. Servers without it fall back to the broker default.
//
// The resolver discovers the live Teleport client handler via the api
// service locator; it does not hold a reference itself so cert rotation
// (handled by the Teleport package's CertWatcher) reaches every call.
func NewTeleportTransportResolver(registry ServerInfoLookup) TransportResolver {
	return &teleportTransportResolver{registry: registry}
}

// HTTPClientFor implements [TransportResolver]. Returns nil for servers
// not configured with Teleport auth; returns a Teleport-aware
// [http.Client] when the server's auth config selects Teleport and the
// Teleport handler is registered.
func (r *teleportTransportResolver) HTTPClientFor(ctx context.Context, serverName string) (*http.Client, error) {
	info, ok := r.registry.GetServerInfo(serverName)
	if !ok {
		return nil, nil
	}
	client, configured, err := teleportHTTPClient(ctx, info)
	if err != nil {
		return nil, fmt.Errorf("server %q: %w", serverName, err)
	}
	if !configured {
		return nil, nil
	}
	logging.Debug("TransportResolver", "Resolved teleport HTTP client for server %s", serverName)
	return client, nil
}

// teleportHTTPClient resolves a Teleport-aware HTTP client for serverInfo.
// The configured return value is true iff serverInfo.AuthConfig selects
// Teleport — when false, callers should use their default transport.
// When configured is true, callers must surface any returned error rather
// than falling back to the default transport, to avoid silently bypassing
// a required Teleport identity.
func teleportHTTPClient(ctx context.Context, serverInfo *ServerInfo) (*http.Client, bool, error) {
	if serverInfo == nil || serverInfo.AuthConfig == nil || serverInfo.AuthConfig.Type != api.AuthTypeTeleport {
		return nil, false, nil
	}
	if serverInfo.AuthConfig.Teleport == nil {
		return nil, true, fmt.Errorf("teleport auth selected but teleport settings missing")
	}

	teleportHandler := api.GetTeleportClient()
	if teleportHandler == nil {
		return nil, true, fmt.Errorf("teleport client handler not registered")
	}

	teleportAuth := serverInfo.AuthConfig.Teleport
	clientConfig := api.TeleportClientConfig{
		IdentityDir:             teleportAuth.IdentityDir,
		IdentitySecretName:      teleportAuth.IdentitySecretName,
		IdentitySecretNamespace: teleportAuth.IdentitySecretNamespace,
		AppName:                 teleportAuth.AppName,
	}

	switch {
	case clientConfig.IdentityDir == "" && clientConfig.IdentitySecretName == "":
		return nil, true, fmt.Errorf("teleport auth requires either identityDir or identitySecretName")
	case clientConfig.IdentityDir != "" && clientConfig.IdentitySecretName != "":
		return nil, true, fmt.Errorf("teleport auth: identityDir and identitySecretName are mutually exclusive")
	}

	client, err := teleportHandler.GetHTTPClientForConfig(ctx, clientConfig)
	if err != nil {
		return nil, true, fmt.Errorf("teleport HTTP client: %w", err)
	}
	return client, true, nil
}
