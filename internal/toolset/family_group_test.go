package toolset

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

func TestInputSchemaFingerprint(t *testing.T) {
	t.Run("a property called description is an argument, not an annotation", func(t *testing.T) {
		withProps := func(props map[string]any) mcp.Tool {
			return mcp.Tool{Name: "create", InputSchema: mcp.ToolInputSchema{Type: "object", Properties: props}}
		}
		a := withProps(map[string]any{"name": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"}})
		b := withProps(map[string]any{"name": map[string]any{"type": "string"}})
		assert.NotEqual(t, InputSchemaFingerprint(a), InputSchemaFingerprint(b))

		c := withProps(map[string]any{"name": map[string]any{"type": "string", "enum": []any{"a"}}})
		d := withProps(map[string]any{"name": map[string]any{"type": "string", "enum": []any{"a", "b"}}})
		assert.NotEqual(t, InputSchemaFingerprint(c), InputSchemaFingerprint(d))
	})

	t.Run("annotations at any depth do not count", func(t *testing.T) {
		a := mcp.Tool{RawInputSchema: []byte(`{"type":"object","title":"A","properties":{"filter":{"type":"object","description":"old","additionalProperties":{"anyOf":[{"type":"string","examples":["x"]}]}}}}`)}
		b := mcp.Tool{InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]any{
			"filter": map[string]any{"additionalProperties": map[string]any{"anyOf": []any{map[string]any{"type": "string"}}}, "type": "object", "description": "new"},
		}}}
		assert.Equal(t, InputSchemaFingerprint(a), InputSchemaFingerprint(b))
	})

	t.Run("required compares as a set", func(t *testing.T) {
		a := mcp.Tool{RawInputSchema: []byte(`{"type":"object","required":["b","a"]}`)}
		b := mcp.Tool{RawInputSchema: []byte(`{"type":"object","required":["a","b"]}`)}
		c := mcp.Tool{RawInputSchema: []byte(`{"type":"object","required":["a"]}`)}
		assert.Equal(t, InputSchemaFingerprint(a), InputSchemaFingerprint(b))
		assert.NotEqual(t, InputSchemaFingerprint(a), InputSchemaFingerprint(c))
	})
}

func TestCommonDescription(t *testing.T) {
	tools := func(ds ...string) []mcp.Tool {
		out := make([]mcp.Tool, len(ds))
		for i, d := range ds {
			out[i] = mcp.Tool{Description: d}
		}
		return out
	}
	assert.Equal(t, "short", CommonDescription(tools("short", "longer one", "short")))
	assert.Equal(t, "longer one", CommonDescription(tools("short", "longer one")))
	assert.Equal(t, "aaaa", CommonDescription(tools("bbbb", "aaaa")))
}
