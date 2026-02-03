package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// CallCommand executes tools with arguments
type CallCommand struct {
	*BaseCommand
}

// NewCallCommand creates a new call command
func NewCallCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *CallCommand {
	return &CallCommand{
		BaseCommand: NewBaseCommand(client, output, transport),
	}
}

// Execute calls a tool with the given arguments
func (c *CallCommand) Execute(ctx context.Context, args []string) error {
	parsed, err := c.parseArgs(args, 1, c.Usage())
	if err != nil {
		return err
	}

	toolName := parsed[0]

	// Find the tool to get its schema for better error messages
	tool := c.findTool(toolName)

	// Parse arguments - support both JSON and key=value syntax
	var toolArgs map[string]interface{}
	if len(parsed) > 1 {
		argsStr := c.joinArgsFrom(parsed, 1)

		// Check if arguments look like JSON (starts with {)
		trimmed := strings.TrimSpace(argsStr)
		if strings.HasPrefix(trimmed, "{") {
			if err := json.Unmarshal([]byte(trimmed), &toolArgs); err != nil {
				// Provide helpful error message with position info
				if syntaxErr, ok := err.(*json.SyntaxError); ok {
					c.output.Error("Invalid JSON at position %d: %v", syntaxErr.Offset, syntaxErr)
				} else {
					c.output.Error("Invalid JSON: %v", err)
				}
				c.output.OutputLine("Hint: Did you mean to use key=value syntax instead?")
				c.output.OutputLine("")
				c.showArgumentHelp(toolName, tool)
				return nil
			}
		} else {
			// Parse key=value syntax
			toolArgs = c.parseKeyValueArgs(parsed[1:])
		}
	} else {
		toolArgs = make(map[string]interface{})
	}

	// If no arguments provided, show the tool schema to help the user
	if len(parsed) == 1 && tool != nil {
		requiredParams := c.getRequiredParams(tool)
		if len(requiredParams) > 0 {
			c.showArgumentHelp(toolName, tool)
			return nil
		}
	}

	// Show what we're doing
	c.output.Info("Executing tool: %s...", toolName)

	// Call the tool
	result, err := c.client.CallTool(ctx, toolName, toolArgs)
	if err != nil {
		c.output.Error("Tool execution failed: %v", err)
		return nil
	}

	// Handle error results
	if result.IsError {
		c.output.OutputLine("Tool returned an error:")
		for _, content := range result.Content {
			if textContent, ok := content.(mcp.TextContent); ok {
				c.output.OutputLine("  %s", textContent.Text)
			}
		}
		return nil
	}

	// Display results
	c.output.OutputLine("Result:")
	if len(result.Content) == 0 {
		c.output.OutputLine("  (no output returned)")
	}
	for _, content := range result.Content {
		switch v := content.(type) {
		case mcp.TextContent:
			// Try to format as JSON if possible
			var jsonObj interface{}
			if err := json.Unmarshal([]byte(v.Text), &jsonObj); err == nil {
				if b, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
					c.output.OutputLine(string(b))
				} else {
					c.output.OutputLine(v.Text)
				}
			} else {
				c.output.OutputLine(v.Text)
			}
		case mcp.ImageContent:
			c.output.OutputLine("[Image: MIME type %s, %d bytes]", v.MIMEType, len(v.Data))
		case mcp.AudioContent:
			c.output.OutputLine("[Audio: MIME type %s, %d bytes]", v.MIMEType, len(v.Data))
		default:
			c.output.OutputLine("%+v", content)
		}
	}

	return nil
}

// parseKeyValueArgs parses arguments in key=value format into a map.
// Delegates to the shared parseKeyValueArgsToInterfaceMap helper.
func (c *CallCommand) parseKeyValueArgs(args []string) map[string]interface{} {
	return parseKeyValueArgsToInterfaceMap(args, c.output)
}

