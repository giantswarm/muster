package commands

import (
	"context"
	"fmt"
	"strings"

	musterctx "muster/internal/context"
)

// ContextCommand handles context listing and switching in the REPL.
// This command allows users to view available contexts, see the current
// context, and switch between contexts without leaving the REPL.
type ContextCommand struct {
	*BaseCommand
	onContextChange func(contextName string) // Callback when context changes
}

// NewContextCommand creates a new context command.
//
// Args:
//   - client: MCP client interface for operations
//   - output: Logger interface for user-facing output
//   - transport: Transport interface for capability checking
//   - onContextChange: Callback function called when context is switched
//
// Returns:
//   - Configured context command instance
func NewContextCommand(client ClientInterface, output OutputLogger, transport TransportInterface, onContextChange func(string)) *ContextCommand {
	return &ContextCommand{
		BaseCommand:     NewBaseCommand(client, output, transport),
		onContextChange: onContextChange,
	}
}

// Execute runs the context command with the given arguments.
// Subcommands:
//   - (no args): Show current context
//   - list/ls: List all available contexts
//   - use/switch <name>: Switch to a different context
func (c *ContextCommand) Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return c.showCurrent()
	}

	subCmd := strings.ToLower(args[0])
	switch subCmd {
	case "list", "ls":
		return c.listContexts()
	case "use", "switch":
		if len(args) < 2 {
			return fmt.Errorf("usage: context use <name>")
		}
		return c.switchContext(args[1])
	default:
		// Treat single argument as context name to switch to
		return c.switchContext(args[0])
	}
}

// showCurrent displays the current context name.
func (c *ContextCommand) showCurrent() error {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return fmt.Errorf("failed to access context storage: %w", err)
	}

	name, err := storage.GetCurrentContextName()
	if err != nil {
		return fmt.Errorf("failed to get current context: %w", err)
	}

	if name == "" {
		c.output.OutputLine("No context set")
		c.output.OutputLine("")
		c.output.OutputLine("Use 'context list' to see available contexts")
		c.output.OutputLine("Use 'context use <name>' to switch context")
	} else {
		c.output.OutputLine("Current context: %s", name)
	}

	return nil
}

// listContexts displays all available contexts.
func (c *ContextCommand) listContexts() error {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return fmt.Errorf("failed to access context storage: %w", err)
	}

	config, err := storage.Load()
	if err != nil {
		return fmt.Errorf("failed to load contexts: %w", err)
	}

	if len(config.Contexts) == 0 {
		c.output.OutputLine("No contexts configured")
		c.output.OutputLine("")
		c.output.OutputLine("Add contexts with:")
		c.output.OutputLine("  muster context add <name> --endpoint <url>")
		return nil
	}

	c.output.OutputLine("Available contexts:")
	for _, ctx := range config.Contexts {
		marker := "  "
		if ctx.Name == config.CurrentContext {
			marker = "* "
		}
		c.output.OutputLine("%s%s", marker, ctx.Name)
	}

	return nil
}

// switchContext switches to a different context.
func (c *ContextCommand) switchContext(name string) error {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return fmt.Errorf("failed to access context storage: %w", err)
	}

	// Check if context exists
	ctxConfig, err := storage.GetContext(name)
	if err != nil {
		return fmt.Errorf("failed to get context: %w", err)
	}
	if ctxConfig == nil {
		// List available contexts for helpful error
		names, _ := storage.GetContextNames()
		if len(names) > 0 {
			return fmt.Errorf("context %q not found. Available contexts: %s", name, strings.Join(names, ", "))
		}
		return fmt.Errorf("context %q not found. No contexts configured", name)
	}

	// Set as current context
	if err := storage.SetCurrentContext(name); err != nil {
		return fmt.Errorf("failed to switch context: %w", err)
	}

	c.output.Success("Switched to context %q", name)
	c.output.OutputLine("Endpoint: %s", ctxConfig.Endpoint)
	c.output.OutputLine("")
	c.output.OutputLine("Note: Exit and restart the REPL to connect to the new endpoint")

	// Notify callback of context change
	if c.onContextChange != nil {
		c.onContextChange(name)
	}

	return nil
}

// Usage returns the usage string for the context command.
func (c *ContextCommand) Usage() string {
	return "context [list|use <name>]"
}

// Description returns a brief description of what the command does.
func (c *ContextCommand) Description() string {
	return "List and switch between muster contexts"
}

// Completions returns possible completions for the context command.
func (c *ContextCommand) Completions(input string) []string {
	parts := strings.Fields(input)

	// First argument: subcommands
	if len(parts) <= 1 {
		return []string{"list", "ls", "use", "switch"}
	}

	// If subcommand is "use" or "switch", complete with context names
	subCmd := strings.ToLower(parts[0])
	if subCmd == "use" || subCmd == "switch" {
		return c.getContextCompletions()
	}

	return nil
}

// getContextCompletions returns available context names for completion.
func (c *ContextCommand) getContextCompletions() []string {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return nil
	}

	names, err := storage.GetContextNames()
	if err != nil {
		return nil
	}

	return names
}

// Aliases returns alternative names for the context command.
func (c *ContextCommand) Aliases() []string {
	return []string{"ctx"}
}
