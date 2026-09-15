package testing

import (
	"context"
	"fmt"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
)

// handleForgetOAuthRegistrations makes a mock OAuth server forget every RFC
// 7591 client registration it holds. Muster's stored credentials for that
// issuer are untouched, which is exactly the situation after an authorization
// server with an in-memory client store restarts: muster still presents a
// client_id the server no longer knows.
func (h *TestToolsHandler) handleForgetOAuthRegistrations(_ context.Context, args map[string]interface{}) (interface{}, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return nil, fmt.Errorf("server argument is required")
	}
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}

	oauthServer := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, serverName)
	if oauthServer == nil {
		return nil, fmt.Errorf("OAuth server %s not found", serverName)
	}

	forgotten := oauthServer.ForgetRegisteredClients()
	if h.debug {
		h.logger.Debug("🔐 OAuth server %s forgot %d DCR registration(s)\n", serverName, forgotten)
	}

	return map[string]interface{}{
		api.FieldSuccess:         true,
		api.FieldServer:          serverName,
		"forgotten":              forgotten,
		"dcr_registered_clients": oauthServer.RegisteredClientCount(),
		api.FieldMessage:         fmt.Sprintf("OAuth server %s forgot %d DCR registration(s)", serverName, forgotten),
	}, nil
}

// handleRestartMockOAuthServer replaces a mock OAuth server's process behind
// its port and issuer -- the authorization server's pod replaced. muster's
// stored tokens and client credentials for the issuer are untouched. Under
// profile pro (or forget_registrations_on_restart) the server's RFC 7591
// registrations are gone with the old process, so muster presents a
// client_id the server no longer knows; every other configuration keeps
// them, the way a server with a persistent client store does.
//
// Args:
//   - server: Required. Name of the mock OAuth server.
func (h *TestToolsHandler) handleRestartMockOAuthServer(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return nil, fmt.Errorf("server argument is required")
	}
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}

	oauthServer := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, serverName)
	if oauthServer == nil {
		return nil, fmt.Errorf("OAuth server %s not found", serverName)
	}

	forgotten, err := oauthServer.Restart(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to restart mock OAuth server %s: %w", serverName, err)
	}
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := oauthServer.WaitForReady(readyCtx); err != nil {
		return nil, fmt.Errorf("mock OAuth server %s not serving after its restart: %w", serverName, err)
	}
	if h.debug {
		h.logger.Debug("🔐 OAuth server %s restarted on port %d, %d DCR registration(s) forgotten\n", serverName, oauthServer.Port(), forgotten)
	}

	return map[string]interface{}{
		api.FieldSuccess:         true,
		api.FieldServer:          serverName,
		"port":                   oauthServer.Port(),
		"forgotten":              forgotten,
		"dcr_registered_clients": oauthServer.RegisteredClientCount(),
		api.FieldMessage:         fmt.Sprintf("OAuth server %s restarted on port %d: %d DCR registration(s) forgotten", serverName, oauthServer.Port(), forgotten),
	}, nil
}
