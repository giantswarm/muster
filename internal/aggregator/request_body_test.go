package aggregator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A body beyond the bound is refused before it is read in full, and the
// endpoint keeps answering.
func TestAggregatorServer_RequestBodyIsBounded(t *testing.T) {
	ctx := context.Background()
	server, err := NewAggregatorServer(ctx, AggregatorConfig{Host: "localhost", Port: 0}, nil)
	require.NoError(t, err)
	require.NoError(t, server.Start(ctx))
	defer func() { _ = server.Stop(ctx) }()
	require.NotEmpty(t, server.httpServer)

	ts := httptest.NewServer(server.httpServer[0].Handler)
	defer ts.Close()
	post := func(body string) int {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"` + strings.Repeat("x", maxRequestBodyBytes) + `"}}`
	assert.Equal(t, http.StatusRequestEntityTooLarge, post(big))
	assert.Equal(t, http.StatusOK, post(`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
}
