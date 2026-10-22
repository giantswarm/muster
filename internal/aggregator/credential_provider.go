package aggregator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/credprovider"
	"github.com/giantswarm/muster/v5/internal/kagent"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// credentialProviderStopTimeout bounds the drain of in-flight FetchSecret
// calls at shutdown.
const credentialProviderStopTimeout = 5 * time.Second

// newCredentialProvider builds the egress gateway's credential provider from
// the configuration, its bindings on the shared Valkey client when muster
// runs on Valkey. Returns nil when it is disabled.
func (a *AggregatorServer) newCredentialProvider() (*credprovider.Provider, error) {
	cfg := a.config.CredentialProvider
	if !cfg.Enabled {
		return nil, nil
	}
	ttl := credprovider.DefaultBindingTTL
	if cfg.BindingTTL != "" {
		d, err := time.ParseDuration(cfg.BindingTTL)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("credential provider: bindingTTL %q is not a positive duration", cfg.BindingTTL)
		}
		ttl = d
	}
	var store credprovider.Store = credprovider.NewMemoryStore()
	if a.valkeyClient != nil {
		store = credprovider.NewValkeyStore(a.valkeyClient, a.valkeyKeyPrefix, a.valkeyEncryptor)
	}
	releasable, gitUsers, err := releasableWithProviderApps(cfg.Releasable, cfg.GitUsers, a.config.ProviderApps)
	if err != nil {
		return nil, fmt.Errorf("credential provider: %w", err)
	}
	if cfg.KagentTarget == "" {
		return nil, fmt.Errorf("credential provider: kagentTarget is required: a bind reads its Session from the kagent controller")
	}
	sessions, err := kagent.Dial(cfg.KagentTarget)
	if err != nil {
		return nil, fmt.Errorf("credential provider: %w", err)
	}
	provider, err := credprovider.NewProvider(credprovider.Options{
		Authority:       cfg.Authority,
		GatewayIdentity: cfg.GatewayIdentity,
		Releasable:      releasable,
		GitUsers:        gitUsers,
		MusterBearer:    cfg.ActorBearer.Enabled,
		BindingTTL:      ttl,
		Store:           store,
		Grants:          releaseSubjectGrant,
		Sessions:        kagentSessionLookup(sessions),
	})
	if err != nil {
		_ = sessions.Close()
		return nil, err
	}
	a.kagentSessions = sessions
	return provider, nil
}

// kagentSessionLookup reads a Session through the kagent client, its refusals
// mapped onto the credential provider's.
func kagentSessionLookup(client *kagent.Client) credprovider.SessionLookup {
	return func(ctx context.Context, bearer, sessionID string) (*credprovider.KagentSession, error) {
		s, err := client.GetSession(ctx, bearer, sessionID)
		switch {
		case errors.Is(err, kagent.ErrSessionNotFound):
			return nil, fmt.Errorf("%w: %s", credprovider.ErrSessionNotFound, sessionID)
		case errors.Is(err, kagent.ErrUnauthenticated), errors.Is(err, kagent.ErrNoToken):
			return nil, credprovider.ErrSessionCallerRefused
		case err != nil:
			return nil, err
		}
		return &credprovider.KagentSession{AgentNamespace: s.AgentNamespace, A2AAuthority: s.A2AAuthority}, nil
	}
}

// releaseSubjectGrant is the provider's GrantSource: the person's grant held
// by the OAuth proxy, refreshed when due.
func releaseSubjectGrant(ctx context.Context, subject, issuer string) *api.OAuthToken {
	handler := api.GetOAuthHandler()
	if handler == nil || !handler.IsEnabled() {
		return nil
	}
	releaser, ok := handler.(api.GrantReleaser)
	if !ok {
		return nil
	}
	return releaser.SubjectGrantForRelease(ctx, subject, issuer)
}

// registerSessionBindingRoutes builds the credential provider and puts its
// binding endpoints on the authenticated mux. A disabled provider registers
// nothing.
func (a *AggregatorServer) registerSessionBindingRoutes(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) error {
	provider, err := a.newCredentialProvider()
	if err != nil || provider == nil {
		return err
	}
	mux.Handle("PUT "+credprovider.BindingsPath, authenticate(http.HandlerFunc(provider.HandleBind)))
	mux.Handle("DELETE "+credprovider.BindingsPath, authenticate(http.HandlerFunc(provider.HandleUnbind)))
	a.credProvider = provider
	return nil
}

// startCredentialProvider starts the FetchSecret listener when the provider
// was built. A listener that cannot start is an error: an installation that
// enabled it relies on it for every workspace Session.
func (a *AggregatorServer) startCredentialProvider(ctx context.Context) error {
	cfg := a.config.CredentialProvider
	if !cfg.Enabled {
		return nil
	}
	if a.credProvider == nil {
		return fmt.Errorf("credential provider: needs the OAuth server (aggregator.oauth.server.enabled), whose identity the session binding endpoints authenticate")
	}
	srv, err := credprovider.Listen(ctx, cfg.ListenAddress, credprovider.TLSFiles{
		ServerCredBundle: cfg.ServerCredBundle,
		ClientCAFile:     cfg.ClientCAFile,
	}, a.credProvider)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.credProviderServer = srv
	a.mu.Unlock()
	logging.InfoWithAttrs("Aggregator", "Credential provider listening",
		slog.String("addr", srv.Addr()),
		slog.String("gateway", a.credProvider.GatewayIdentity()))
	return nil
}

// stopCredentialProvider drains and stops the FetchSecret listener.
func (a *AggregatorServer) stopCredentialProvider(ctx context.Context) {
	a.mu.Lock()
	srv := a.credProviderServer
	a.credProviderServer = nil
	sessions := a.kagentSessions
	a.kagentSessions = nil
	a.mu.Unlock()
	if sessions != nil {
		_ = sessions.Close()
	}
	if srv == nil {
		return
	}
	stopCtx, cancel := context.WithTimeout(ctx, credentialProviderStopTimeout)
	defer cancel()
	srv.Stop(stopCtx)
}
