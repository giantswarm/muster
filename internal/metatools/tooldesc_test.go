package metatools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetaToolAnnotationsAreComplete: every meta-tool reports every hint, so
// no client falls back to the spec's defaults. All but call_tool only read;
// call_tool runs whatever it is given. All are open world (readOnlyMetaTool).
func TestMetaToolAnnotationsAreComplete(t *testing.T) {
	for _, tool := range NewProvider().GetTools() {
		a := tool.Annotations
		require.NotNil(t, a, "%s declares no annotations", tool.Name)
		require.NotNil(t, a.ReadOnlyHint, "%s: readOnlyHint unset", tool.Name)
		require.NotNil(t, a.DestructiveHint, "%s: destructiveHint unset", tool.Name)
		require.NotNil(t, a.IdempotentHint, "%s: idempotentHint unset", tool.Name)
		require.NotNil(t, a.OpenWorldHint, "%s: openWorldHint unset", tool.Name)
		assert.True(t, *a.OpenWorldHint, "%s reads the catalogue, which can reach the servers", tool.Name)
		if tool.Name == ToolCallTool {
			assert.False(t, *a.ReadOnlyHint, "call_tool runs any tool, writes included")
			assert.True(t, *a.DestructiveHint, "call_tool runs any tool, destructive ones included")
			assert.False(t, *a.IdempotentHint, "call_tool runs any tool, non-idempotent ones included")
			continue
		}
		assert.True(t, *a.ReadOnlyHint, "%s only reads", tool.Name)
		assert.False(t, *a.DestructiveHint, tool.Name)
		assert.True(t, *a.IdempotentHint, tool.Name)
	}
}
