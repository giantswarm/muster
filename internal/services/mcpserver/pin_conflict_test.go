package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/services"
)

// conflictAggregator answers api.AuthorizationServerConflictChecker; the
// rest of api.AggregatorHandler is not used here.
type conflictAggregator struct {
	api.AggregatorHandler
	conflict error
}

func (a *conflictAggregator) AuthorizationServerConflict(string) error { return a.conflict }

func TestConflictingAuthorizationServerIsTheServersStatus(t *testing.T) {
	aggregator := &conflictAggregator{
		conflict: fmt.Errorf("server newer: %w: server owner, whose description of https://as.example.com is used", api.ErrAuthorizationServerConflict),
	}
	prev := api.GetAggregator()
	api.RegisterAggregator(aggregator)
	t.Cleanup(func() { api.RegisterAggregator(prev) })

	backend := startHTTPStatusServer(t, http.StatusUnauthorized)
	svc, err := NewService(&api.MCPServer{
		Name: "newer",
		Type: api.MCPServerTypeStreamableHTTP,
		URL:  backend.URL + "/mcp",
		Auth: &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
			Issuer:                "https://as.example.com",
			AuthorizationEndpoint: "https://elsewhere.example.com/authorize",
			TokenEndpoint:         "https://elsewhere.example.com/token",
		}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	err = svc.Start(t.Context())
	require.ErrorIs(t, err, api.ErrAuthorizationServerConflict)
	assert.False(t, api.IsAuthRequiredError(err), "a conflict is a failed start, not a server waiting for a sign-in")
	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.NotNil(t, svc.GetNextRetryAfter(), "the conflict ends without a restart of this server")
	assert.Equal(t, api.FailureReasonAuthorizationServerConflict, svc.GetServiceData()[api.ServiceDataFailureReason])

	aggregator.conflict = nil
	require.True(t, api.IsAuthRequiredError(svc.Start(t.Context())))
	assert.Equal(t, services.StateAuthRequired, svc.GetState())
	assert.NotContains(t, svc.GetServiceData(), api.ServiceDataFailureReason)
}
