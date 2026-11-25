package mcpserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/client/filesystem"
)

func call(t *testing.T, adapter *Adapter, tool string, args map[string]interface{}) (string, bool) {
	t.Helper()
	result, err := adapter.ExecuteTool(context.Background(), tool, args)
	require.NoError(t, err)
	return resultText(t, result), result.IsError
}

// core_mcpserver_create needs approval settings, a preset or explicit ones,
// and fails without either, naming the presets.
func TestCreateRequiresApprovalSettings(t *testing.T) {
	c := &recordingClient{}
	adapter := NewAdapterWithClient(c, "test-ns")

	text, isErr := call(t, adapter, "mcpserver_create", remoteServerArgs(nil))
	require.True(t, isErr)
	assert.Contains(t, text, "recommended, allow-all, ask-all")
	assert.Empty(t, c.createdObjs)

	text, isErr = call(t, adapter, "mcpserver_create", remoteServerArgs(map[string]interface{}{"preset": "recommended", "approval": map[string]interface{}{"rule": "Ask"}}))
	require.True(t, isErr)
	assert.Contains(t, text, "not both")

	text, isErr = call(t, adapter, "mcpserver_create", remoteServerArgs(map[string]interface{}{"preset": "lenient"}))
	require.True(t, isErr)
	assert.Contains(t, text, "unknown approval preset")

	_, isErr = call(t, adapter, "mcpserver_create", remoteServerArgs(map[string]interface{}{"preset": "recommended"}))
	require.False(t, isErr)
	require.Len(t, c.createdObjs, 1)
	require.NotNil(t, c.createdObjs[0].Spec.Approval)
	assert.Equal(t, api.ApprovalAsk, c.createdObjs[0].Spec.Approval.Rule, "a server not listed yet gets the preset's rule")

	_, isErr = call(t, adapter, "mcpserver_create", remoteServerArgs(map[string]interface{}{"name": "pd", "approval": map[string]interface{}{
		"rule": "Allow", "tools": map[string]interface{}{"delete": "Block"},
	}}))
	require.False(t, isErr)
	require.Len(t, c.createdObjs, 2)
	assert.Equal(t, map[string]string{"delete": "Block"}, c.createdObjs[1].Spec.Approval.Tools)

	text, isErr = call(t, adapter, "mcpserver_create", remoteServerArgs(map[string]interface{}{"name": "bad", "approval": map[string]interface{}{"rule": "Block"}}))
	require.True(t, isErr)
	assert.Contains(t, text, "rule must be Allow or Ask")
}

// Where a server is and how muster reaches it changes only through
// core_mcpserver_connect; core_mcpserver_update takes the rest.
func TestUpdateAndConnectAreSplit(t *testing.T) {
	adapter := NewAdapterWithClient(filesystem.New(t.TempDir()), "default")
	_, isErr := call(t, adapter, "mcpserver_create", remoteServerArgs(map[string]interface{}{"preset": "allow-all"}))
	require.False(t, isErr)

	text, isErr := call(t, adapter, "mcpserver_update", map[string]interface{}{"name": "kagi-search", "url": "https://elsewhere.example.com/mcp"})
	require.True(t, isErr)
	assert.Contains(t, text, "does not take url: use core_mcpserver_connect")

	text, isErr = call(t, adapter, "mcpserver_update", map[string]interface{}{"name": "kagi-search", "approval": map[string]interface{}{}})
	require.True(t, isErr)
	assert.Contains(t, text, "core_mcpserver_set_approval")

	text, isErr = call(t, adapter, "mcpserver_connect", map[string]interface{}{"name": "kagi-search", "suspended": true})
	require.True(t, isErr)
	assert.Contains(t, text, "does not take suspended: use core_mcpserver_update")

	_, isErr = call(t, adapter, "mcpserver_connect", map[string]interface{}{"name": "kagi-search", "url": "https://elsewhere.example.com/mcp"})
	require.False(t, isErr)
	_, isErr = call(t, adapter, "mcpserver_update", map[string]interface{}{"name": "kagi-search", "description": "Search"})
	require.False(t, isErr)

	info, err := adapter.GetMCPServer(context.Background(), "kagi-search")
	require.NoError(t, err)
	assert.Equal(t, "https://elsewhere.example.com/mcp", info.URL)
	assert.Equal(t, "Search", info.Description)
}

func TestSetApproval(t *testing.T) {
	adapter := NewAdapterWithClient(filesystem.New(t.TempDir()), "default")
	_, isErr := call(t, adapter, "mcpserver_create", remoteServerArgs(map[string]interface{}{"preset": "allow-all"}))
	require.False(t, isErr)

	text, isErr := call(t, adapter, "mcpserver_set_approval", map[string]interface{}{"name": "kagi-search"})
	require.True(t, isErr)
	assert.Contains(t, text, "pass preset")

	text, isErr = call(t, adapter, "mcpserver_set_approval", map[string]interface{}{"name": "kagi-search", "url": "x", "preset": "ask-all"})
	require.True(t, isErr)
	assert.Contains(t, text, "does not take url")

	_, isErr = call(t, adapter, "mcpserver_set_approval", map[string]interface{}{"name": "kagi-search", "approval": map[string]interface{}{
		"rule": "Ask", "tools": map[string]interface{}{"search": "Allow", "purge": "Block"},
	}})
	require.False(t, isErr)
	info, err := adapter.GetMCPServer(context.Background(), "kagi-search")
	require.NoError(t, err)
	assert.Equal(t, &api.MCPServerApproval{Rule: "Ask", Tools: map[string]string{"search": "Allow", "purge": "Block"}}, info.Approval)

	// A preset expands from the tools listed now: none without an
	// aggregator, so the rule alone, written out explicitly.
	_, isErr = call(t, adapter, "mcpserver_set_approval", map[string]interface{}{"name": "kagi-search", "preset": "allow-all"})
	require.False(t, isErr)
	info, err = adapter.GetMCPServer(context.Background(), "kagi-search")
	require.NoError(t, err)
	assert.Equal(t, &api.MCPServerApproval{Rule: "Allow"}, info.Approval)

	text, isErr = call(t, adapter, "mcpserver_set_approval", map[string]interface{}{"name": "missing", "preset": "allow-all"})
	require.True(t, isErr)
	assert.Contains(t, text, "not found")
}
