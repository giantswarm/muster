package oauth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
)

// BrokerExchanger implements mcp-oauth's server.Exchanger on top of muster's
// TokenExchanger, turning muster into a shared RFC 8693 token broker: an
// external confidential client (e.g. a developer-portal backend) presents a
// subject token plus an audience name and receives a token minted by the
// audience's downstream Dex.
//
// mcp-oauth owns subject-token validation (TrustedIssuers), client
// authentication, the per-client audience allowlist
// (Config.TokenExchangeClientAudiences), and audit. BrokerExchanger owns the
// audience -> downstream-Dex mapping and the actual downstream exchange,
// reusing the per-(endpoint, connector, user) token cache of TokenExchanger.
//
// The exchanger instance is deliberately separate from the OAuth manager's
// internal SSO exchanger: broker targets carry their own scope sets (e.g. the
// Dex cross-client scope for kube-apiserver-bound audiences), and sharing the
// cache with the internal SSO path could serve a token minted with different
// scopes for the same (endpoint, connector, user) key.
//
// Thread-safe: Yes.
type BrokerExchanger struct {
	cfg       config.TokenExchangeBrokerConfig
	exchanger *TokenExchanger
}

// NewBrokerExchanger creates a BrokerExchanger for the configured targets.
func NewBrokerExchanger(cfg config.TokenExchangeBrokerConfig) *BrokerExchanger {
	// Mirror the OAuth manager's internal-deployment handling: mcp-oauth's
	// private-IP-allowed client bypasses the process-wide augmented transport
	// (--extra-ca-file), so hand the exchanger an explicit client backed by
	// http.DefaultTransport when private IPs are allowed.
	var httpClient *http.Client
	if cfg.AllowPrivateIP {
		httpClient = &http.Client{
			Transport: http.DefaultTransport,
			Timeout:   30 * time.Second,
		}
	}
	return &BrokerExchanger{
		cfg: cfg,
		exchanger: NewTokenExchangerWithOptions(TokenExchangerOptions{
			AllowPrivateIP: cfg.AllowPrivateIP,
			HTTPClient:     httpClient,
		}),
	}
}

// Exchange maps the requested audience to a downstream Dex target and performs
// the downstream RFC 8693 exchange, forwarding the (already validated) subject
// token verbatim. Unknown audiences return an error wrapping
// server.ErrInvalidTarget so the client receives invalid_target.
func (b *BrokerExchanger) Exchange(ctx context.Context, req *oauthserver.ExchangerRequest) (*oauthserver.ExchangerResult, error) {
	target, ok := b.cfg.Targets[req.Audience]
	if !ok {
		return nil, fmt.Errorf("%w: no broker target configured for audience %q", oauthserver.ErrInvalidTarget, req.Audience)
	}

	exchangeConfig := &api.TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: target.DexTokenEndpoint,
		ExpectedIssuer:   target.ExpectedIssuer,
		ConnectorID:      target.ConnectorID,
		// Scopes are operator-controlled per target (the RFC 8693 scope
		// parameter from the client is intentionally ignored): k8s-bound
		// audiences need the Dex cross-client scope and clients must not be
		// able to drop or extend it.
		Scopes: target.Scopes,
	}

	if target.ClientCredentialsSecretRef != nil {
		creds, err := b.loadCredentials(ctx, target.ClientCredentialsSecretRef)
		if err != nil {
			logging.Warn("TokenBroker", "Failed to load credentials for audience=%s: %v", req.Audience, err)
			return nil, fmt.Errorf("load broker credentials for audience %q: %w", req.Audience, err)
		}
		exchangeConfig.ClientID = creds.ClientID
		exchangeConfig.ClientSecret = creds.ClientSecret
	}

	result, err := b.exchanger.Exchange(ctx, &ExchangeRequest{
		Config:           exchangeConfig,
		SubjectToken:     req.SubjectToken,
		SubjectTokenType: req.SubjectTokenType,
		UserID:           req.Subject.Subject,
	})
	if err != nil {
		return nil, err
	}

	logging.Debug("TokenBroker", "Brokered exchange for audience=%s user=%s (cached=%v)",
		req.Audience, logging.TruncateIdentifier(req.Subject.Subject), result.FromCache)

	return &oauthserver.ExchangerResult{
		AccessToken:     result.AccessToken,
		IssuedTokenType: result.IssuedTokenType,
		ExpiresAt:       result.ExpiresAt,
	}, nil
}

// loadCredentials resolves the downstream exchange client credentials from the
// referenced Kubernetes Secret via the registered SecretCredentialsHandler
// (the same mechanism the per-MCPServer tokenExchange config uses).
func (b *BrokerExchanger) loadCredentials(ctx context.Context, ref *config.BrokerSecretRefConfig) (*api.ClientCredentials, error) {
	handler := api.GetSecretCredentialsHandler()
	if handler == nil {
		return nil, fmt.Errorf("no secret credentials handler registered (broker secret refs require Kubernetes mode)")
	}
	return handler.LoadClientCredentials(ctx, &api.ClientCredentialsSecretRef{
		Name:            ref.Name,
		Namespace:       ref.Namespace,
		ClientIDKey:     ref.ClientIDKey,
		ClientSecretKey: ref.ClientSecretKey,
	}, b.cfg.DefaultSecretNamespace)
}
