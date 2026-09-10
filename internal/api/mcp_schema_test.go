package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInputSchemaFromArgs(t *testing.T) {
	schema := InputSchemaFromArgs([]ArgMetadata{
		{Name: "name", Type: ArgTypeString, Required: true, Description: "Name of the tool"},
		{Name: "limit", Type: ArgTypeNumber, Description: "Page size", Default: 50},
		{
			Name:        "toolset",
			Type:        ArgTypeArray,
			Description: "Selectors",
			Schema:      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "overridden"},
		},
	})

	assert.Equal(t, "object", schema.Type)
	assert.Equal(t, []string{"name"}, schema.Required)
	require.Len(t, schema.Properties, 3)

	assert.Equal(t, map[string]any{"type": ArgTypeString, "description": "Name of the tool"}, schema.Properties["name"])
	assert.Equal(t, map[string]any{"type": ArgTypeNumber, "description": "Page size", "default": 50}, schema.Properties["limit"])
	assert.Equal(t, map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"description": "Selectors",
	}, schema.Properties["toolset"], "a detailed schema is kept, with the argument's description winning")
}

func TestInputSchemaFromArgs_NoArgs(t *testing.T) {
	schema := InputSchemaFromArgs(nil)
	assert.Equal(t, "object", schema.Type)
	assert.Empty(t, schema.Properties)
	assert.Empty(t, schema.Required)
}
