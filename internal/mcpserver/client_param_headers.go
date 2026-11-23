package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

type toolListing struct {
	done         chan struct{}
	tools        []mcp.Tool
	generation   uint64
	invalidation uint64
	err          error
}

func (b *baseMCPClient) callToolWithParamHeaders(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !b.streamableHTTP || !mcp.IsModernProtocol(b.negotiatedProtocolVersion) {
		return b.client.CallTool(ctx, request)
	}
	_, generation, err := b.loadToolDefinitions(ctx, 0, false)
	if err != nil {
		return nil, err
	}
	result, err := b.client.CallTool(ctx, request)
	if !mcp.IsHeaderMismatch(err) {
		return result, err
	}
	if _, _, err := b.loadToolDefinitions(ctx, generation, false); err != nil {
		return nil, fmt.Errorf("refresh tool definitions after header mismatch: %w", err)
	}
	return b.client.CallTool(ctx, request)
}

// Caller holds mu for reading, keeping the SDK client stable during listing.
// A rejected generation may reuse a newer listing even when its TTL is zero.
// Only catalogs used to generate parameter headers need expiry refreshes.
func (b *baseMCPClient) loadToolDefinitions(ctx context.Context, refreshAfter uint64, force bool) ([]mcp.Tool, uint64, error) {
	for {
		b.toolsMu.Lock()
		if !force && b.toolsListedFor == b.client &&
			((refreshAfter == 0 && (!b.toolsHaveParamHeaders || time.Now().Before(b.toolsExpiresAt))) ||
				(refreshAfter != 0 && b.toolsGeneration > refreshAfter)) {
			generation := b.toolsGeneration
			b.toolsMu.Unlock()
			return nil, generation, nil
		}
		if pending := b.toolsListing; pending != nil {
			b.toolsMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-pending.done:
			}
			b.toolsMu.Lock()
			valid := pending.invalidation == b.toolsInvalidation
			b.toolsMu.Unlock()
			if pending.err == nil && valid {
				return pending.tools, pending.generation, nil
			}
			continue
		}
		pending := &toolListing{done: make(chan struct{}), invalidation: b.toolsInvalidation}
		b.toolsListing = pending
		b.toolsMu.Unlock()

		tools, expiresAt, err := b.fetchToolDefinitions(ctx)
		if err != nil {
			err = fmt.Errorf("list tool definitions: %w", err)
		}

		b.toolsMu.Lock()
		valid := pending.invalidation == b.toolsInvalidation
		if err == nil && valid {
			b.toolsListedFor = b.client
			b.toolsGeneration++
			b.toolsExpiresAt = expiresAt
			b.toolsHaveParamHeaders = slices.ContainsFunc(tools, func(tool mcp.Tool) bool {
				return len(mcp.ExtractParamHeaderBindings(&tool)) > 0
			})
		}
		pending.tools, pending.generation, pending.err = tools, b.toolsGeneration, err
		close(pending.done)
		b.toolsListing = nil
		b.toolsMu.Unlock()
		if err != nil {
			return nil, 0, err
		}
		if valid {
			return tools, pending.generation, nil
		}
	}
}

func (b *baseMCPClient) invalidateToolDefinitions() {
	b.toolsMu.Lock()
	defer b.toolsMu.Unlock()
	b.toolsListedFor = nil
	b.toolsInvalidation++
	b.toolsExpiresAt = time.Time{}
}

func (b *baseMCPClient) fetchToolDefinitions(ctx context.Context) ([]mcp.Tool, time.Time, error) {
	var tools []mcp.Tool
	var expiresAt time.Time
	request := mcp.ListToolsRequest{}
	seen := map[mcp.Cursor]struct{}{"": {}}
	for {
		if err := ctx.Err(); err != nil {
			return nil, time.Time{}, err
		}
		page, err := b.client.ListToolsByPage(ctx, request)
		if err != nil {
			return nil, time.Time{}, err
		}
		pageExpiresAt := time.Now()
		if ttlMillis, _ := page.TTL(); ttlMillis > 0 {
			const maxTTLMillis = int64((1<<63 - 1) / time.Millisecond)
			pageExpiresAt = pageExpiresAt.Add(time.Duration(min(ttlMillis, maxTTLMillis)) * time.Millisecond)
		}
		if expiresAt.IsZero() || pageExpiresAt.Before(expiresAt) {
			expiresAt = pageExpiresAt
		}
		tools = append(tools, page.Tools...)
		if page.NextCursor == "" {
			return tools, expiresAt, nil
		}
		if _, repeated := seen[page.NextCursor]; repeated {
			return nil, time.Time{}, fmt.Errorf("tools/list repeated a pagination cursor")
		}
		seen[page.NextCursor] = struct{}{}
		request.Params.Cursor = page.NextCursor
	}
}
