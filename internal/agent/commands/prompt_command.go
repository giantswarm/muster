package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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

	// Find the prompt to get its schema for better error messages
	prompt := p.findPrompt(promptName)

	// Parse arguments - support both JSON and key=value syntax
	var promptArgs map[string]string
	if len(parsed) > 1 {
		argsStr := p.joinArgsFrom(parsed, 1)

		// Check if arguments look like JSON (starts with {)
		trimmed := strings.TrimSpace(argsStr)
		if strings.HasPrefix(trimmed, "{") {
			var genericArgs map[string]interface{}
			if err := json.Unmarshal([]byte(trimmed), &genericArgs); err != nil {
				// Provide helpful error message with position info
				if syntaxErr, ok := err.(*json.SyntaxError); ok {
					p.output.Error("Invalid JSON at position %d: %v", syntaxErr.Offset, syntaxErr)
				} else {
					p.output.Error("Invalid JSON: %v", err)
				}
				p.output.OutputLine("Hint: Did you mean to use key=value syntax instead?")
				p.output.OutputLine("")
				p.showArgumentHelp(promptName, prompt)
				return nil
			}

			// Convert to string map
			promptArgs = make(map[string]string)
			for k, v := range genericArgs {
				promptArgs[k] = fmt.Sprintf("%v", v)
			}
		} else {
			// Parse key=value syntax
			promptArgs = p.parseKeyValueArgs(parsed[1:])
		}
	} else {
		// Check if this prompt requires arguments
		if prompt != nil && len(prompt.Arguments) > 0 {
			hasRequired := false
			for _, arg := range prompt.Arguments {
				if arg.Required {
					hasRequired = true
					break
				}
			}
			if hasRequired {
				p.showArgumentHelp(promptName, prompt)
				return nil
			}
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

// parseKeyValueArgs parses arguments in key=value format into a string map.
// Delegates to the shared parseKeyValueArgsToStringMap helper.
func (p *PromptCommand) parseKeyValueArgs(args []string) map[string]string {
	return parseKeyValueArgsToStringMap(args, p.output)
}

// showArgumentHelp displays helpful information about a prompt's arguments
func (p *PromptCommand) showArgumentHelp(promptName string, prompt *mcp.Prompt) {
	if prompt == nil {
		p.output.Error("Prompt not found: %s", promptName)
		p.output.OutputLine("Use 'list prompts' to see available prompts")
		return
	}

	p.output.OutputLine("Prompt: %s", promptName)
	if prompt.Description != "" {
		p.output.OutputLine("Description: %s", prompt.Description)
	}
	p.output.OutputLine("")

	if len(prompt.Arguments) == 0 {
		p.output.OutputLine("This prompt has no arguments.")
		p.output.OutputLine("")
		p.output.OutputLine("Usage: prompt %s", promptName)
		return
	}

	// Get sorted argument names
	var argNames []string
	for _, arg := range prompt.Arguments {
		argNames = append(argNames, arg.Name)
	}
	sort.Strings(argNames)

	p.output.OutputLine("Arguments:")
	for _, argName := range argNames {
		// Find the argument
		var arg *mcp.PromptArgument
		for i := range prompt.Arguments {
			if prompt.Arguments[i].Name == argName {
				arg = &prompt.Arguments[i]
				break
			}
		}
		if arg == nil {
			continue
		}

		// Use asterisk marker for required args (easier to scan visually)
		marker := " "
		if arg.Required {
			marker = "*"
		}

		p.output.OutputLine("  %s %s", marker, arg.Name)
		if arg.Description != "" {
			p.output.OutputLine("      %s", arg.Description)
		}
	}
	p.output.OutputLine("")
	p.output.OutputLine("  * = required argument")

	p.output.OutputLine("")
	p.output.OutputLine("Usage examples:")

	// Build example with key=value syntax
	var exampleParts []string
	for _, arg := range prompt.Arguments {
		if arg.Required {
			exampleParts = append(exampleParts, fmt.Sprintf("%s=<value>", arg.Name))
		}
	}
	if len(exampleParts) > 0 {
		p.output.OutputLine("  prompt %s %s", promptName, strings.Join(exampleParts, " "))
	} else {
		p.output.OutputLine("  prompt %s", promptName)
	}

	// Also show JSON syntax
	p.output.OutputLine("  prompt %s {\"arg\": \"value\"}", promptName)
}

// findPrompt looks up a prompt by name from the cache.
// Delegates to the shared findPromptByName helper.
func (p *PromptCommand) findPrompt(name string) *mcp.Prompt {
	prompts := p.client.GetPromptCache()
	return findPromptByName(prompts, name)
}

// getPromptArgNames returns all argument names for a prompt.
// Delegates to the shared getPromptArgNames helper.
func (p *PromptCommand) getPromptArgNames(prompt *mcp.Prompt) []string {
	return getPromptArgNames(prompt)
}

// Usage returns the usage string
func (p *PromptCommand) Usage() string {
	return "prompt <name> [params...] - supports key=value or JSON syntax"
}

// Description returns the command description
func (p *PromptCommand) Description() string {
	return "Get a prompt with key=value or JSON arguments"
}

// Completions returns possible completions
func (p *PromptCommand) Completions(input string) []string {
	parts := strings.Fields(input)

	if len(parts) <= 1 {
		// Complete prompt names
		return p.getPromptCompletions()
	}

	// Complete argument names for the specified prompt
	promptName := parts[1]
	prompt := p.findPrompt(promptName)
	if prompt == nil {
		return p.getPromptCompletions()
	}

	args := p.getPromptArgNames(prompt)

	// Format as arg= for easy completion
	var completions []string
	for _, arg := range args {
		completions = append(completions, arg+"=")
	}
	return completions
}

// Aliases returns command aliases
func (p *PromptCommand) Aliases() []string {
	return []string{"template"}
}
