package oauth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/giantswarm/mcp-oauth/providers/tokencache"
	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
)

// CredentialProvider mints credentials for one broker target.
//
// Each implementation encapsulates the downstream token-exchange protocol for a
// specific target type (e.g. OIDC/Dex exchange, GitHub App installation token).
// The broker calls Mint after mcp-oauth has already validated and authorized the
// request: the provider only needs to perform the downstream exchange.
type CredentialProvider interface {
	Mint(ctx context.Context, req MintRequest) (*MintResult, error)
}

// MintRequest carries the parameters for a downstream credential exchange.
type MintRequest struct {
	// Subject is the authz principal: req.Subject.Subject (the impersonated
	// identity). Used as the cache key by the oidc-exchange provider. Do NOT
	// replace this with the actor subject — see comment on oidcExchangeProvider.
	Subject string

	// SubjectToken is the raw RFC 8693 subject_token, forwarded verbatim to
	// the downstream token endpoint.
	SubjectToken string

	// SubjectTokenType is the RFC 8693 token-type URN of SubjectToken.
	SubjectTokenType string

	// Target is the audience / target name, used for logging and error messages.
	Target string
}

// MintResult is the result of a successful credential exchange.
type MintResult struct {
	AccessToken     string
	IssuedTokenType string
	ExpiresAt       time.Time
	// FromCache is true when the oidc-exchange provider returned a cached token.
	FromCache bool
}

// providerDeps holds the long-lived dependencies shared across provider instances
// constructed per Exchange call. The broker builds this once in NewBrokerExchanger
// and threads it through the factory so caches survive individual Mint calls.
type providerDeps struct {
	exchanger   *TokenExchanger
	githubCache *tokencache.Cache
	// httpClient is the broker's shared HTTP client; nil falls back to http.DefaultClient.
	httpClient *http.Client
	defaultNS  string
}

// providerFactory constructs a CredentialProvider for a single broker target.
type providerFactory func(target config.BrokerTargetConfig, deps providerDeps) CredentialProvider

// providerRegistry dispatches to CredentialProvider implementations by target type.
// Factories are keyed on BrokerTargetType; an empty type defaults to
// config.TargetTypeOIDCExchange.
type providerRegistry struct {
	factories map[config.BrokerTargetType]providerFactory
}

// defaultProviderRegistry returns a registry pre-loaded with the built-in providers.
func defaultProviderRegistry() *providerRegistry {
	r := &providerRegistry{factories: make(map[config.BrokerTargetType]providerFactory)}
	r.factories[config.TargetTypeOIDCExchange] = func(target config.BrokerTargetConfig, deps providerDeps) CredentialProvider {
		return &oidcExchangeProvider{target: target, exchanger: deps.exchanger, defaultNS: deps.defaultNS}
	}
	r.factories[config.TargetTypeGithubApp] = func(target config.BrokerTargetConfig, deps providerDeps) CredentialProvider {
		httpClient := deps.httpClient
		if httpClient == nil {
			httpClient = http.DefaultClient
		}
		return &githubAppProvider{target: target, cache: deps.githubCache, defaultNS: deps.defaultNS, httpClient: httpClient}
	}
	return r
}

// forTarget resolves the CredentialProvider for the given target.
// An empty target Type defaults to config.TargetTypeOIDCExchange.
// An unregistered type returns an error wrapping oauthserver.ErrInvalidTarget.
func (r *providerRegistry) forTarget(audience string, target config.BrokerTargetConfig, deps providerDeps) (CredentialProvider, error) {
	targetType := target.Type
	if targetType == "" {
		targetType = config.TargetTypeOIDCExchange
	}
	factory, ok := r.factories[targetType]
	if !ok {
		return nil, fmt.Errorf("%w: unsupported target type %q for audience %q", oauthserver.ErrInvalidTarget, targetType, audience)
	}
	return factory(target, deps), nil
}

// oidcExchangeProvider implements CredentialProvider via downstream RFC 8693
// OIDC/Dex token exchange. It wraps TokenExchanger and preserves the per-
// (endpoint, connector, user) token cache.
//
// Cache keying: MintRequest.Subject (= req.Subject.Subject, the impersonated
// identity) is used as TokenExchanger's UserID. Do NOT substitute the actor
// subject here: if two different impersonated subjects were keyed on the same
// actor, the cache would serve the first subject's token to the second caller.
// Authorization is enforced upstream by mcp-oauth before Mint is called.
type oidcExchangeProvider struct {
	target    config.BrokerTargetConfig
	exchanger *TokenExchanger
	defaultNS string
}

func (p *oidcExchangeProvider) Mint(ctx context.Context, req MintRequest) (*MintResult, error) {
	exchangeConfig := &api.TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: p.target.DexTokenEndpoint,
		ExpectedIssuer:   p.target.ExpectedIssuer,
		ConnectorID:      p.target.ConnectorID,
		// Scopes are operator-controlled per target: the RFC 8693 scope
		// parameter from the client is intentionally ignored. Kubernetes-bound
		// audiences require the Dex cross-client scope, which clients must not
		// be able to drop or extend.
		Scopes: p.target.Scopes,
	}

	if p.target.ClientCredentialsSecretRef != nil {
		creds, err := p.loadCredentials(ctx, p.target.ClientCredentialsSecretRef)
		if err != nil {
			logging.Warn("TokenBroker", "Failed to load credentials for audience=%s: %v", req.Target, err)
			return nil, fmt.Errorf("load broker credentials for audience %q: %w", req.Target, err)
		}
		exchangeConfig.ClientID = creds.ClientID
		exchangeConfig.ClientSecret = creds.ClientSecret
	}

	result, err := p.exchanger.Exchange(ctx, &ExchangeRequest{
		Config:           exchangeConfig,
		SubjectToken:     req.SubjectToken,
		SubjectTokenType: req.SubjectTokenType,
		UserID:           req.Subject,
	})
	if err != nil {
		return nil, err
	}

	return &MintResult{
		AccessToken:     result.AccessToken,
		IssuedTokenType: result.IssuedTokenType,
		ExpiresAt:       result.ExpiresAt,
		FromCache:       result.FromCache,
	}, nil
}

func (p *oidcExchangeProvider) loadCredentials(ctx context.Context, ref *config.BrokerSecretRefConfig) (*api.ClientCredentials, error) {
	handler := api.GetSecretCredentialsHandler()
	if handler == nil {
		return nil, fmt.Errorf("no secret credentials handler registered (broker secret refs require Kubernetes mode)")
	}
	return handler.LoadClientCredentials(ctx, &api.ClientCredentialsSecretRef{
		Name:            ref.Name,
		Namespace:       ref.Namespace,
		ClientIDKey:     ref.ClientIDKey,
		ClientSecretKey: ref.ClientSecretKey,
	}, p.defaultNS)
}
