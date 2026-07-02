package server

import (
	"context"
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
	"github.com/giantswarm/mcp-oauth/storage/valkey"
	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	musteroauth "github.com/giantswarm/muster/internal/oauth"
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
		// The forwarded-ID-token (TrustedAudiences) JWKS validation in
		// mcp-oauth is gated on this top-level flag. Mirror the provider-side
		// intent: when the operator has opted into a private-IP Dex via
		// Dex.AllowPrivateIPOIDC, the forwarded-token JWKS client must accept
		// the same private-IP issuer instead of rejecting it as a DNS-rebinding
		// attack. Public-hostname Dex deployments are unaffected.
		AllowPrivateIPJWKS: cfg.Dex.AllowPrivateIPOIDC,
		// Per-client audience allowlist for brokered RFC 8693 token exchange.
		// Only consulted when an Exchanger is registered (see
		// buildOAuthServerOptions); a miss returns invalid_target.
		TokenExchangeClientAudiences: cfg.TokenExchangeBroker.ClientAudiences,
	}
	if cfg.AllowedOrigins != "" {
		result.CORS.AllowedOrigins = strings.Split(cfg.AllowedOrigins, ",")
		for i, o := range result.CORS.AllowedOrigins {
			result.CORS.AllowedOrigins[i] = strings.TrimSpace(o)
		}
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
			issuers[i] = toTrustedIssuer(iss)
		}
		opts = append(opts, oauthserver.WithTrustedIssuers(issuers))
	}

	if cfg.TokenExchangeBroker.Enabled() {
		if len(cfg.TrustedIssuers) == 0 {
			return nil, fmt.Errorf("tokenExchangeBroker requires at least one trustedIssuers entry to validate subject tokens")
		}
		opts = append(opts, oauthserver.WithExchanger(musteroauth.NewBrokerExchanger(cfg.TokenExchangeBroker)))
		brokerLogger := logger
		if brokerLogger == nil {
			brokerLogger = slog.Default()
		}
		brokerLogger.Info("Brokered RFC 8693 token exchange enabled",
			"targets", len(cfg.TokenExchangeBroker.Targets),
			"brokerClients", len(cfg.TokenExchangeBroker.ClientAudiences))
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

// seedBrokerClients ensures each configured confidential broker client exists
// in the OAuth server's store, recreating its record from the same id+secret on
// every startup. mcp-oauth stores a broker client's record (id -> bcrypt secret
// hash) only in its backing store (Valkey); a store wipe leaves the audience
// allowlist in config and the credentials in the holder's secret, but the
// client record gone, so every exchange returns invalid_client. Seeding makes a
// wipe self-heal.
//
// Best-effort: a missing secret handler (non-Kubernetes mode) or an unresolvable
// secret is logged and skipped rather than failing startup -- the broker would
// surface the missing client later, and crashing muster over a transient secret
// read helps no one.
func seedBrokerClients(ctx context.Context, srv *oauth.Server, broker config.TokenExchangeBrokerConfig, logger *slog.Logger) {
	if len(broker.BrokerClients) == 0 {
		return
	}

	handler := api.GetSecretCredentialsHandler()
	if handler == nil {
		logger.Warn("Cannot seed broker clients: no secret credentials handler registered (requires Kubernetes mode)",
			"brokerClients", len(broker.BrokerClients))
		return
	}

	for clientID, bc := range broker.BrokerClients {
		if bc.ClientCredentialsSecretRef == nil {
			logger.Warn("Skipping broker client seed: no clientCredentialsSecretRef", "client_id", clientID)
			continue
		}
		ref := bc.ClientCredentialsSecretRef
		creds, err := handler.LoadClientCredentials(ctx, &api.ClientCredentialsSecretRef{
			Name:            ref.Name,
			Namespace:       ref.Namespace,
			ClientIDKey:     ref.ClientIDKey,
			ClientSecretKey: ref.ClientSecretKey,
		}, broker.DefaultSecretNamespace)
		if err != nil {
			logger.Warn("Failed to load broker client credentials; skipping seed",
				"client_id", clientID, "secret", ref.Name, "error", err)
			continue
		}

		// The config map key is authoritative for the client id; warn if the
		// secret carries a different one so a misconfiguration is visible.
		if creds.ClientID != "" && creds.ClientID != clientID {
			logger.Warn("Broker client id in secret differs from config key; using config key",
				"config_client_id", clientID, "secret_client_id", creds.ClientID)
		}

		seeded, err := srv.EnsureConfidentialClient(ctx, clientID, creds.ClientSecret, bc.Scopes)
		if err != nil {
			logger.Warn("Failed to seed broker client", "client_id", clientID, "error", err)
			continue
		}
		if seeded {
			logger.Info("Seeded confidential broker client", "client_id", clientID)
		} else {
			logger.Debug("Broker client already present; no seeding needed", "client_id", clientID)
		}
	}
}

func toTrustedIssuer(iss config.TrustedIssuerConfig) oauthserver.TrustedIssuer {
	return oauthserver.TrustedIssuer{
		Issuer:                  iss.Issuer,
		JwksURL:                 iss.JwksURL,
		AllowedAudiences:        iss.AllowedAudiences,
		AllowedScopes:           iss.AllowedScopes,
		AllowedClaims:           iss.AllowedClaims,
		SubjectClaim:            iss.SubjectClaim,
		AllowPrivateIPJWKS:      iss.AllowPrivateIPJWKS,
		AllowPrivateIPJWKSHosts: iss.AllowPrivateIPJWKSHosts,
		AcceptedTypHeaders:      iss.AcceptedTypHeaders,
	}
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
		return valkey.NewDPoPReplayCache(client, prefix+"dpop:"), client, nil
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
