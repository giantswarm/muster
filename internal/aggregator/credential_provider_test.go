package aggregator

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/credprovider"
	apiv1alpha1 "github.com/giantswarm/muster/v5/internal/kagent/gen/kagent/api/v1alpha1"
)

// testKagentTarget names a port nothing serves: the client dials lazily, so
// only a bind reaches it.
const testKagentTarget = "grpc://127.0.0.1:1"

func credentialProviderConfig() config.CredentialProviderConfig {
	return config.CredentialProviderConfig{
		Enabled:         true,
		GatewayIdentity: "spiffe://cluster.local/ns/agent-platform/sa/substrate-atenet-egress",
		Releasable:      map[string]string{"github": "https://github.com/apps/giantswarm-workspaces"},
		KagentTarget:    testKagentTarget,
	}
}

// aliceSessions is a kagent controller holding alice's Session s1, which runs
// the actor s1 in atespace kagent; it answers it to alice's bearer alone.
type aliceSessions struct {
	apiv1alpha1.UnimplementedSessionServiceServer
}

func (aliceSessions) GetSession(ctx context.Context, req *apiv1alpha1.GetSessionRequest) (*apiv1alpha1.GetSessionResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if req.GetSessionId() != "s1" || strings.Join(md.Get("authorization"), ",") != "Bearer alice" {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	return &apiv1alpha1.GetSessionResponse{Session: &apiv1alpha1.Session{
		Id:           "s1",
		Agent:        &apiv1alpha1.ResourceReference{Namespace: "kagent", Name: "swarm-operator"},
		A2AAuthority: "s1.kagent.actors.resources.substrate.ate.dev",
	}}, nil
}

// serveKagent serves srv on loopback and returns its grpc:// target.
func serveKagent(t *testing.T, srv apiv1alpha1.SessionServiceServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := grpc.NewServer()
	apiv1alpha1.RegisterSessionServiceServer(s, srv)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	return "grpc://" + lis.Addr().String()
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
	a.config.CredentialProvider.KagentTarget = ""
	_, err = a.newCredentialProvider()
	assert.ErrorContains(t, err, "kagentTarget is required")

	a.config.CredentialProvider.KagentTarget = "http://kagent:8080"
	_, err = a.newCredentialProvider()
	assert.ErrorContains(t, err, "grpc://")

	a.config.CredentialProvider.KagentTarget = testKagentTarget
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
	a.config.CredentialProvider.KagentTarget = serveKagent(t, aliceSessions{})
	t.Cleanup(func() { a.stopCredentialProvider(context.Background()) })
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

	other := `{"actor":"spiffe://substrate-actor.local/actor/kagent/s2","session":"kagent/s1","providers":["github"]}`
	req = httptest.NewRequest(http.MethodPut, credprovider.BindingsPath, strings.NewReader(other))
	req.Header.Set("Authorization", "Bearer alice")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code, "kagent says which actor the Session runs")
	assert.Len(t, srv.Keys(), 1)
}

func TestStartCredentialProviderNeedsTheOAuthServer(t *testing.T) {
	a := &AggregatorServer{}
	require.NoError(t, a.startCredentialProvider(context.Background()), "disabled: nothing to start")

	a.config.CredentialProvider = credentialProviderConfig()
	assert.ErrorContains(t, a.startCredentialProvider(context.Background()), "OAuth server")
	a.stopCredentialProvider(context.Background())
}
