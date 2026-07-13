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
// subject token plus an audience name and receives a token issued by the
// audience's downstream OIDC/Dex token endpoint.
//
// mcp-oauth owns subject-token validation (TrustedIssuers), client
// authentication, the per-client audience allowlist
// (Config.TokenExchangeClientAudiences), and audit. BrokerExchanger owns the
// audience → target dispatch and the downstream exchange.
//
// The exchanger instance is deliberately separate from the OAuth manager's
// internal SSO exchanger: broker targets carry their own scope sets (e.g. the
// Dex cross-client scope for kube-apiserver-bound audiences), and sharing the
// cache with the internal SSO path could serve a token issued with different
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
	// http.DefaultTransport when private IPs are allowed. The timeout is
	// unconditional: downstream API calls must not block indefinitely.
	httpClient := &http.Client{Timeout: 30 * time.Second}
	if cfg.AllowPrivateIP {
		httpClient.Transport = http.DefaultTransport
	}
	return &BrokerExchanger{
		cfg: cfg,
		exchanger: NewTokenExchangerWithOptions(TokenExchangerOptions{
			AllowPrivateIP: cfg.AllowPrivateIP,
			HTTPClient:     httpClient,
		}),
	}
}

// Exchange maps the requested audience to its downstream token endpoint and
// performs the RFC 8693 exchange, forwarding the (already validated) subject
// token verbatim. Unknown audiences return errors wrapping
// server.ErrInvalidTarget so the client receives invalid_target.
//
// Cache keying: req.Subject.Subject (the impersonated identity) is used as
// TokenExchanger's UserID. Do NOT substitute the actor subject here: if two
// different impersonated subjects were keyed on the same actor, the cache
// would serve the first subject's token to the second caller. Authorization
// is enforced upstream by mcp-oauth before Exchange is called.
func (b *BrokerExchanger) Exchange(ctx context.Context, req *oauthserver.ExchangerRequest) (*oauthserver.ExchangerResult, error) {
	target, ok := b.cfg.Targets[req.Audience]
	if !ok {
		return nil, fmt.Errorf("%w: no broker target configured for audience %q", oauthserver.ErrInvalidTarget, req.Audience)
	}

	specConfig := api.TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: target.DexTokenEndpoint,
		ExpectedIssuer:   target.ExpectedIssuer,
		ConnectorID:      target.ConnectorID,
		// Scopes are operator-controlled per target: the RFC 8693 scope
		// parameter from the client is intentionally ignored. Kubernetes-bound
		// audiences require the Dex cross-client scope, which clients must not
		// be able to drop or extend.
		Scopes: target.Scopes,
	}

	var clientID, clientSecret string
	if target.ClientCredentialsSecretRef != nil {
		creds, err := b.loadCredentials(ctx, target.ClientCredentialsSecretRef)
		if err != nil {
			logging.Warn("TokenBroker", "Failed to load credentials for audience=%s: %v", req.Audience, err)
			return nil, fmt.Errorf("load broker credentials for audience %q: %w", req.Audience, err)
		}
		clientID, clientSecret = creds.ClientID, creds.ClientSecret
	}

	// Broker targets carry no required audiences, so this stamps only the
	// credentials; it cannot fail without audiences to format.
	exchangeConfig, err := specConfig.WithResolvedRuntime(clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("resolve broker exchange config for audience %q: %w", req.Audience, err)
	}

	result, err := b.exchanger.Exchange(ctx, &ExchangeRequest{
		Config:           &exchangeConfig.TokenExchangeConfig,
		SubjectToken:     req.SubjectToken,
		SubjectTokenType: req.SubjectTokenType,
		UserID:           req.Subject.Subject,
	})
	if err != nil {
		return nil, fmt.Errorf("broker exchange for audience %q: %w", req.Audience, err)
	}

	logging.Debug("TokenBroker", "Brokered exchange for audience=%s user=%s (cached=%v)",
		req.Audience, logging.TruncateIdentifier(req.Subject.Subject), result.FromCache)

	return &oauthserver.ExchangerResult{
		AccessToken:     result.AccessToken,
		IssuedTokenType: result.IssuedTokenType,
		ExpiresAt:       result.ExpiresAt,
	}, nil
}

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
