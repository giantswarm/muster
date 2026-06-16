package oauth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
)

// BrokerExchanger implements mcp-oauth's server.Exchanger on top of muster's
// TokenExchanger, turning muster into a shared RFC 8693 token broker: an
// external confidential client (e.g. a developer-portal backend) presents a
// subject token plus an audience name and receives a token minted by the
// audience's downstream credential provider.
//
// mcp-oauth owns subject-token validation (TrustedIssuers), client
// authentication, the per-client audience allowlist
// (Config.TokenExchangeClientAudiences), workload-audience authorization
// (Config.WorkloadAudiences), and audit. BrokerExchanger owns the audience →
// provider dispatch and delegates minting to the registered CredentialProvider.
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
	// registry overrides the default provider registry. Nil uses defaultProviderRegistry.
	registry *providerRegistry
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

// effectiveRegistry returns the configured registry, falling back to the default
// when none is set. Callers constructed via struct literal (e.g. in tests) that
// do not set registry receive the default oidc-exchange registry built from
// b.exchanger.
func (b *BrokerExchanger) effectiveRegistry() *providerRegistry {
	if b.registry != nil {
		return b.registry
	}
	return defaultProviderRegistry()
}

// Exchange maps the requested audience to a downstream credential provider and
// mints a token, forwarding the (already validated) subject token verbatim.
// Unknown audiences or unsupported target types return errors wrapping
// server.ErrInvalidTarget so the client receives invalid_target.
func (b *BrokerExchanger) Exchange(ctx context.Context, req *oauthserver.ExchangerRequest) (*oauthserver.ExchangerResult, error) {
	target, ok := b.cfg.Targets[req.Audience]
	if !ok {
		return nil, fmt.Errorf("%w: no broker target configured for audience %q", oauthserver.ErrInvalidTarget, req.Audience)
	}

	provider, err := b.effectiveRegistry().forTarget(req.Audience, target, b.exchanger, b.cfg.DefaultSecretNamespace)
	if err != nil {
		return nil, err
	}

	result, err := provider.Mint(ctx, MintRequest{
		Subject:          req.Subject.Subject,
		SubjectToken:     req.SubjectToken,
		SubjectTokenType: req.SubjectTokenType,
		Target:           req.Audience,
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
