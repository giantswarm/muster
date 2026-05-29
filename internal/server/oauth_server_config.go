package server

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	oauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/instrumentation"
	"github.com/giantswarm/mcp-oauth/security"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/giantswarm/mcp-oauth/storage"
	storagevalkey "github.com/giantswarm/mcp-oauth/storage/valkey"
	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/internal/config"
)

// newOAuthServerConfig maps the muster OAuth config onto the mcp-oauth Config.
// Pure mapper: no I/O, no goroutines.
func newOAuthServerConfig(cfg config.OAuthServerConfig, refreshTokenTTL time.Duration) *oauthserver.Config {
	result := &oauthserver.Config{
		Issuer:                                cfg.BaseURL,
		ResourceIdentifier:                    cfg.ResourceIdentifier,
		AccessTokenTTL:                        int64(DefaultAccessTokenTTL / time.Second),
		RefreshTokenTTL:                       int64(refreshTokenTTL / time.Second),
		AllowRefreshTokenRotation:             true,
		RequirePKCE:                           true,
		AllowPKCEPlain:                        false,
		AllowPublicClientRegistration:         cfg.AllowPublicClientRegistration,
		RegistrationAccessToken:               cfg.RegistrationToken,
		MaxClientsPerIP:                       DefaultMaxClientsPerIP,
		EnableClientIDMetadataDocuments:       cfg.EnableCIMD,
		EnableIntrospectionEndpoint:           true,
		EnableUserInfoEndpoint:                true,
		TrustedPublicRegistrationSchemes:      cfg.TrustedPublicRegistrationSchemes,
		TrustedPublicRegistrationRedirectURIs: cfg.TrustedPublicRegistrationRedirectURIs,
		AllowLocalhostRedirectURIs:            cfg.AllowLocalhostRedirectURIs,
		TrustedAudiences:                      cfg.TrustedAudiences,
	}
	if cfg.EnableJWTMode {
		result.AccessTokenFormat = oauthserver.AccessTokenFormatJWT
	}
	return result
}

// buildOAuthServerOptions assembles the functional options for the mcp-oauth server.
// instrumentation.New registers a Prometheus collector on the OTel global
// provider, so a second call in the same process will race or duplicate-register.
func buildOAuthServerOptions(cfg config.OAuthServerConfig, logger *slog.Logger) ([]oauth.ServerOption, error) {
	inst, err := instrumentation.New(instrumentation.Config{
		Enabled:         true,
		ServiceName:     "muster",
		ServiceVersion:  "1.0.0",
		MetricsExporter: "prometheus",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create instrumentation: %w", err)
	}

	opts := []oauth.ServerOption{
		oauth.WithInstrumentation(inst),
		oauth.WithAuditor(security.NewAuditor(logger, true, security.WithPIIRedaction(true))),
		oauth.WithRateLimiter(security.NewRateLimiter(DefaultIPRateLimit, DefaultIPBurst, logger)),
		oauth.WithUserRateLimiter(security.NewRateLimiter(DefaultUserRateLimit, DefaultUserBurst, logger)),
		oauth.WithSecurityEventRateLimiter(security.NewRateLimiter(DefaultSecurityEventRate, DefaultSecurityEventBurst, logger)),
		oauth.WithClientRegistrationRateLimiter(security.NewClientRegistrationRateLimiterWithConfig(
			DefaultMaxClientsPerIP,
			security.DefaultRegistrationWindow,
			security.DefaultMaxRegistrationEntries,
			logger,
		)),
		oauth.WithMetadataFetchRateLimiter(security.NewRateLimiter(DefaultMetadataFetchRate, DefaultMetadataFetchBurst, logger)),
	}

	issuers := make([]oauthserver.TrustedIssuer, 0, len(cfg.KubernetesSATrusts)+len(cfg.TrustedIssuers))
	for _, t := range cfg.KubernetesSATrusts {
		subPattern, err := k8sSASubPattern(t.AllowedNamespaces, t.AllowedServiceAccounts)
		if err != nil {
			return nil, fmt.Errorf("kubernetesSATrust %q: %w", t.Issuer, err)
		}
		ti := oauthserver.TrustedIssuer{
			Issuer:             t.Issuer,
			JwksURL:            t.JwksURL,
			AllowedAudiences:   t.AllowedAudiences,
			AllowedScopes:      t.AllowedScopes,
			AllowPrivateIPJWKS: true,
		}
		if subPattern != "" {
			ti.AllowedClaims = map[string]string{"sub": subPattern}
		}
		issuers = append(issuers, ti)
	}
	for _, iss := range cfg.TrustedIssuers {
		issuers = append(issuers, oauthserver.TrustedIssuer{
			Issuer:           iss.Issuer,
			JwksURL:          iss.JwksURL,
			AllowedAudiences: iss.AllowedAudiences,
			AllowedScopes:    iss.AllowedScopes,
		})
	}
	if len(issuers) > 0 {
		opts = append(opts, oauthserver.WithTrustedIssuers(issuers))
	}

	if len(cfg.TrustedProxyCIDRs) > 0 {
		cidrs, err := parseCIDRs(cfg.TrustedProxyCIDRs)
		if err != nil {
			return nil, err
		}
		opts = append(opts, oauthserver.WithTrustedProxyCIDRs(cidrs))
	}

	return opts, nil
}

// k8sSASubPattern encodes namespace and service-account allow-lists as a glob
// pattern on the K8s SA token "sub" claim
// (system:serviceaccount:<namespace>:<name>). Returns "" when no restrictions
// apply. Returns an error for multi-entry allow-lists because TrustedIssuer
// AllowedClaims accepts only a single pattern per claim.
func k8sSASubPattern(allowedNamespaces, allowedServiceAccounts []string) (string, error) {
	if len(allowedServiceAccounts) > 1 {
		return "", fmt.Errorf("allowedServiceAccounts supports at most one entry (got %d)", len(allowedServiceAccounts))
	}
	if len(allowedNamespaces) > 1 {
		return "", fmt.Errorf("allowedNamespaces supports at most one entry (got %d)", len(allowedNamespaces))
	}
	if len(allowedServiceAccounts) == 1 {
		ns, name, ok := strings.Cut(allowedServiceAccounts[0], "/")
		if !ok || ns == "" || name == "" {
			return "", fmt.Errorf("allowedServiceAccounts entry %q must be in namespace/name format", allowedServiceAccounts[0])
		}
		if len(allowedNamespaces) == 1 && allowedNamespaces[0] != ns {
			return "", fmt.Errorf("allowedServiceAccounts namespace %q conflicts with allowedNamespaces %q", ns, allowedNamespaces[0])
		}
		return "system:serviceaccount:" + ns + ":" + name, nil
	}
	if len(allowedNamespaces) == 1 {
		return "system:serviceaccount:" + allowedNamespaces[0] + ":*", nil
	}
	return "", nil
}

func parseCIDRs(cidrs []string) ([]*net.IPNet, error) {
	result := make([]*net.IPNet, 0, len(cidrs))
	for _, s := range cidrs {
		_, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", s, err)
		}
		result = append(result, ipnet)
	}
	return result, nil
}

