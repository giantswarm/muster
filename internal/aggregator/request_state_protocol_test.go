package aggregator

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

func TestIndependentRequestStateKeyAcrossHTTPReplicas(t *testing.T) {
	key := newKey(t)
	backend := newInputBackend(t)
	settings := AggregatorConfig{RequestState: config.RequestStateConfig{Key: key}}
	firstURL := newInputMuster(t, map[string]*inputBackend{testElicitingServer: backend}, settings)
	secondURL := newInputMuster(t, map[string]*inputBackend{testElicitingServer: backend}, settings)
	firstClient := connectToMuster(t, firstURL, mcp.ProtocolVersion20260728, urlAndForm(), nil)
	secondClient := connectToMuster(t, secondURL, mcp.ProtocolVersion20260728, urlAndForm(), nil)
	first, err := call(t, firstClient, testElicitingServer+"/create", mcp.MultiRoundTripParams{})
	require.NoError(t, err)
	require.True(t, first.NeedsInput())
	require.NotEmpty(t, first.RequestState)
	retried, err := call(t, secondClient, testElicitingServer+"/create", mcp.MultiRoundTripParams{InputResponses: accepted(), RequestState: first.RequestState})
	require.NoError(t, err)
	require.Contains(t, textOf(t, retried), "accept "+testBackendState)
}