// findTool looks up a tool by name from the cache.
// Delegates to the shared findToolByName helper.
func (c *CallCommand) findTool(name string) *mcp.Tool {
	tools := c.client.GetToolCache()
	return findToolByName(tools, name)
}

// getRequiredParams returns the list of required parameter names for a tool
func (c *CallCommand) getRequiredParams(tool *mcp.Tool) []string {
	if tool == nil {
		return nil
	}
	return tool.InputSchema.Required
}

// getToolParams returns all parameter names for a tool.
// Delegates to the shared getToolParamNames helper.
func (c *CallCommand) getToolParams(tool *mcp.Tool) []string {
	return getToolParamNames(tool)
}

// showArgumentHelp displays helpful information about a tool's parameters
func (c *CallCommand) showArgumentHelp(toolName string, tool *mcp.Tool) {
	if tool == nil {
		c.output.Error("Tool not found: %s", toolName)
		c.output.OutputLine("Use 'list tools' to see available tools")
		return
	}

	c.output.OutputLine("Tool: %s", toolName)
	if tool.Description != "" {
		c.output.OutputLine("Description: %s", tool.Description)
	}
	c.output.OutputLine("")

	if len(tool.InputSchema.Properties) == 0 {
		c.output.OutputLine("This tool has no parameters.")
		c.output.OutputLine("")
		c.output.OutputLine("Usage: call %s", toolName)
		return
	}

	// Create a set of required params for quick lookup
	requiredSet := make(map[string]bool)
	for _, req := range tool.InputSchema.Required {
		requiredSet[req] = true
	}

	// Get sorted parameter names
	params := c.getToolParams(tool)

	c.output.OutputLine("Parameters:")
	for _, paramName := range params {
		propData := tool.InputSchema.Properties[paramName]
		propMap, ok := propData.(map[string]interface{})
		if !ok {
			continue
		}

		// Get type and description using shared helper
		paramType := getStringFromMap(propMap, "type", "string")
		description := getStringFromMap(propMap, "description", "")

		// Use asterisk marker for required params (easier to scan visually)
		marker := " "
		if requiredSet[paramName] {
			marker = "*"
		}

		c.output.OutputLine("  %s %s (%s)", marker, paramName, paramType)
		if description != "" {
			c.output.OutputLine("      %s", description)
		}
	}
	c.output.OutputLine("")
	c.output.OutputLine("  * = required parameter")

	c.output.OutputLine("")
	c.output.OutputLine("Usage examples:")

	// Build example with key=value syntax
	var exampleParts []string
	for _, paramName := range params {
		if requiredSet[paramName] {
			exampleParts = append(exampleParts, fmt.Sprintf("%s=<value>", paramName))
		}
	}
	if len(exampleParts) > 0 {
		c.output.OutputLine("  call %s %s", toolName, strings.Join(exampleParts, " "))
	} else {
		c.output.OutputLine("  call %s", toolName)
	}

	// Also show JSON syntax
	c.output.OutputLine("  call %s {\"param\": \"value\"}", toolName)
}

// Usage returns the usage string
func (c *CallCommand) Usage() string {
	return "call <tool> [params...] - supports key=value or JSON syntax"
}

// Description returns the command description
func (c *CallCommand) Description() string {
	return "Execute a tool with key=value or JSON arguments"
}

// Completions returns possible completions
func (c *CallCommand) Completions(input string) []string {
	parts := strings.Fields(input)

	if len(parts) <= 1 {
		// Complete tool names
		return c.getToolCompletions()
	}

	// Complete parameter names for the specified tool
	toolName := parts[1]
	tool := c.findTool(toolName)
	if tool == nil {
		return c.getToolCompletions()
	}

	params := c.getToolParams(tool)

	// Format as param= for easy completion
	var completions []string
	for _, param := range params {
		completions = append(completions, param+"=")
	}
	return completions
}

// Aliases returns command aliases
func (c *CallCommand) Aliases() []string {
	return []string{"run", "exec"}
}
