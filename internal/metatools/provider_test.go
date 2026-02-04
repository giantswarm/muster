package metatools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProvider(t *testing.T) {
	provider := NewProvider()
	require.NotNil(t, provider)
	assert.NotNil(t, provider.formatters)
}

func TestProvider_GetTools(t *testing.T) {
	provider := NewProvider()
	tools := provider.GetTools()

	// Verify we have all 11 meta-tools
	assert.Len(t, tools, 11, "Expected 11 meta-tools")

	// Create a map for easy lookup
	toolMap := make(map[string]bool)
	for _, tool := range tools {
		toolMap[tool.Name] = true
	}

	// Verify all expected tools are present
	expectedTools := []string{
		"list_tools",
		"describe_tool",
		"list_core_tools",
		"filter_tools",
		"call_tool",
		"list_resources",
		"describe_resource",
		"get_resource",
		"list_prompts",
		"describe_prompt",
		"get_prompt",
	}

	for _, name := range expectedTools {
		assert.True(t, toolMap[name], "Expected tool %s to be present", name)
	}
}

func TestProvider_GetTools_Metadata(t *testing.T) {
	provider := NewProvider()
	tools := provider.GetTools()

	// Find call_tool and verify its metadata
	var callTool *struct {
		Name        string
		Description string
		Args        []struct {
			Name     string
			Type     string
			Required bool
		}
	}

	for _, tool := range tools {
		if tool.Name == "call_tool" {
			callTool = &struct {
				Name        string
				Description string
				Args        []struct {
					Name     string
					Type     string
					Required bool
				}
			}{
				Name:        tool.Name,
				Description: tool.Description,
			}
			for _, arg := range tool.Args {
				callTool.Args = append(callTool.Args, struct {
					Name     string
					Type     string
					Required bool
				}{
					Name:     arg.Name,
					Type:     arg.Type,
					Required: arg.Required,
				})
			}
			break
		}
	}

	require.NotNil(t, callTool, "call_tool should be present")
	assert.Equal(t, "call_tool", callTool.Name)
	assert.Contains(t, callTool.Description, "Execute a tool")

	// Verify call_tool has required 'name' argument
	var hasNameArg bool
	for _, arg := range callTool.Args {
		if arg.Name == "name" {
			hasNameArg = true
			assert.True(t, arg.Required, "name argument should be required")
			assert.Equal(t, "string", arg.Type)
		}
	}
	assert.True(t, hasNameArg, "call_tool should have 'name' argument")
}

func TestProvider_GetFormatters(t *testing.T) {
	provider := NewProvider()
	formatters := provider.GetFormatters()
	require.NotNil(t, formatters)
}