// newDPoPReplayCache returns a DPoP replay cache and, when Valkey storage is
// configured, the underlying valkeygo.Client. The caller must call Close() on
// the returned client (if non-nil) when the cache is no longer needed.
func newDPoPReplayCache(storageCfg config.OAuthStorageConfig) (oauthserver.DPoPReplayCache, valkeygo.Client, error) {
	if storageCfg.Type == storage.BackendValkey && storageCfg.Valkey.URL != "" {
		clientOpts := valkeygo.ClientOption{
			InitAddress: []string{storageCfg.Valkey.URL},
			SelectDB:    storageCfg.Valkey.DB,
		}
		if storageCfg.Valkey.Password != "" {
			clientOpts.Password = storageCfg.Valkey.Password
		}
		if storageCfg.Valkey.TLSEnabled {
			clientOpts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		client, err := valkeygo.NewClient(clientOpts)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create Valkey client for DPoP replay cache: %w", err)
		}
		prefix := storageCfg.Valkey.KeyPrefix
		if prefix == "" {
			prefix = "muster:"
		}
		return storagevalkey.NewDPoPReplayCache(client, prefix+"dpop:"), client, nil
	}
	return oauthserver.NewMemoryDPoPReplayCache(), nil, nil
}

// logEnabledOAuthOptions emits operator-facing Info lines confirming which
// security subsystems came up. Call only after the constructor succeeded.
func logEnabledOAuthOptions(cfg config.OAuthServerConfig, logger *slog.Logger) {
	logger.Info("Security audit logging enabled")
	logger.Info("IP-based rate limiting enabled", "rate", DefaultIPRateLimit, "burst", DefaultIPBurst)
	logger.Info("User-based rate limiting enabled", "rate", DefaultUserRateLimit, "burst", DefaultUserBurst)
	logger.Info("Security-event rate limiting enabled", "rate", DefaultSecurityEventRate, "burst", DefaultSecurityEventBurst)
	logger.Info("Client registration rate limiting enabled", "maxClientsPerIP", DefaultMaxClientsPerIP)
	logger.Info("CIMD metadata-fetch rate limiting enabled", "rate", DefaultMetadataFetchRate, "burst", DefaultMetadataFetchBurst)

}
