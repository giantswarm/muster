package server

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"time"

	oauth "github.com/giantswarm/mcp-oauth"
	dpopvalkey "github.com/giantswarm/mcp-oauth/dpop/valkey"
	"github.com/giantswarm/mcp-oauth/instrumentation"
	"github.com/giantswarm/mcp-oauth/security"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/giantswarm/mcp-oauth/storage"
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

	// Valkey wires its own encryptor on the store; only memory storage needs WithEncryptor here.
	if cfg.EncryptionKey != "" && cfg.Storage.Type != storage.BackendValkey {
		keyBytes, err := security.DecodeKey(cfg.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode encryption key: %w", err)
		}
		encryptor, err := security.NewEncryptor(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to create encryptor: %w", err)
		}
		opts = append(opts, oauth.WithEncryptor(encryptor))
	}

	if len(cfg.KubernetesSATrusts) > 0 {
		trusts := make([]oauthserver.KubernetesSATrust, len(cfg.KubernetesSATrusts))
		for i, t := range cfg.KubernetesSATrusts {
			trusts[i] = oauthserver.KubernetesSATrust{
				Issuer:                 t.Issuer,
				JwksURL:                t.JwksURL,
				AllowedAudiences:       t.AllowedAudiences,
				AllowedScopes:          t.AllowedScopes,
				AllowedNamespaces:      t.AllowedNamespaces,
				AllowedServiceAccounts: t.AllowedServiceAccounts,
			}
		}
		opts = append(opts, oauthserver.WithKubernetesSATrust(trusts))
	}

	if len(cfg.TrustedIssuers) > 0 {
		issuers := make([]oauthserver.TrustedIssuer, len(cfg.TrustedIssuers))
		for i, iss := range cfg.TrustedIssuers {
			issuers[i] = oauthserver.TrustedIssuer{
				Issuer:           iss.Issuer,
				JwksURL:          iss.JwksURL,
				AllowedAudiences: iss.AllowedAudiences,
				AllowedScopes:    iss.AllowedScopes,
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

	dpopCache, err := newDPoPReplayCache(cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("failed to create DPoP replay cache: %w", err)
	}
	opts = append(opts, oauthserver.WithDPoPReplayCache(dpopCache))

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

// newDPoPReplayCache returns a Valkey-backed DPoP replay cache when Valkey storage is
// configured, falling back to an in-memory cache for single-process deployments.
func newDPoPReplayCache(storageCfg config.OAuthStorageConfig) (oauthserver.DPoPReplayCache, error) {
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
			return nil, fmt.Errorf("failed to create Valkey client for DPoP replay cache: %w", err)
		}
		prefix := storageCfg.Valkey.KeyPrefix
		if prefix == "" {
			prefix = "muster:"
		}
		return dpopvalkey.New(client, prefix+"dpop:"), nil
	}
	return oauthserver.NewMemoryDPoPReplayCache(), nil
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

	if cfg.EncryptionKey != "" && cfg.Storage.Type != storage.BackendValkey {
		logger.Info("Token encryption at rest enabled (AES-256-GCM)")
	}
}
