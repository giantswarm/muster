package mcpserver

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func toolListWithTTL(ttlMillis int64) *mcp.ListToolsResult {
	result := &mcp.ListToolsResult{}
	result.SetCacheHints(ttlMillis, mcp.CacheScopePrivate)
	return result
}

func TestCallTool_ToolDefinitionsExpire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		observed := &observedToolClient{
			list: func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
				return toolListWithTTL(1000), nil
			},
			call: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("called"), nil
			},
		}
		connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
		for range 2 {
			_, err := connection.callTool(t.Context(), "read", nil)
			require.NoError(t, err)
		}
		require.EqualValues(t, 1, observed.lists.Load())
		<-time.After(time.Second)
		_, err := connection.callTool(t.Context(), "read", nil)
		require.NoError(t, err)
		require.EqualValues(t, 2, observed.lists.Load())
	})
}

func TestCallTool_StaleCacheHintsAreNotReused(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		result *mcp.ListToolsResult
	}{
		{name: "absent", result: &mcp.ListToolsResult{}},
		{name: "zero", result: toolListWithTTL(0)},
		{name: "negative", result: toolListWithTTL(-1)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			observed := &observedToolClient{
				list: func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
					return testCase.result, nil
				},
				call: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return mcp.NewToolResultText("called"), nil
				},
			}
			connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
			for range 2 {
				_, err := connection.callTool(t.Context(), "read", nil)
				require.NoError(t, err)
			}
			require.EqualValues(t, 2, observed.lists.Load())
		})
	}
}

func TestCallTool_ToolListNotificationInvalidatesFreshDefinitions(t *testing.T) {
	observed := &observedToolClient{
		list: func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
			return toolListWithTTL(60000), nil
		},
		call: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("called"), nil
		},
	}
	connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
	notification := mcp.JSONRPCNotification{Notification: mcp.Notification{Method: string(mcp.MethodNotificationToolsListChanged)}}
	var received []mcp.JSONRPCNotification
	connection.onNotification(func(notification mcp.JSONRPCNotification) {
		received = append(received, notification)
	})
	_, err := connection.callTool(t.Context(), "read", nil)
	require.NoError(t, err)
	connection.dispatchNotification(notification)
	_, err = connection.callTool(t.Context(), "read", nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, observed.lists.Load())
	require.Equal(t, []mcp.JSONRPCNotification{notification}, received)
}

func TestCallTool_PaginatedDefinitionsUseEarliestExpiry(t *testing.T) {
	for _, shorterPage := range []string{"first", "second"} {
		t.Run(shorterPage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				observed := &observedToolClient{
					list: func(_ context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
						page := toolListWithTTL(60000)
						if request.Params.Cursor == "" {
							page.NextCursor = "second"
							page.Tools = []mcp.Tool{mcp.NewTool("first")}
							if shorterPage == "first" {
								page.SetCacheHints(1000, mcp.CacheScopePrivate)
							}
						} else {
							require.Equal(t, mcp.Cursor("second"), request.Params.Cursor)
							page.Tools = []mcp.Tool{mcp.NewTool("second")}
							if shorterPage == "second" {
								page.SetCacheHints(1000, mcp.CacheScopePrivate)
							}
						}
						return page, nil
					},
					call: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
						return mcp.NewToolResultText("called"), nil
					},
				}
				connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
				tools, err := connection.listTools(t.Context())
				require.NoError(t, err)
				require.Len(t, tools, 2)
				_, err = connection.callTool(t.Context(), "second", nil)
				require.NoError(t, err)
				require.EqualValues(t, 2, observed.lists.Load())
				<-time.After(time.Second)
				_, err = connection.callTool(t.Context(), "second", nil)
				require.NoError(t, err)
				require.EqualValues(t, 4, observed.lists.Load())
			})
		})
	}
}

func TestListTools_RejectsRepeatedPaginationCursor(t *testing.T) {
	observed := &observedToolClient{list: func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
		page := toolListWithTTL(60000)
		page.NextCursor = "repeated"
		return page, nil
	}}
	connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
	_, err := connection.listTools(t.Context())
	require.ErrorContains(t, err, "repeated a pagination cursor")
	require.EqualValues(t, 2, observed.lists.Load())
}

func TestCallTool_FailedLaterPageDoesNotMarkDefinitionsFresh(t *testing.T) {
	pageError := errors.New("second page unavailable")
	observed := &observedToolClient{}
	observed.list = func(_ context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
		page := toolListWithTTL(60000)
		if request.Params.Cursor == "" {
			page.NextCursor = "second"
		} else if observed.lists.Load() == 2 {
			return nil, pageError
		}
		return page, nil
	}
	observed.call = func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("called"), nil
	}
	connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
	_, err := connection.callTool(t.Context(), "read", nil)
	require.ErrorIs(t, err, pageError)
	require.Zero(t, observed.calls.Load())
	_, err = connection.callTool(t.Context(), "read", nil)
	require.NoError(t, err)
	require.EqualValues(t, 4, observed.lists.Load())
}

func TestCallTool_ToolDefinitionTTLCannotOverflow(t *testing.T) {
	observed := &observedToolClient{
		list: func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
			return toolListWithTTL(1<<63 - 1), nil
		},
		call: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("called"), nil
		},
	}
	connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
	for range 2 {
		_, err := connection.callTool(t.Context(), "read", nil)
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, observed.lists.Load())
}

func TestCallTool_DefinitionsCanExpireWhileFetchingLaterPages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		observed := &observedToolClient{
			list: func(_ context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
				if request.Params.Cursor == "" {
					page := toolListWithTTL(1000)
					page.NextCursor = "second"
					return page, nil
				}
				<-time.After(2 * time.Second)
				return toolListWithTTL(60000), nil
			},
			call: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("called"), nil
			},
		}
		connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
		for range 2 {
			_, err := connection.callTool(t.Context(), "read", nil)
			require.NoError(t, err)
		}
		require.EqualValues(t, 4, observed.lists.Load())
	})
}

func TestListTools_CancellationBetweenPagesStopsListing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	observed := &observedToolClient{list: func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
		page := toolListWithTTL(60000)
		page.NextCursor = "second"
		cancel()
		return page, nil
	}}
	connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
	_, err := connection.listTools(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.EqualValues(t, 1, observed.lists.Load())
}
