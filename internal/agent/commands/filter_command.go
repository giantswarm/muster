package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/giantswarm/muster/internal/metatools"

	"github.com/mark3labs/mcp-go/mcp"
)

// FilterCommand filters tools by pattern and description
type FilterCommand struct {
	*BaseCommand
}

// NewFilterCommand creates a new filter command
func NewFilterCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *FilterCommand {
	return &FilterCommand{
		BaseCommand: NewBaseCommand(client, output, transport),
	}
}

// Execute filters tools based on patterns
func (f *FilterCommand) Execute(ctx context.Context, args []string) error {
	parsed, err := f.parseArgs(args, 1, f.Usage())
	if err != nil {
		return err
	}

	target := strings.ToLower(parsed[0])
	if target != "tools" {
		return f.validateTarget(target, []string{"tools"})
	}

	// Get pattern and description filter from args
	var pattern, descriptionFilter string
	var caseSensitive, detailed bool

	if len(parsed) > 1 {
		pattern = parsed[1]
	}
	if len(parsed) > 2 {
		descriptionFilter = parsed[2]
	}
	if len(parsed) > 3 {
		caseSensitive = strings.ToLower(parsed[3]) == "true"
	}
	if len(parsed) > 4 {
		detailed = strings.ToLower(parsed[4]) == "true"
	}

	return f.filterTools(ctx, pattern, descriptionFilter, caseSensitive, detailed)
}

// filterTools filters tools by calling the filter_tools meta-tool.
// This returns actual tools (core_*, x_*, workflow_*) rather than meta-tools.
func (f *FilterCommand) filterTools(ctx context.Context, pattern, descriptionFilter string, caseSensitive bool, detailed bool) error {
	// Build args for the filter_tools meta-tool
	toolArgs := map[string]interface{}{
		"case_sensitive": caseSensitive,
		"include_schema": detailed,
	}
	if pattern != "" {
		toolArgs["pattern"] = pattern
	}
	if descriptionFilter != "" {
		toolArgs["description_filter"] = descriptionFilter
	}

	// Call the filter_tools meta-tool
	result, err := f.client.CallTool(ctx, metatools.ToolFilterTools, toolArgs)
	if err != nil {
		return fmt.Errorf("failed to filter tools: %w", err)
	}

	if result.IsError {
		f.output.Error("Error filtering tools:")
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				f.output.OutputLine("  %s", textContent.Text)
			}
		}
		return nil
	}

	// Parse the JSON response from filter_tools
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			var response metatools.FilterToolsResponse

			if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
				// Not JSON, just output the raw text
				f.output.OutputLine(textContent.Text)
				return nil
			}

			// Show filter details
			if pattern != "" || descriptionFilter != "" {
				f.output.Info("Filtering tools with:")
				if pattern != "" {
					f.output.Info("  Pattern: %s", pattern)
				}
				if descriptionFilter != "" {
					f.output.Info("  Description filter: %s", descriptionFilter)
				}
				f.output.Info("  Case sensitive: %t", caseSensitive)
				f.output.Info("Results: %d of %d tools match", response.FilteredCount, response.TotalTools)
			}

			if len(response.Tools) == 0 {
				f.output.OutputLine("No tools match the specified filters.")
				return nil
			}

			// Display matching tools - brief mode by default for better CLI UX
			if detailed {
				// Detailed mode - show full specifications (optional)
				f.output.OutputLine("\nFiltered Tools with Full Specifications:")
				f.output.OutputLine(strings.Repeat("=", 60))

				for i, tool := range response.Tools {
					f.output.OutputLine("\n%d. %s", i+1, tool.Name)
					f.output.OutputLine("   Description: %s", tool.Description)
					if tool.InputSchema != nil {
						if schemaJSON, err := json.MarshalIndent(tool.InputSchema, "  ", "  "); err == nil {
							f.output.OutputLine("   Schema: %s", string(schemaJSON))
						}
					}
					if i < len(response.Tools)-1 {
						f.output.OutputLine(strings.Repeat("-", 40))
					}
				}
			} else {
				// Brief mode - show simple list (default for good CLI UX)
				f.output.OutputLine("\nMatching tools:")
				for i, tool := range response.Tools {
					f.output.OutputLine("  %d. %-30s - %s", i+1, tool.Name, tool.Description)
				}
			}

			return nil
		}
	}

	f.output.OutputLine("No tools available to filter")
	return nil
}

// Usage returns the usage string
func (f *FilterCommand) Usage() string {
	return "filter tools [pattern] [description-filter] [case-sensitive] [detailed]"
}

// Description returns the command description
func (f *FilterCommand) Description() string {
	return "Filter tools by name pattern or description"
}

// Completions returns possible completions
func (f *FilterCommand) Completions(input string) []string {
	parts := strings.Fields(input)

	if len(parts) == 1 {
		return []string{"tools"}
	}

	return []string{}
}

// Aliases returns command aliases
func (f *FilterCommand) Aliases() []string {
	return []string{"find", "search"}
}
