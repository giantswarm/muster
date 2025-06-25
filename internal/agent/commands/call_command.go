package commands

import (
	"context"
	"encoding/json"

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

	// Parse arguments (default to empty JSON object if not provided)
	var toolArgs map[string]interface{}
	if len(parsed) > 1 {
		argsStr := c.joinArgsFrom(parsed, 1)
		if err := json.Unmarshal([]byte(argsStr), &toolArgs); err != nil {
			c.output.Error("Arguments must be valid JSON")
			c.output.OutputLine("Example: %s %s {\"param1\": \"value1\", \"param2\": 123}", "call", toolName)
			return nil
		}
	} else {
		toolArgs = make(map[string]interface{})
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

// Usage returns the usage string
func (c *CallCommand) Usage() string {
	return "call <tool-name> [json-arguments]"
}

// Description returns the command description
func (c *CallCommand) Description() string {
	return "Execute a tool with JSON arguments"
}

// Completions returns possible completions
func (c *CallCommand) Completions(input string) []string {
	return c.getToolCompletions()
}

// Aliases returns command aliases
func (c *CallCommand) Aliases() []string {
	return []string{"run", "exec"}
}
