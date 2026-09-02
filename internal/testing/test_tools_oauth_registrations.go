package testing

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/internal/api"
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
