package commands

import (
	"context"
	"strings"
)

// DescribeCommand shows detailed information about tools, resources, or prompts
type DescribeCommand struct {
	*BaseCommand
}

// NewDescribeCommand creates a new describe command
func NewDescribeCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *DescribeCommand {
	return &DescribeCommand{
		BaseCommand: NewBaseCommand(client, output, transport),
	}
}

// Execute describes a tool, resource, or prompt
func (d *DescribeCommand) Execute(ctx context.Context, args []string) error {
	parsed, err := d.parseArgs(args, 2, d.Usage())
	if err != nil {
		return err
	}

	itemType := strings.ToLower(parsed[0])
	itemName := parsed[1]

	switch itemType {
	case "tool":
		return d.describeTool(itemName)
	case "resource":
		return d.describeResource(itemName)
	case "prompt":
		return d.describePrompt(itemName)
	default:
		return d.validateTarget(itemType, []string{"tool", "resource", "prompt"})
	}
}

// describeTool shows detailed information about a tool
func (d *DescribeCommand) describeTool(name string) error {
	tools := d.client.GetToolCache()
	tool := d.getFormatters().FindTool(tools, name)
	if tool == nil {
		d.output.Error("Tool not found: %s", name)
		return nil
	}

	d.output.OutputLine(d.getFormatters().FormatToolDetail(*tool))
	return nil
}

// describeResource shows detailed information about a resource
func (d *DescribeCommand) describeResource(uri string) error {
	resources := d.client.GetResourceCache()
	resource := d.getFormatters().FindResource(resources, uri)
	if resource == nil {
		d.output.Error("Resource not found: %s", uri)
		return nil
	}

	d.output.OutputLine(d.getFormatters().FormatResourceDetail(*resource))
	return nil
}

// describePrompt shows detailed information about a prompt
func (d *DescribeCommand) describePrompt(name string) error {
	prompts := d.client.GetPromptCache()
	prompt := d.getFormatters().FindPrompt(prompts, name)
	if prompt == nil {
		d.output.Error("Prompt not found: %s", name)
		return nil
	}

	d.output.OutputLine(d.getFormatters().FormatPromptDetail(*prompt))
	return nil
}

// Usage returns the usage string
func (d *DescribeCommand) Usage() string {
	return "describe <tool|resource|prompt> <name|uri>"
}

// Description returns the command description
func (d *DescribeCommand) Description() string {
	return "Show detailed information about a tool, resource, or prompt"
}

// Completions returns possible completions
func (d *DescribeCommand) Completions(input string) []string {
	parts := strings.Fields(input)

	if len(parts) == 1 {
		// Complete the type
		return d.getCompletionsForTargets([]string{"tool", "resource", "prompt"})
	} else if len(parts) == 2 {
		// Complete the name based on type
		switch strings.ToLower(parts[1]) {
		case "tool":
			return d.getToolCompletions()
		case "resource":
			return d.getResourceCompletions()
		case "prompt":
			return d.getPromptCompletions()
		}
	}

	return []string{}
}

// Aliases returns command aliases
func (d *DescribeCommand) Aliases() []string {
	return []string{"desc", "info"}
}
