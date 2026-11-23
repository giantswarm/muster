package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

func (b *baseMCPClient) callToolWithParamHeaders(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !b.streamableHTTP || !mcp.IsModernProtocol(b.negotiatedProtocolVersion) {
		return b.client.CallTool(ctx, request)
	}
	if err := b.ensureToolsListed(ctx); err != nil {
		return nil, err
	}
	result, err := b.client.CallTool(ctx, request)
	if !mcp.IsHeaderMismatch(err) {
		return result, err
	}
	if _, err := b.client.ListTools(ctx, mcp.ListToolsRequest{}); err != nil {
		return nil, fmt.Errorf("refresh tool definitions after header mismatch: %w", err)
	}
	return b.client.CallTool(ctx, request)
}

// The read lock on mu keeps the client stable during discovery.
func (b *baseMCPClient) ensureToolsListed(ctx context.Context) error {
	for {
		b.toolsMu.Lock()
		if b.toolsListedFor == b.client {
			b.toolsMu.Unlock()
			return nil
		}
		if pending := b.toolsListing; pending != nil {
			b.toolsMu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-pending:
				continue
			}
		}
		b.toolsListing = make(chan struct{})
		b.toolsMu.Unlock()

		_, err := b.client.ListTools(ctx, mcp.ListToolsRequest{})

		b.toolsMu.Lock()
		if err == nil {
			b.toolsListedFor = b.client
		}
		close(b.toolsListing)
		b.toolsListing = nil
		b.toolsMu.Unlock()
		if err != nil {
			return fmt.Errorf("list tools before calling: %w", err)
		}
		return nil
	}
}
