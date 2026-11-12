package oauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
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
	cfg                  config.TokenExchangeBrokerConfig
	defaultSubjectIssuer string
	exchanger            *TokenExchanger
}

// NewBrokerExchanger creates a BrokerExchanger for the configured targets.
// defaultSubjectIssuer is the issuer whose subject tokens a grant target
// accepts when it lists no subjectIssuers: the Dex issuer of muster's own
// sign-in.
func NewBrokerExchanger(cfg config.TokenExchangeBrokerConfig, defaultSubjectIssuer string) *BrokerExchanger {
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
		cfg:                  cfg,
		defaultSubjectIssuer: defaultSubjectIssuer,
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
	if target.IsGrantTarget() {
		return b.releaseGrant(ctx, req, target)
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

// releaseGrant answers a grant target: the person's own grant from the
// target's issuer, as the OAuth proxy holds it (refreshed when due), released
// to the authenticated broker client. mcp-oauth has already validated the
// subject token and the client's audience allowlist; this decides whether the
// token's issuer may release grants, which person, and whether a grant exists.
//
// The person is the subject an MCP request presenting the subject token is
// served as (api.SessionSubjectResolver): the subject the person's grants are
// filed under when such a session signs in, so a grant is released by the
// identity it was filed under whatever the trustedIssuers subjectClaim says.
// The refresh token stays in muster: the relying party gets the access token
// and its remaining lifetime and re-exchanges when that runs out.
func (b *BrokerExchanger) releaseGrant(ctx context.Context, req *oauthserver.ExchangerRequest, target config.BrokerTargetConfig) (*oauthserver.ExchangerResult, error) {
	issuer := strings.TrimSuffix(target.GrantIssuer, "/")

	subjectIssuer := ""
	if req.Subject != nil {
		subjectIssuer = req.Subject.Issuer
	}
	if !b.subjectIssuerAllowed(target, subjectIssuer) {
		logging.WarnWithAttrs("TokenBroker", "subject_grant_release_refused",
			slog.String("audience", req.Audience),
			slog.String("client", req.ClientID),
			slog.String("issuer", issuer),
			slog.String("subjectIssuer", subjectIssuer),
			slog.String("reason", "subject_issuer_not_allowed"))
		return nil, fmt.Errorf("%w: subject_issuer_not_allowed: subject tokens of %s do not release grants of %s", oauthserver.ErrInvalidTarget, subjectIssuer, issuer)
	}

	sub, err := grantSubject(ctx, req)
	if err != nil {
		logging.WarnWithAttrs("TokenBroker", "subject_grant_release_refused",
			slog.String("audience", req.Audience),
			slog.String("client", req.ClientID),
			slog.String("issuer", issuer),
			slog.String("reason", "no_subject"),
			slog.String("error", err.Error()))
		return nil, fmt.Errorf("%w: no_grant: %v", oauthserver.ErrInvalidTarget, err)
	}

	handler := api.GetOAuthHandler()
	releaser, ok := handler.(api.GrantReleaser)
	if handler == nil || !handler.IsEnabled() || !ok {
		return nil, fmt.Errorf("grant target %q: the OAuth proxy is disabled, no grants are held", req.Audience)
	}

	token := releaser.SubjectGrantForRelease(ctx, sub, issuer)
	if token == nil || token.AccessToken == "" {
		logging.InfoWithAttrs("TokenBroker", "subject_grant_release_refused",
			slog.String("audience", req.Audience),
			slog.String("client", req.ClientID),
			slog.String("issuer", issuer),
			slog.String("sub", logging.TruncateIdentifier(sub)),
			slog.String("reason", "no_grant"))
		return nil, fmt.Errorf("%w: no_grant: the person holds no grant for issuer %s", oauthserver.ErrInvalidTarget, issuer)
	}

	logging.InfoWithAttrs("TokenBroker", "subject_grant_released",
		slog.String("audience", req.Audience),
		slog.String("client", req.ClientID),
		slog.String("issuer", issuer),
		slog.String("sub", logging.TruncateIdentifier(sub)),
		slog.Time("expiresAt", token.ExpiresAt))

	return &oauthserver.ExchangerResult{
		AccessToken:     token.AccessToken,
		IssuedTokenType: oauthserver.SubjectTokenTypeAccessToken,
		ExpiresAt:       token.ExpiresAt,
		Scope:           token.Scope,
	}, nil
}

// subjectIssuerAllowed reports whether a subject token of the issuer may
// release the target's grant: target.SubjectIssuers, else the default subject
// issuer.
func (b *BrokerExchanger) subjectIssuerAllowed(target config.BrokerTargetConfig, subjectIssuer string) bool {
	if subjectIssuer == "" {
		return false
	}
	allowed := target.SubjectIssuers
	if len(allowed) == 0 {
		allowed = []string{b.defaultSubjectIssuer}
	}
	subjectIssuer = strings.TrimSuffix(subjectIssuer, "/")
	return slices.ContainsFunc(allowed, func(issuer string) bool {
		return issuer != "" && strings.TrimSuffix(issuer, "/") == subjectIssuer
	})
}

// grantSubject returns the subject the person's grants are filed under: the
// session subject of the request's subject token. A subject that differs from
// the token's raw sub (a trustedIssuers subjectClaim mapping) is logged, as a
// grant filed under one is never found under the other.
func grantSubject(ctx context.Context, req *oauthserver.ExchangerRequest) (string, error) {
	resolver := api.GetSessionSubjectResolver()
	if resolver == nil {
		return "", errors.New("muster runs no OAuth server to resolve the subject token's session subject")
	}
	sub, err := resolver.SessionSubject(ctx, req.SubjectToken)
	if err != nil {
		return "", fmt.Errorf("resolve the subject token's session subject: %w", err)
	}
	if raw := rawSubject(req.Subject); raw != "" && raw != sub {
		logging.WarnWithAttrs("TokenBroker", "subject_grant_subject_mapped",
			slog.String("audience", req.Audience),
			slog.String("sub", logging.TruncateIdentifier(raw)),
			slog.String("sessionSubject", logging.TruncateIdentifier(sub)))
	}
	return sub, nil
}

// rawSubject returns the raw sub of the validated subject token.
func rawSubject(identity *oauthserver.SubjectIdentity) string {
	if identity == nil {
		return ""
	}
	if identity.Claims != nil && identity.Claims.Subject != "" {
		return identity.Claims.Subject
	}
	return identity.Subject
}

func (b *BrokerExchanger) loadCredentials(ctx context.Context, ref *config.BrokerSecretRefConfig) (*api.ClientCredentials, error) {
	handler := api.GetSecretCredentialsHandler()
	if handler == nil {
		return nil, fmt.Errorf("no secret credentials handler registered (broker secret refs require Kubernetes mode)")
	}
	return handler.LoadClientCredentialsForConfig(ctx, &api.ClientCredentialsSecretRef{
		Name:            ref.Name,
		Namespace:       ref.Namespace,
		ClientIDKey:     ref.ClientIDKey,
		ClientSecretKey: ref.ClientSecretKey,
	}, b.cfg.DefaultSecretNamespace)
}
