package server

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	oauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/providers/mock"
	"github.com/giantswarm/mcp-oauth/storage/memory"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
)

type stubSecretHandler struct {
	creds *api.ClientCredentials
	err   error
}

func (s *stubSecretHandler) LoadClientCredentials(_ context.Context, _ *api.ClientCredentialsSecretRef, _ string) (*api.ClientCredentials, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.creds, nil
}

func (s *stubSecretHandler) LoadSecretKey(_ context.Context, _ *api.ClientCredentialsSecretRef, _ string, _ string) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func newTestOAuthServer(t *testing.T) *oauth.Server {
	t.Helper()
	store := memory.New()
	srv, err := oauth.NewServerWithCombined(mock.NewProvider(), store, &oauth.ServerConfig{
		Issuer:            "https://muster.example.com",
		AllowInsecureHTTP: false,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewServerWithCombined: %v", err)
	}
	return srv
}

func TestSeedBrokerClients(t *testing.T) {
	ctx := context.Background()
	const (
		clientID = "broker-client-id"
		secret   = "broker-client-secret"
	)

	t.Run("seeds client from secret", func(t *testing.T) {
		srv := newTestOAuthServer(t)
		prev := api.GetSecretCredentialsHandler()
		api.RegisterSecretCredentialsHandler(&stubSecretHandler{
			creds: &api.ClientCredentials{ClientID: clientID, ClientSecret: secret},
		})
		t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })

		broker := config.TokenExchangeBrokerConfig{
			DefaultSecretNamespace: "agentic-platform",
			BrokerClients: map[string]config.BrokerClientConfig{
				clientID: {ClientCredentialsSecretRef: &config.BrokerSecretRefConfig{Name: "muster-broker-clients"}},
			},
		}

		seedBrokerClients(ctx, srv, broker, slog.Default())

		if err := srv.ValidateClientCredentials(ctx, clientID, secret); err != nil {
			t.Errorf("seeded client should validate: %v", err)
		}
	})

	t.Run("no handler is a no-op", func(t *testing.T) {
		srv := newTestOAuthServer(t)
		prev := api.GetSecretCredentialsHandler()
		api.RegisterSecretCredentialsHandler(nil)
		t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })

		broker := config.TokenExchangeBrokerConfig{
			BrokerClients: map[string]config.BrokerClientConfig{
				clientID: {ClientCredentialsSecretRef: &config.BrokerSecretRefConfig{Name: "x"}},
			},
		}
		// Must not panic and must not seed.
		seedBrokerClients(ctx, srv, broker, slog.Default())
		if _, err := srv.GetClient(ctx, clientID); err == nil {
			t.Error("expected no client to be seeded without a handler")
		}
	})

	t.Run("empty config is a no-op", func(t *testing.T) {
		srv := newTestOAuthServer(t)
		seedBrokerClients(ctx, srv, config.TokenExchangeBrokerConfig{}, slog.Default())
	})
}
