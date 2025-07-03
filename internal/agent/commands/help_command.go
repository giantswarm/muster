package commands

import (
	"context"
	"strings"
)

// HelpCommand shows available commands and usage information
type HelpCommand struct {
	*BaseCommand
	registry *Registry
}

// NewHelpCommand creates a new help command
func NewHelpCommand(client ClientInterface, output OutputLogger, transport TransportInterface, registry *Registry) *HelpCommand {
	return &HelpCommand{
		BaseCommand: NewBaseCommand(client, output, transport),
		registry:    registry,
	}
}

// Execute shows help information
func (h *HelpCommand) Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		h.showGeneralHelp()
		return nil
	}

	// Show help for specific command
	commandName := strings.ToLower(args[0])

	// Handle ? alias
	if commandName == "?" {
		commandName = "help"
	}

	command, exists := h.registry.Get(commandName)
	if !exists {
		h.output.Error("Unknown command: %s", commandName)
		h.output.OutputLine("Use 'help' to see all available commands.")
		return nil
	}

	h.showCommandHelp(commandName, command)
	return nil
}

// showGeneralHelp displays the general help message
func (h *HelpCommand) showGeneralHelp() {
	h.output.OutputLine("Available commands:")
	h.output.OutputLine("  help, ?                      - Show this help message")
	h.output.OutputLine("  list tools                   - List all available tools")
	h.output.OutputLine("  list resources               - List all available resources")
	h.output.OutputLine("  list prompts                 - List all available prompts")
	h.output.OutputLine("  list workflows               - List all available workflows with parameters")
	h.output.OutputLine("  list core-tools              - List core muster tools (built-in functionality)")
	h.output.OutputLine("  filter tools [pattern] [desc] [case] [detailed] - Filter tools by name pattern or description")
	h.output.OutputLine("  describe tool <name>         - Show detailed information about a tool")
	h.output.OutputLine("  describe resource <uri>      - Show detailed information about a resource")
	h.output.OutputLine("  describe prompt <name>       - Show detailed information about a prompt")
	h.output.OutputLine("  call <tool> {json}           - Execute a tool with JSON arguments")
	h.output.OutputLine("  get <resource-uri>           - Retrieve a resource")
	h.output.OutputLine("  prompt <name> {json}         - Get a prompt with JSON arguments")
	h.output.OutputLine("  workflow <name> [param=val]  - Execute a workflow with optional parameters")
	h.output.OutputLine("  notifications <on|off>       - Enable/disable notification display")
	h.output.OutputLine("  exit, quit                   - Exit the REPL")
	h.output.OutputLine("")
	h.output.OutputLine("Keyboard shortcuts:")
	h.output.OutputLine("  TAB                          - Auto-complete commands and arguments")
	h.output.OutputLine("  ↑/↓ (arrow keys)             - Navigate command history")
	h.output.OutputLine("  Ctrl+R                       - Search command history")
	h.output.OutputLine("  Ctrl+C                       - Cancel current line")
	h.output.OutputLine("  Ctrl+D                       - Exit REPL")
	h.output.OutputLine("")
	h.output.OutputLine("Examples:")
	h.output.OutputLine("  call calculate {\"operation\": \"add\", \"x\": 5, \"y\": 3}")
	h.output.OutputLine("  get docs://readme")
	h.output.OutputLine("  prompt greeting {\"name\": \"Alice\"}")
	h.output.OutputLine("  workflow deploy-app environment=production replicas=3")
	h.output.OutputLine("  workflow auth-setup cluster=test")
	h.output.OutputLine("  filter tools *workflow*      - Find tools with 'workflow' in name")
	h.output.OutputLine("  filter tools \"\" \"kubernetes\" - Find tools with 'kubernetes' in description")
}

// showCommandHelp displays help for a specific command
func (h *HelpCommand) showCommandHelp(commandName string, cmd Command) {
	h.output.OutputLine("Command: %s", commandName)
	h.output.OutputLine("Description: %s", cmd.Description())
	h.output.OutputLine("Usage: %s", cmd.Usage())

	aliases := cmd.Aliases()
	if len(aliases) > 0 {
		h.output.OutputLine("Aliases: %v", aliases)
	}
}

// Usage returns the usage string
func (h *HelpCommand) Usage() string {
	return "help [command]"
}

// Description returns the command description
func (h *HelpCommand) Description() string {
	return "Show help information for commands"
}

// Completions returns possible completions
func (h *HelpCommand) Completions(input string) []string {
	// Return all command names for completion
	return h.registry.AllCompletions()
}

// Aliases returns command aliases
func (h *HelpCommand) Aliases() []string {
	return []string{"?"}
}
