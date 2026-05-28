package server

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"time"

	oauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/instrumentation"
	"github.com/giantswarm/mcp-oauth/security"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/giantswarm/mcp-oauth/storage"
	dpopvalkey "github.com/giantswarm/mcp-oauth/storage/valkey"
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

	if len(cfg.TrustedIssuers) > 0 {
		issuers := make([]oauthserver.TrustedIssuer, len(cfg.TrustedIssuers))
		for i, iss := range cfg.TrustedIssuers {
			issuers[i] = oauthserver.TrustedIssuer{
				Issuer:           iss.Issuer,
				JwksURL:          iss.JwksURL,
				AllowedAudiences: iss.AllowedAudiences,
				AllowedScopes:    iss.AllowedScopes,
				AllowedClaims:    iss.AllowedClaims,
			}
		}
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
		return dpopvalkey.NewDPoPReplayCache(client, prefix+"dpop:"), client, nil
	}
	return oauthserver.NewMemoryDPoPReplayCache(), nil, nil
}

// logEnabledOAuthOptions emits operator-facing Info lines confirming which
// security subsystems came up. Call only after the constructor succeeded.
func logEnabledOAuthOptions(logger *slog.Logger) {
	logger.Info("Security audit logging enabled")
	logger.Info("IP-based rate limiting enabled", "rate", DefaultIPRateLimit, "burst", DefaultIPBurst)
	logger.Info("User-based rate limiting enabled", "rate", DefaultUserRateLimit, "burst", DefaultUserBurst)
	logger.Info("Security-event rate limiting enabled", "rate", DefaultSecurityEventRate, "burst", DefaultSecurityEventBurst)
	logger.Info("Client registration rate limiting enabled", "maxClientsPerIP", DefaultMaxClientsPerIP)
	logger.Info("CIMD metadata-fetch rate limiting enabled", "rate", DefaultMetadataFetchRate, "burst", DefaultMetadataFetchBurst)

}
