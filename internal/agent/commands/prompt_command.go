package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// PromptCommand gets prompts with arguments
type PromptCommand struct {
	*BaseCommand
}

// NewPromptCommand creates a new prompt command
func NewPromptCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *PromptCommand {
	return &PromptCommand{
		BaseCommand: NewBaseCommand(client, output, transport),
	}
}

// Execute gets a prompt with the given arguments
func (p *PromptCommand) Execute(ctx context.Context, args []string) error {
	parsed, err := p.parseArgs(args, 1, p.Usage())
	if err != nil {
		return err
	}

	promptName := parsed[0]

	// Parse arguments (default to empty if not provided)
	var promptArgs map[string]string
	if len(parsed) > 1 {
		argsStr := p.joinArgsFrom(parsed, 1)
		var genericArgs map[string]interface{}
		if err := json.Unmarshal([]byte(argsStr), &genericArgs); err != nil {
			p.output.Error("Arguments must be valid JSON")
			p.output.OutputLine("Example: %s %s {\"arg1\": \"value1\", \"arg2\": \"value2\"}", "prompt", promptName)
			return nil
		}

		// Convert to string map
		promptArgs = make(map[string]string)
		for k, v := range genericArgs {
			promptArgs[k] = fmt.Sprintf("%v", v)
		}
	} else {
		// Check if this prompt requires arguments
		prompts := p.client.GetPromptCache()
		prompt := p.getFormatters().FindPrompt(prompts, promptName)
		if prompt != nil && len(prompt.Arguments) > 0 {
			p.output.Error("This prompt requires arguments.")
			p.output.OutputLine("Required arguments:")
			for _, arg := range prompt.Arguments {
				p.output.OutputLine("  - %s: %s", arg.Name, arg.Description)
			}
			return nil
		}
		promptArgs = make(map[string]string)
	}

	p.output.Info("Getting prompt: %s...", promptName)

	// Get the prompt
	result, err := p.client.GetPrompt(ctx, promptName, promptArgs)
	if err != nil {
		p.output.Error("Failed to get prompt: %v", err)
		return nil
	}

	// Display messages
	p.output.OutputLine("Messages:")
	for i, msg := range result.Messages {
		p.output.OutputLine("\n[%d] Role: %s", i+1, msg.Role)
		if textContent, ok := msg.Content.(mcp.TextContent); ok {
			p.output.OutputLine("Content: %s", textContent.Text)
		} else if imageContent, ok := msg.Content.(mcp.ImageContent); ok {
			p.output.OutputLine("Content: [Image: MIME type %s, %d bytes]", imageContent.MIMEType, len(imageContent.Data))
		} else if audioContent, ok := msg.Content.(mcp.AudioContent); ok {
			p.output.OutputLine("Content: [Audio: MIME type %s, %d bytes]", audioContent.MIMEType, len(audioContent.Data))
		} else if resource, ok := msg.Content.(mcp.EmbeddedResource); ok {
			p.output.OutputLine("Content: [Embedded Resource: %v]", resource.Resource)
		} else {
			p.output.OutputLine("Content: %+v", msg.Content)
		}
	}

	return nil
}

// Usage returns the usage string
func (p *PromptCommand) Usage() string {
	return "prompt <prompt-name> [json-arguments]"
}

// Description returns the command description
func (p *PromptCommand) Description() string {
	return "Get a prompt with JSON arguments"
}

// Completions returns possible completions
func (p *PromptCommand) Completions(input string) []string {
	return p.getPromptCompletions()
}

// Aliases returns command aliases
func (p *PromptCommand) Aliases() []string {
	return []string{"template"}
}
