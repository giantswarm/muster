package mcpserver

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestCallTool_ConcurrentCallsShareParamHeaderDiscovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		observed := &observedToolClient{
			list: func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
				close(started)
				<-release
				return &mcp.ListToolsResult{}, nil
			},
			call: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("called"), nil
			},
		}
		connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
		results := make(chan error, 8)
		for range 8 {
			go func() {
				_, err := connection.callTool(t.Context(), "pull_request_read", nil)
				results <- err
			}()
		}
		<-started
		synctest.Wait()
		require.EqualValues(t, 1, observed.lists.Load())
		close(release)
		for range 8 {
			require.NoError(t, <-results)
		}
		require.EqualValues(t, 8, observed.calls.Load())
	})
}

func TestCallTool_ParamHeaderDiscoveryWaitCanBeCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		observed := &observedToolClient{
			list: func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
				close(started)
				<-release
				return &mcp.ListToolsResult{}, nil
			},
			call: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("called"), nil
			},
		}
		connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
		first := make(chan error, 1)
		go func() {
			_, err := connection.callTool(t.Context(), "pull_request_read", nil)
			first <- err
		}()
		<-started
		ctx, cancel := context.WithCancel(t.Context())
		second := make(chan error, 1)
		go func() {
			_, err := connection.callTool(ctx, "pull_request_read", nil)
			second <- err
		}()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-second, context.Canceled)
		require.Zero(t, observed.calls.Load())
		require.EqualValues(t, 1, observed.lists.Load())
		close(release)
		require.NoError(t, <-first)
	})
}

func TestCallTool_ParamHeaderDiscoveryWaiterRetriesFailedListing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		discoveryError := errors.New("discovery unavailable")
		observed := &observedToolClient{}
		observed.list = func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
			if observed.lists.Load() == 1 {
				close(started)
				<-release
				return nil, discoveryError
			}
			return &mcp.ListToolsResult{}, nil
		}
		observed.call = func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("called"), nil
		}
		connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
		first := make(chan error, 1)
		go func() {
			_, err := connection.callTool(t.Context(), "pull_request_read", nil)
			first <- err
		}()
		<-started
		second := make(chan error, 1)
		go func() {
			_, err := connection.callTool(t.Context(), "pull_request_read", nil)
			second <- err
		}()
		synctest.Wait()
		close(release)
		require.ErrorIs(t, <-first, discoveryError)
		require.NoError(t, <-second)
		require.EqualValues(t, 2, observed.lists.Load())
		require.EqualValues(t, 1, observed.calls.Load())
	})
}

func TestCallTool_ConcurrentMismatchesShareParamHeaderRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const callers = 8
		started := make(chan struct{}, callers)
		release := make(chan struct{})
		observed := &observedToolClient{}
		observed.list = func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
			if observed.lists.Load() == 1 {
				return toolListWithTTL(60000), nil
			}
			return toolListWithTTL(0), nil
		}
		observed.call = func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if observed.calls.Load() <= callers {
				started <- struct{}{}
				<-release
				return nil, mcp.HeaderMismatchError{Header: "Mcp-Param-Owner", Reason: "missing header"}
			}
			return mcp.NewToolResultText("called"), nil
		}
		connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
		results := make(chan error, callers)
		for range callers {
			go func() {
				_, err := connection.callTool(t.Context(), "read", nil)
				results <- err
			}()
		}
		for range callers {
			<-started
		}
		close(release)
		for range callers {
			require.NoError(t, <-results)
		}
		require.EqualValues(t, 2, observed.lists.Load())
		require.EqualValues(t, callers*2, observed.calls.Load())
	})
}

func TestCallTool_NotificationDuringDiscoveryRequiresAnotherListing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		observed := &observedToolClient{}
		observed.list = func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
			if observed.lists.Load() == 1 {
				close(started)
				<-release
			}
			return toolListWithTTL(60000), nil
		}
		observed.call = func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("called"), nil
		}
		connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
		results := make(chan error, 2)
		for range 2 {
			go func() {
				_, err := connection.callTool(t.Context(), "read", nil)
				results <- err
			}()
		}
		<-started
		synctest.Wait()
		connection.dispatchNotification(mcp.JSONRPCNotification{Notification: mcp.Notification{Method: string(mcp.MethodNotificationToolsListChanged)}})
		close(release)
		for range 2 {
			require.NoError(t, <-results)
		}
		require.EqualValues(t, 2, observed.lists.Load())
		require.EqualValues(t, 2, observed.calls.Load())
	})
}

func TestListTools_SharesDiscoveryWithToolCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		observed := &observedToolClient{
			list: func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
				close(started)
				<-release
				page := toolListWithTTL(60000)
				page.Tools = []mcp.Tool{mcp.NewTool("read")}
				return page, nil
			},
			call: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("called"), nil
			},
		}
		connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
		called := make(chan error, 1)
		go func() {
			_, err := connection.callTool(t.Context(), "read", nil)
			called <- err
		}()
		<-started
		listed := make(chan []mcp.Tool, 1)
		listError := make(chan error, 1)
		go func() {
			tools, err := connection.listTools(t.Context())
			listed <- tools
			listError <- err
		}()
		synctest.Wait()
		close(release)
		require.NoError(t, <-called)
		require.NoError(t, <-listError)
		require.Equal(t, []mcp.Tool{mcp.NewTool("read")}, <-listed)
		require.EqualValues(t, 1, observed.lists.Load())
	})
}
