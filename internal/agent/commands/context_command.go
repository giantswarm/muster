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
// It lazily initializes storage on first use to avoid unnecessary file system access.
type defaultStorageProvider struct {
	storage *musterctx.Storage
}

// getStorage returns the cached storage instance, creating it on first access.
// This avoids repeated NewStorage() calls which would read the config file each time.
func (d *defaultStorageProvider) getStorage() (*musterctx.Storage, error) {
	if d.storage != nil {
		return d.storage, nil
	}
	storage, err := musterctx.NewStorage()
	if err != nil {
		return nil, err
	}
	d.storage = storage
	return d.storage, nil
}

func (d *defaultStorageProvider) GetCurrentContextName() (string, error) {
	storage, err := d.getStorage()
	if err != nil {
		return "", err
	}
	return storage.GetCurrentContextName()
}

func (d *defaultStorageProvider) Load() (*musterctx.ContextConfig, error) {
	storage, err := d.getStorage()
	if err != nil {
		return nil, err
	}
	return storage.Load()
}

func (d *defaultStorageProvider) GetContext(name string) (*musterctx.Context, error) {
	storage, err := d.getStorage()
	if err != nil {
		return nil, err
	}
	return storage.GetContext(name)
}

func (d *defaultStorageProvider) GetContextNames() ([]string, error) {
	storage, err := d.getStorage()
	if err != nil {
		return nil, err
	}
	return storage.GetContextNames()
}

func (d *defaultStorageProvider) SetCurrentContext(name string) error {
	storage, err := d.getStorage()
	if err != nil {
		return err
	}
	return storage.SetCurrentContext(name)
}

// ReconnectFunc is the function signature for reconnecting to a new endpoint.
type ReconnectFunc func(ctx context.Context, endpoint string) error

// ContextCommand handles context listing and switching in the REPL.
// This command allows users to view available contexts, see the current
// context, and switch between contexts without leaving the REPL.
// When switching contexts, it automatically reconnects to the new endpoint.
type ContextCommand struct {
	*BaseCommand
	storage         StorageProvider
	onContextChange func(contextName string) // Callback when context changes
	onReconnect     ReconnectFunc            // Callback to reconnect to new endpoint
}

// NewContextCommand creates a new context command.
//
// Args:
//   - client: MCP client interface for operations
//   - output: Logger interface for user-facing output
//   - transport: Transport interface for capability checking
//   - onContextChange: Callback function called when context is switched (updates prompt)
//   - onReconnect: Callback function to reconnect to the new endpoint
//
// Returns:
//   - Configured context command instance
func NewContextCommand(client ClientInterface, output OutputLogger, transport TransportInterface, onContextChange func(string), onReconnect ReconnectFunc) *ContextCommand {
	return &ContextCommand{
		BaseCommand:     NewBaseCommand(client, output, transport),
		storage:         &defaultStorageProvider{},
		onContextChange: onContextChange,
		onReconnect:     onReconnect,
	}
}

// knownSubcommands lists all valid subcommands for typo detection.
var knownSubcommands = []string{"list", "ls", "use", "switch"}

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
		// Check if this looks like a typo of a known subcommand
		if suggestion := c.findSimilarSubcommand(subCmd); suggestion != "" {
			return fmt.Errorf("unknown subcommand %q - did you mean %q? Use 'context use %s' to switch to a context named %q",
				subCmd, suggestion, subCmd, subCmd)
		}
		// Treat single argument as context name to switch to
		return c.switchContext(ctx, args[0])
	}
}

// findSimilarSubcommand checks if the input looks like a typo of a known subcommand.
// Returns the likely intended subcommand if found, empty string otherwise.
func (c *ContextCommand) findSimilarSubcommand(input string) string {
	// Check for common typos using Levenshtein-like simple heuristics
	for _, cmd := range knownSubcommands {
		// Check if only 1-2 characters differ (simple edit distance check)
		if len(input) == len(cmd) && countDifferentChars(input, cmd) <= 2 {
			return cmd
		}
		// Check for missing/extra character
		if absDiff(len(input), len(cmd)) == 1 && hasCommonPrefix(input, cmd, 2) {
			return cmd
		}
	}
	return ""
}

// countDifferentChars counts how many characters differ between two strings of equal length.
func countDifferentChars(a, b string) int {
	diff := 0
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			diff++
		}
	}
	return diff
}

// hasCommonPrefix checks if two strings share at least n common prefix characters.
func hasCommonPrefix(a, b string, n int) bool {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	if minLen < n {
		return false
	}
	common := 0
	for i := 0; i < minLen; i++ {
		if a[i] == b[i] {
			common++
		} else {
			break
		}
	}
	return common >= n
}

// absDiff returns the absolute difference between two integers.
// Using a specialized function instead of a generic abs() to avoid
// importing math package for a simple integer operation.
func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
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

// listContexts displays all available contexts with their endpoints.
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

	// Calculate max name length for alignment
	maxNameLen := 0
	for _, ctx := range config.Contexts {
		if len(ctx.Name) > maxNameLen {
			maxNameLen = len(ctx.Name)
		}
	}

	c.output.OutputLine("Available contexts:")
	for _, ctx := range config.Contexts {
		marker := "  "
		if ctx.Name == config.CurrentContext {
			marker = "* "
		}
		// Pad name for alignment and show endpoint
		padding := strings.Repeat(" ", maxNameLen-len(ctx.Name))
		c.output.OutputLine("%s%s%s  %s", marker, ctx.Name, padding, ctx.Endpoint)
	}

	return nil
}

// switchContext switches to a different context and reconnects to the new endpoint.
func (c *ContextCommand) switchContext(ctx context.Context, name string) error {
	// Check if context exists
	ctxConfig, err := c.storage.GetContext(name)
	if err != nil {
		return fmt.Errorf("failed to get context: %w", err)
	}
	if ctxConfig == nil {
		// List available contexts for helpful error
		names, _ := c.storage.GetContextNames()
		if len(names) > 0 {
			return fmt.Errorf("context %q not found. Available: %s", name, strings.Join(names, ", "))
		}
		return fmt.Errorf("context %q not found. No contexts configured", name)
	}

	// Set as current context in storage
	if err := c.storage.SetCurrentContext(name); err != nil {
		return fmt.Errorf("failed to switch context: %w", err)
	}

	// Consolidated output: context name and endpoint on one line
	c.output.Success("Switched to %s (%s)", name, ctxConfig.Endpoint)

	// Notify callback of context change (updates prompt)
	if c.onContextChange != nil {
		c.onContextChange(name)
	}

	// Attempt to reconnect to the new endpoint
	if c.onReconnect != nil {
		if err := c.onReconnect(ctx, ctxConfig.Endpoint); err != nil {
			c.output.Error("Failed to reconnect: %v", err)
			c.output.OutputLine("Run 'muster agent --repl' to restart with the new endpoint.")
			return nil // Don't fail the command, context was switched successfully
		}
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
