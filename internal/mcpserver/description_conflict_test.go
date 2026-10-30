package mcpserver

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// describingAggregator answers api.AuthorizationServerDescriptionChecker
// with conflict and records what it was asked; the rest of
// api.AggregatorHandler is not used here.
type describingAggregator struct {
	api.AggregatorHandler
	conflict error
	keys     []string
	refs     []string
}

func (a *describingAggregator) DescriptionConflict(key string, auth *api.MCPServerAuth) error {
	a.keys = append(a.keys, key)
	ref := ""
	if r := auth.AuthorizationServer.ClientCredentialsSecretRef; r != nil {
		ref = r.Name
	}
	a.refs = append(a.refs, ref)
	return a.conflict
}

func useDescribingAggregator(t *testing.T, conflict error) *describingAggregator {
	t.Helper()
	aggregator := &describingAggregator{conflict: conflict}
	prev := api.GetAggregator()
	api.RegisterAggregator(aggregator)
	t.Cleanup(func() { api.RegisterAggregator(prev) })
	return aggregator
}

var errOtherHubSpot = fmt.Errorf("MCP server 'hubspot' signs in to https://as.example.com with another client or endpoints: %w", api.ErrAuthorizationServerConflict)

func TestDescriptionConflict_RefusedBeforeAnyWrite(t *testing.T) {
	t.Run("create with clientCredentials", func(t *testing.T) {
		aggregator := useDescribingAggregator(t, errOtherHubSpot)
		h := newCredsHarness(t, nil)
		text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
		require.True(t, isErr)
		assert.Contains(t, text, "Cannot create MCP server 'test-server': MCP server 'hubspot' signs in to https://as.example.com")
		assert.Contains(t, text, "use an existing Secret with that server's client, or another issuer")
		assert.Empty(t, h.writes)
		require.Equal(t, []string{"test-server"}, aggregator.keys)
		assert.Equal(t, []string{"test-server-oauth-client-new"}, aggregator.refs, "checked with a client of its own")
	})
	t.Run("update that writes the auth", func(t *testing.T) {
		useDescribingAggregator(t, errOtherHubSpot)
		h := newCredsHarness(t, generatedServer("gitops-client"))
		text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{
			"name":              "test-server",
			"clientCredentials": map[string]interface{}{"clientId": testClientID, "clientSecret": testClientSecret},
		})
		require.True(t, isErr)
		assert.Contains(t, text, "Cannot update MCP server 'test-server'")
		assert.Empty(t, h.writes, "no Secret is created for a refused update")
	})
	t.Run("an unrelated update is not blocked", func(t *testing.T) {
		aggregator := useDescribingAggregator(t, errOtherHubSpot)
		h := newCredsHarness(t, generatedServer("gitops-client"))
		text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{"name": "test-server", "description": "x"})
		require.False(t, isErr, text)
		assert.Empty(t, aggregator.keys)
	})
	t.Run("no conflict", func(t *testing.T) {
		useDescribingAggregator(t, nil)
		h := newCredsHarness(t, nil)
		text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
		require.False(t, isErr, text)
	})
}
