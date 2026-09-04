package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// pinAuthorizationServer hands the operator's description of an MCPServer's
// authorization server (spec.auth.authorizationServer) to the OAuth handler:
// the client registered with it out of band, read from its Secret; the
// endpoints of an AS that publishes no discovery document; and whether its
// grants belong to the person. A pin with none of those is a plain issuer
// override and needs no registration.
//
// Idempotent. It runs when the server registers and again before every login,
// so a rotated Secret is picked up without a restart and a server that
// registered before OAuth was ready still gets its pin.
func pinAuthorizationServer(ctx context.Context, serverInfo *ServerInfo) error {
	if serverInfo == nil || serverInfo.AuthConfig == nil {
		return nil
	}
	as := serverInfo.AuthConfig.AuthorizationServer
	if as == nil || (as.ClientCredentialsSecretRef == nil && !as.HasPinnedEndpoints() && !as.SubjectScoped()) {
		return nil
	}

	handler := api.GetOAuthHandler()
	if handler == nil || !handler.IsEnabled() {
		return fmt.Errorf("server %s pins its authorization server but the OAuth proxy is disabled", serverInfo.Name)
	}
	pinner, ok := handler.(api.IssuerPinner)
	if !ok {
		return fmt.Errorf("server %s pins its authorization server but the OAuth handler cannot record pins", serverInfo.Name)
	}

	pin := api.IssuerPin{
		AuthorizationEndpoint: as.AuthorizationEndpoint,
		TokenEndpoint:         as.TokenEndpoint,
		SubjectScoped:         as.SubjectScoped(),
	}
	if as.ClientCredentialsSecretRef != nil {
		secrets := api.GetSecretCredentialsHandler()
		if secrets == nil {
			return fmt.Errorf("server %s references client credentials but no secret handler is registered", serverInfo.Name)
		}
		creds, err := secrets.LoadClientCredentials(ctx, as.ClientCredentialsSecretRef, serverInfo.GetNamespace())
		if err != nil {
			return fmt.Errorf("load client credentials for server %s: %w", serverInfo.Name, err)
		}
		pin.ClientID, pin.ClientSecret = creds.ClientID, creds.ClientSecret
	}

	issuer := strings.TrimSuffix(as.Issuer, "/")
	pinner.PinIssuer(issuer, pin)
	logging.InfoWithAttrs("Aggregator", "oauth_authorization_server_pinned",
		slog.String("server", serverInfo.Name),
		slog.String("issuer", issuer),
		slog.Bool("preregisteredClient", pin.ClientID != ""),
		slog.Bool("pinnedEndpoints", as.HasPinnedEndpoints()),
		slog.Bool("subjectScoped", pin.SubjectScoped))
	return nil
}
