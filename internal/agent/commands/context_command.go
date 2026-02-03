package commands

import (
	"context"
	"fmt"
	"strings"

	musterctx "muster/internal/context"
)

// StorageProvider abstracts context storage operations for testability.
// This interface allows injecting mock storage in tests.
type StorageProvider interface {
	GetCurrentContextName() (string, error)
	Load() (*musterctx.ContextConfig, error)
	GetContext(name string) (*musterctx.Context, error)
	GetContextNames() ([]string, error)
	SetCurrentContext(name string) error
}

// defaultStorageProvider wraps musterctx.Storage to implement StorageProvider.
type defaultStorageProvider struct{}

func (d *defaultStorageProvider) GetCurrentContextName() (string, error) {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return "", err
	}
	return storage.GetCurrentContextName()
}

func (d *defaultStorageProvider) Load() (*musterctx.ContextConfig, error) {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return nil, err
	}
	return storage.Load()
}

func (d *defaultStorageProvider) GetContext(name string) (*musterctx.Context, error) {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return nil, err
	}
	return storage.GetContext(name)
}

func (d *defaultStorageProvider) GetContextNames() ([]string, error) {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return nil, err
	}
	return storage.GetContextNames()
}

func (d *defaultStorageProvider) SetCurrentContext(name string) error {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return err
	}
	return storage.SetCurrentContext(name)
}

// ContextCommand handles context listing and switching in the REPL.
// This command allows users to view available contexts, see the current
// context, and switch between contexts without leaving the REPL.
type ContextCommand struct {
	*BaseCommand
	storage         StorageProvider
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
		storage:         &defaultStorageProvider{},
		onContextChange: onContextChange,
	}
}

// NewContextCommandWithStorage creates a new context command with a custom storage provider.
// This is primarily used for testing.
func NewContextCommandWithStorage(client ClientInterface, output OutputLogger, transport TransportInterface, onContextChange func(string), storage StorageProvider) *ContextCommand {
	return &ContextCommand{
		BaseCommand:     NewBaseCommand(client, output, transport),
		storage:         storage,
		onContextChange: onContextChange,
	}
}

// Execute runs the context command with the given arguments.
// Subcommands:
//   - (no args): Show current context
//   - list/ls: List all available contexts
//   - use/switch <name>: Switch to a different context
//
// The ctx parameter is passed through to subcommands for cancellation support.
func (c *ContextCommand) Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return c.showCurrent(ctx)
	}

	subCmd := strings.ToLower(args[0])
	switch subCmd {
	case "list", "ls":
		return c.listContexts(ctx)
	case "use", "switch":
		if len(args) < 2 {
			return fmt.Errorf("usage: context use <name>")
		}
		return c.switchContext(ctx, args[1])
	default:
		// Treat single argument as context name to switch to
		return c.switchContext(ctx, args[0])
	}
}

// showCurrent displays the current context name.
func (c *ContextCommand) showCurrent(_ context.Context) error {
	name, err := c.storage.GetCurrentContextName()
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
func (c *ContextCommand) listContexts(_ context.Context) error {
	config, err := c.storage.Load()
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
func (c *ContextCommand) switchContext(_ context.Context, name string) error {
	// Check if context exists
	ctxConfig, err := c.storage.GetContext(name)
	if err != nil {
		return fmt.Errorf("failed to get context: %w", err)
	}
	if ctxConfig == nil {
		// List available contexts for helpful error
		names, _ := c.storage.GetContextNames()
		if len(names) > 0 {
			return fmt.Errorf("context %q not found. Available contexts: %s", name, strings.Join(names, ", "))
		}
		return fmt.Errorf("context %q not found. No contexts configured", name)
	}

	// Set as current context
	if err := c.storage.SetCurrentContext(name); err != nil {
		return fmt.Errorf("failed to switch context: %w", err)
	}

	c.output.Success("Switched to context %q", name)
	c.output.OutputLine("Endpoint: %s", ctxConfig.Endpoint)
	c.output.OutputLine("")
	c.output.OutputLine("Prompt updated. Restart REPL to connect to the new endpoint.")

	// Notify callback of context change
	if c.onContextChange != nil {
		c.onContextChange(name)
	}

	return nil
}

// Usage returns the usage string for the context command.
func (c *ContextCommand) Usage() string {
	return "context [list|ls|use|switch <name>]"
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
		return c.GetContextNames()
	}

	return nil
}

// GetContextNames returns available context names for completion.
// This method is exported for use by the REPL completer.
func (c *ContextCommand) GetContextNames() []string {
	names, err := c.storage.GetContextNames()
	if err != nil {
		return nil
	}
	return names
}

// Aliases returns alternative names for the context command.
func (c *ContextCommand) Aliases() []string {
	return []string{"ctx"}
}
