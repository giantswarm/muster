package aggregator

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"syscall"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// flakyListClient fails its first tools listings with err, then lists tools.
type flakyListClient struct {
	mockMCPClient
	failures int
	err      error
	lists    int
}

func (c *flakyListClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	c.lists++
	if c.lists <= c.failures {
		return nil, c.err
	}
	return c.mockMCPClient.ListTools(ctx)
}

// resetByPeer is the error a POST meets on a kept-alive connection the
// backend's old process closed, as the streamable-http transport wraps it.
func resetByPeer() error {
	return fmt.Errorf("failed to list tools: %w", transport.NewError(fmt.Errorf("failed to send request: %w",
		&url.Error{Op: "Post", URL: "http://backend/mcp", Err: syscall.ECONNRESET})))
}

// A listing lost on a connection the backend closed is listed again once, on
// a new connection, instead of waiting for the next poll.
func TestRelistDeclared_ListsAgainAfterAClosedConnection(t *testing.T) {
	client := &flakyListClient{
		mockMCPClient: mockMCPClient{initialized: true, tools: []mcp.Tool{{Name: "report"}}},
		failures:      1,
		err:           resetByPeer(),
	}

	tools, _, _, err := relistDeclared(t.Context(), client, "rolled", refreshByPoll)

	require.NoError(t, err)
	require.Equal(t, []mcp.Tool{{Name: "report"}}, tools)
	require.Equal(t, 2, client.lists)
}

// The repeat is one: a backend that keeps closing the connection fails the
// listing, and any other failure is not repeated.
func TestRelistDeclared_RepeatsOnlyOnceAndOnlyAClosedConnection(t *testing.T) {
	keepsClosing := &flakyListClient{mockMCPClient: mockMCPClient{initialized: true}, failures: 2, err: resetByPeer()}
	_, _, _, err := relistDeclared(t.Context(), keepsClosing, "rolled", refreshByPoll)
	require.ErrorIs(t, err, syscall.ECONNRESET)
	require.Equal(t, 2, keepsClosing.lists)

	refused := &flakyListClient{mockMCPClient: mockMCPClient{initialized: true}, failures: 1, err: errors.New("401 Unauthorized")}
	_, _, _, err = relistDeclared(t.Context(), refused, "rolled", refreshByPoll)
	require.Error(t, err)
	require.Equal(t, 1, refused.lists)
}
