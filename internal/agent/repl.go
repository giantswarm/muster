package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"muster/internal/agent/commands"
	musterctx "muster/internal/context"

	"github.com/chzyer/readline"
	"github.com/mark3labs/mcp-go/mcp"
)

// promptPrefix uses a mathematical bold "m" (𝗺) for muster branding in the REPL prompt.
const promptPrefix = "𝗺"

// promptChevron is the guillemet separator used in the prompt.
const promptChevron = "»"

// REPL represents an interactive Read-Eval-Print Loop for MCP interaction.
// It provides a command-line interface for exploring and testing MCP capabilities
// with features like tab completion, command history, and real-time notifications.
//
// The REPL uses a modular command system where each command implements the Command
// interface, enabling extensible functionality and consistent user experience.
// Commands support aliases, usage documentation, and context-aware tab completion.
//
// Key features:
//   - Interactive command execution with argument parsing
//   - Tab completion for commands, tool names, and args
//   - Persistent command history across sessions
//   - Real-time notification display (SSE transport)
//   - Graceful error handling and recovery
//   - Transport-aware feature adaptation
//   - Stylish prompt with current context display
type REPL struct {
	client           *Client
	logger           *Logger
	rl               *readline.Instance
	notificationChan chan mcp.JSONRPCNotification
	stopChan         chan struct{}
	wg               sync.WaitGroup
	commandRegistry  *commands.Registry
	currentContext   string // Current muster context name for prompt display
	mu               sync.RWMutex
}

// NewREPL creates a new REPL instance with the specified client and logger.
// It initializes the command registry and registers all available commands
// with their respective aliases and completion handlers.
//
// Args:
//   - client: MCP client for server communication
//   - logger: Logger instance for structured output and debugging
//
// The REPL is created with:
//   - Pre-registered command set (help, list, describe, call, etc.)
//   - Notification channel for real-time updates
//   - Command registry with alias support
//   - Transport adapter for command execution
//   - Current context detection for stylish prompt display
//
// Example:
//
//	client := agent.NewClient("http://localhost:8090/sse", logger, agent.TransportSSE)
//	repl := agent.NewREPL(client, logger)
//	if err := repl.Run(ctx); err != nil {
//	    log.Fatal(err)
//	}
func NewREPL(client *Client, logger *Logger) *REPL {
	repl := &REPL{
		client:           client,
		logger:           logger,
		notificationChan: make(chan mcp.JSONRPCNotification, 10),
		stopChan:         make(chan struct{}),
		commandRegistry:  commands.NewRegistry(),
		currentContext:   loadCurrentContext(),
	}

	// Register all commands
	repl.registerCommands()

	return repl
}

// loadCurrentContext retrieves the current context name from storage.
// Returns an empty string if no context is set or on error.
func loadCurrentContext() string {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return ""
	}

	name, err := storage.GetCurrentContextName()
	if err != nil {
		return ""
	}

	return name
}

// buildPrompt creates the REPL prompt with the current context.
// Format: m context » or m » if no context is set.
func (r *REPL) buildPrompt() string {
	r.mu.RLock()
	ctx := r.currentContext
	r.mu.RUnlock()

	if ctx == "" {
		return fmt.Sprintf("%s %s ", promptPrefix, promptChevron)
	}

	return fmt.Sprintf("%s %s %s ", promptPrefix, ctx, promptChevron)
}

// updatePrompt refreshes the readline prompt with the current context.
// This should be called when the context changes.
func (r *REPL) updatePrompt() {
	if r.rl != nil {
		r.rl.SetPrompt(r.buildPrompt())
	}
}

// setCurrentContext updates the current context and refreshes the prompt.
// This is called by the context command when switching contexts.
func (r *REPL) setCurrentContext(name string) {
	r.mu.Lock()
	r.currentContext = name
	r.mu.Unlock()

	r.updatePrompt()
}

