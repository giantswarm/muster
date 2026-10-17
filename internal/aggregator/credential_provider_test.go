package aggregator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/credprovider"
)

func credentialProviderConfig() config.CredentialProviderConfig {
	return config.CredentialProviderConfig{
		Enabled:         true,
		GatewayIdentity: "spiffe://cluster.local/ns/agent-platform/sa/substrate-atenet-egress",
		Releasable:      map[string]string{"github": "https://github.com/apps/giantswarm-workspaces"},
	}
}

func TestNewCredentialProvider(t *testing.T) {
	a := &AggregatorServer{}
	p, err := a.newCredentialProvider()
	require.NoError(t, err)
	assert.Nil(t, p, "disabled by default")

	a.config.CredentialProvider = credentialProviderConfig()
	p, err = a.newCredentialProvider()
	require.NoError(t, err)
	require.NotNil(t, p)

	a.config.CredentialProvider.BindingTTL = "fortnight"
	_, err = a.newCredentialProvider()
	assert.ErrorContains(t, err, "bindingTTL")

	a.config.CredentialProvider.BindingTTL = "336h"
	a.config.CredentialProvider.Releasable = nil
	_, err = a.newCredentialProvider()
	assert.Error(t, err)
}

func TestSessionBindingRoutesAreAuthenticatedAndShared(t *testing.T) {
	srv := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)

	a := &AggregatorServer{valkeyClient: client, valkeyKeyPrefix: "muster:"}
	a.config.CredentialProvider = credentialProviderConfig()
	mux := http.NewServeMux()
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer alice" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(api.WithSubject(r.Context(), "alice")))
		})
	}
	require.NoError(t, a.registerSessionBindingRoutes(mux, authenticate))
	require.NotNil(t, a.credProvider)

	body := `{"actor":"spiffe://substrate-actor.local/actor/kagent/s1","session":"kagent/s1","providers":["github"]}`
	req := httptest.NewRequest(http.MethodPut, credprovider.BindingsPath, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "the bind needs the person's bearer")

	req = httptest.NewRequest(http.MethodPut, credprovider.BindingsPath, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer alice")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Len(t, srv.Keys(), 1, "the binding is in Valkey, shared by every replica")
}

func TestStartCredentialProviderNeedsTheOAuthServer(t *testing.T) {
	a := &AggregatorServer{}
	require.NoError(t, a.startCredentialProvider(context.Background()), "disabled: nothing to start")

	a.config.CredentialProvider = credentialProviderConfig()
	assert.ErrorContains(t, a.startCredentialProvider(context.Background()), "OAuth server")
	a.stopCredentialProvider(context.Background())
}
