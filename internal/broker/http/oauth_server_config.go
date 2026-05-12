package brokerhttp

import (
	"fmt"
	"log/slog"
	"time"

	oauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/instrumentation"
	"github.com/giantswarm/mcp-oauth/security"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/giantswarm/mcp-oauth/storage"

	"github.com/giantswarm/muster/internal/broker"
	"github.com/giantswarm/muster/internal/config"
)

// newOAuthServerConfig maps the muster OAuth config onto the mcp-oauth Config.
// Pure mapper: no I/O, no goroutines.
func newOAuthServerConfig(cfg config.OAuthServerConfig, refreshTokenTTL time.Duration) *oauthserver.Config {
	return &oauthserver.Config{
		Issuer:                                cfg.BaseURL,
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
		TrustedPublicRegistrationSchemes:      cfg.TrustedPublicRegistrationSchemes,
		TrustedPublicRegistrationRedirectURIs: cfg.TrustedPublicRegistrationRedirectURIs,
		AllowLocalhostRedirectURIs:            cfg.AllowLocalhostRedirectURIs,
		TrustedAudiences:                      cfg.TrustedAudiences,
	}
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
		oauth.WithAuditor(security.NewAuditor(logger, true)),
		oauth.WithRateLimiter(security.NewRateLimiter(DefaultIPRateLimit, DefaultIPBurst, logger)),
		oauth.WithUserRateLimiter(security.NewRateLimiter(DefaultUserRateLimit, DefaultUserBurst, logger)),
		oauth.WithSecurityEventRateLimiter(security.NewRateLimiter(DefaultSecurityEventRate, DefaultSecurityEventBurst, logger)),
		oauth.WithClientRegistrationRateLimiter(security.NewClientRegistrationRateLimiterWithConfig(
			DefaultMaxClientsPerIP,
			security.DefaultRegistrationWindow,
			security.DefaultMaxRegistrationEntries,
			logger,
		)),
	}

	// Valkey wires its own encryptor on the store; only memory storage needs WithEncryptor here.
	if cfg.EncryptionKey != "" && cfg.Storage.Type != storage.BackendValkey {
		keyBytes, err := broker.DecodeEncryptionKey(cfg.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode encryption key: %w", err)
		}
		encryptor, err := security.NewEncryptor(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to create encryptor: %w", err)
		}
		opts = append(opts, oauth.WithEncryptor(encryptor))
	}

	return opts, nil
}

// logEnabledOAuthOptions emits operator-facing Info lines confirming which
// security subsystems came up. Call only after the constructor succeeded.
func logEnabledOAuthOptions(cfg config.OAuthServerConfig, logger *slog.Logger) {
	logger.Info("Security audit logging enabled")
	logger.Info("IP-based rate limiting enabled", "rate", DefaultIPRateLimit, "burst", DefaultIPBurst)
	logger.Info("User-based rate limiting enabled", "rate", DefaultUserRateLimit, "burst", DefaultUserBurst)
	logger.Info("Security-event rate limiting enabled", "rate", DefaultSecurityEventRate, "burst", DefaultSecurityEventBurst)
	logger.Info("Client registration rate limiting enabled", "maxClientsPerIP", DefaultMaxClientsPerIP)

	if cfg.EncryptionKey != "" && cfg.Storage.Type != storage.BackendValkey {
		logger.Info("Token encryption at rest enabled (AES-256-GCM)")
	}
}