// registerCommands registers all available commands with the command registry.
// This method initializes the complete command set for the REPL, including
// aliases and transport adapters for each command. The commands are registered
// with consistent naming and provide comprehensive functionality for MCP interaction.
//
// Registered commands:
//   - help: Command documentation and usage information
//   - list: Display available tools, resources, and prompts
//   - describe: Detailed information about specific items
//   - call: Execute tools with argument validation
//   - get: Retrieve resources and execute prompts
//   - prompt: Template-based prompt execution
//   - filter: Advanced pattern-based tool filtering
//   - notifications: Toggle and manage real-time updates
//   - workflow: Execute workflows with parameters
//   - context: List and switch between muster contexts
//   - exit: Graceful session termination
//
// Each command is provided with access to the client, logger, and transport
// adapter to enable consistent functionality across different transport types.
func (r *REPL) registerCommands() {
	// Create transport adapter for commands to check capabilities
	transport := &transportAdapter{client: r.client}

	// Register all commands with their respective implementations
	r.commandRegistry.Register("help", commands.NewHelpCommand(r.client, r.logger, transport, r.commandRegistry))
	r.commandRegistry.Register("list", commands.NewListCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("describe", commands.NewDescribeCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("call", commands.NewCallCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("get", commands.NewGetCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("prompt", commands.NewPromptCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("filter", commands.NewFilterCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("notifications", commands.NewNotificationsCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("workflow", commands.NewWorkflowCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("context", commands.NewContextCommand(r.client, r.logger, transport, r.setCurrentContext))
	r.commandRegistry.Register("exit", commands.NewExitCommand(r.client, r.logger, transport))
}

// transportAdapter adapts Client to TransportInterface for the command system.
// This adapter enables commands to query transport capabilities without
// directly depending on the Client implementation, maintaining clean
// separation of concerns and testability.
//
// The adapter provides:
//   - Transport capability checking for feature adaptation
//   - Consistent interface across different transport types
//   - Clean abstraction for command implementations
type transportAdapter struct {
	client *Client
}

// SupportsNotifications returns whether the underlying transport supports notifications.
// This method enables commands to adapt their behavior based on transport capabilities,
// particularly for features that depend on real-time updates from the server.
//
// Returns:
//   - true if the transport supports real-time notifications (SSE)
//   - false for request-response only transports (Streamable HTTP)
func (t *transportAdapter) SupportsNotifications() bool {
	return t.client.SupportsNotifications()
}

// executeCommand parses and executes a command using the registry.
// This method handles the complete command execution pipeline including
// parsing, alias resolution, validation, and error handling.
//
// Args:
//   - input: Raw command input from the user
//
// The method performs:
//   - Command line parsing and argument extraction
//   - Alias resolution (e.g., "?" -> "help")
//   - Command lookup in the registry
//   - Command execution with timeout context
//   - Error handling and user feedback
//
// Special handling:
//   - Empty input is silently ignored
//   - "?" is automatically translated to "help" command
//   - Unknown commands provide helpful error messages
//   - Execution uses separate timeout context to prevent interference
//
// Returns:
//   - Error for unknown commands or execution failures
//   - nil for successful execution
func (r *REPL) executeCommand(input string) error {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	commandName := strings.ToLower(parts[0])
	args := parts[1:]

	// Handle special case for ? alias to help command
	if commandName == "?" {
		commandName = "help"
	}

	// Get command from registry with alias support
	command, exists := r.commandRegistry.Get(commandName)
	if !exists {
		return fmt.Errorf("unknown command: %s. Type 'help' for available commands", parts[0])
	}

	// Create a separate context for command execution with a reasonable timeout
	// This prevents tool calls from being canceled by agent lifecycle events
	// but still allows for reasonable timeouts and manual cancellation
	commandCtx, commandCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer commandCancel()

	// Execute the command with timeout protection
	return command.Execute(commandCtx, args)
}

// Run starts the REPL and enters the main interaction loop.
// This method initializes the REPL environment, sets up notification handling,
// configures readline with history and completion, and enters the main
// command processing loop.
//
// The method performs complete REPL initialization:
//   - Notification channel routing for supported transports
//   - Readline configuration with history file and tab completion
//   - Background notification listener for real-time updates
//   - Main command processing loop with graceful shutdown
//
// Key features:
//   - Persistent command history across sessions
//   - Context-aware tab completion for commands and args
//   - Real-time notification display (transport dependent)
//   - Graceful shutdown handling for Ctrl+C and EOF
//   - Transport capability adaptation
//
// The REPL continues running until:
//   - Context cancellation (Ctrl+C or external signal)
//   - EOF input (Ctrl+D)
//   - Explicit exit command
//   - Fatal readline errors
//
// Returns:
//   - nil for normal shutdown
//   - Error for initialization or fatal runtime errors
func (r *REPL) Run(ctx context.Context) error {

	// Set up REPL-specific notification channel routing for transports that support notifications
	if r.client.SupportsNotifications() && r.client.NotificationChan != nil {
		go func() {
			for notification := range r.client.NotificationChan {
				select {
				case r.notificationChan <- notification:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Set up readline with comprehensive tab completion and history
	completer := r.createCompleter()
	historyFile := filepath.Join(os.TempDir(), ".muster_agent_history")

	config := &readline.Config{
		Prompt:          r.buildPrompt(),
		HistoryFile:     historyFile,
		AutoComplete:    completer,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",

		HistorySearchFold:   true,
		FuncFilterInputRune: filterInput,
	}

	rl, err := readline.NewEx(config)
	if err != nil {
		return fmt.Errorf("failed to create readline instance: %w", err)
	}
	defer rl.Close()
	r.rl = rl

	// Start notification listener in background for transports that support notifications
	if r.client.SupportsNotifications() {
		r.wg.Add(1)
		go r.notificationListener(ctx)
		r.logger.Info("MCP REPL started with notification support. Type 'help' for available commands. Use TAB for completion.")
	} else {
		r.logger.Info("MCP REPL started. Type 'help' for available commands. Use TAB for completion.")
		r.logger.Info("Note: Real-time notifications are not supported with %s transport.", r.client.transport)
	}
	fmt.Println()

	// Main REPL loop - process commands until shutdown
	for {
		// Check if context is cancelled before each iteration
		select {
		case <-ctx.Done():
			if r.client.SupportsNotifications() {
				close(r.stopChan)
				r.wg.Wait()
			}
			r.logger.Info("REPL shutting down...")
			return nil
		default:
		}

		// Read input with interrupt and EOF handling
		line, err := r.rl.Readline()
		if err == readline.ErrInterrupt {
			if len(line) == 0 {
				continue // Empty line on Ctrl+C, continue
			}
		} else if err == io.EOF {
			// Graceful shutdown on Ctrl+D
			if r.client.SupportsNotifications() {
				close(r.stopChan)
				r.wg.Wait()
			}
			r.logger.Info("Goodbye!")
			return nil
		} else if err != nil {
			return fmt.Errorf("readline error: %w", err)
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue // Skip empty input
		}

		// Parse and execute command with error handling
		if err := r.executeCommand(input); err != nil {
			if err.Error() == "exit" {
				// Explicit exit command
				if r.client.SupportsNotifications() {
					close(r.stopChan)
					r.wg.Wait()
				}
				r.logger.Info("Goodbye!")
				return nil
			}
			// Display command execution errors to user
			r.logger.Error("Error: %v", err)
		}

		fmt.Println() // Add spacing between commands
	}
}

// notificationListener handles notifications in the background for real-time updates.
// This goroutine processes server notifications asynchronously to keep the REPL
// responsive while providing immediate feedback about capability changes.
//
// The listener performs:
//   - Continuous monitoring of the notification channel
//   - Graceful shutdown handling via context and stop channel
//   - Readline interaction management for clean display
//   - Cache refresh triggering for capability changes
//   - Tab completion updates when items change
//
// Notification handling:
//   - Temporarily pauses readline for clean output
//   - Delegates to client's notification handler for processing
//   - Updates tab completion when capability lists change
//   - Refreshes readline prompt for continued interaction
//
// The listener runs until:
//   - Context cancellation
//   - Stop channel signal
//   - Client notification channel closure
//
// This method is only started for transports that support notifications.
func (r *REPL) notificationListener(ctx context.Context) {
	defer r.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopChan:
			return
		case notification := <-r.notificationChan:
			// Temporarily pause readline for clean notification display
			if r.rl != nil {
				r.rl.Stdout().Write([]byte("\r\033[K"))
			}

			// Handle the notification (this will log it and update caches)
			if err := r.client.handleNotification(ctx, notification); err != nil {
				r.logger.Error("Failed to handle notification: %v", err)
			}

			// Update completer if items changed to reflect new capabilities
			switch notification.Method {
			case "notifications/tools/list_changed",
				"notifications/resources/list_changed",
				"notifications/prompts/list_changed":
				if r.rl != nil {
					r.rl.Config.AutoComplete = r.createCompleter()
				}
			}

			// Refresh readline prompt for continued interaction
			if r.rl != nil {
				r.rl.Refresh()
			}
		}
	}
}
