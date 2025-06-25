package commands

import (
	"context"
	"strings"

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
	var caseSensitive bool

	if len(parsed) > 1 {
		pattern = parsed[1]
	}
	if len(parsed) > 2 {
		descriptionFilter = parsed[2]
	}
	if len(parsed) > 3 {
		caseSensitive = strings.ToLower(parsed[3]) == "true"
	}

	return f.filterTools(pattern, descriptionFilter, caseSensitive)
}

// filterTools filters tools by pattern and description
func (f *FilterCommand) filterTools(pattern, descriptionFilter string, caseSensitive bool) error {
	tools := f.client.GetToolCache()
	if len(tools) == 0 {
		f.output.OutputLine("No tools available to filter")
		return nil
	}

	var filteredTools []mcp.Tool

	for _, tool := range tools {
		nameMatch := pattern == "" || f.matchesPattern(tool.Name, pattern, caseSensitive)
		descMatch := descriptionFilter == "" || f.containsDescription(tool.Description, descriptionFilter, caseSensitive)

		if nameMatch && descMatch {
			filteredTools = append(filteredTools, tool)
		}
	}

	// Show filter details if in verbose mode
	if pattern != "" || descriptionFilter != "" {
		f.output.Info("Filtering tools with:")
		if pattern != "" {
			f.output.Info("  Pattern: %s", pattern)
		}
		if descriptionFilter != "" {
			f.output.Info("  Description filter: %s", descriptionFilter)
		}
		f.output.Info("  Case sensitive: %t", caseSensitive)
		f.output.Info("Results: %d of %d tools match", len(filteredTools), len(tools))
	}

	if len(filteredTools) == 0 {
		f.output.OutputLine("No tools match the specified filters.")
		return nil
	}

	// Display matching tools
	f.output.OutputLine("\nMatching tools:")
	for i, tool := range filteredTools {
		f.output.OutputLine("  %d. %-30s - %s", i+1, tool.Name, tool.Description)
	}

	return nil
}

// matchesPattern checks if a name matches a pattern (supports wildcards)
func (f *FilterCommand) matchesPattern(name, pattern string, caseSensitive bool) bool {
	if !caseSensitive {
		name = strings.ToLower(name)
		pattern = strings.ToLower(pattern)
	}

	// Simple wildcard matching
	if strings.Contains(pattern, "*") {
		// Replace * with regex equivalent and check
		parts := strings.Split(pattern, "*")
		current := name
		for _, part := range parts {
			if part == "" {
				continue
			}
			index := strings.Index(current, part)
			if index == -1 {
				return false
			}
			current = current[index+len(part):]
		}
		return true
	}

	// Exact or substring match
	return strings.Contains(name, pattern)
}

// containsDescription checks if description contains the filter text
func (f *FilterCommand) containsDescription(description, filter string, caseSensitive bool) bool {
	if !caseSensitive {
		description = strings.ToLower(description)
		filter = strings.ToLower(filter)
	}
	return strings.Contains(description, filter)
}

// Usage returns the usage string
func (f *FilterCommand) Usage() string {
	return "filter tools [pattern] [description-filter] [case-sensitive]"
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
