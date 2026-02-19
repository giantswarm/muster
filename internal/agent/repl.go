package agent

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	musterctx "github.com/giantswarm/muster/internal/context"

	agentoauth "github.com/giantswarm/muster/internal/agent/oauth"

	"github.com/giantswarm/muster/internal/agent/commands"
	"github.com/giantswarm/muster/internal/api"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/chzyer/readline"
	"github.com/mark3labs/mcp-go/mcp"
)

// promptPrefixUnicode uses a mathematical bold "m" (𝗺) for muster branding in the REPL prompt.
// Used when terminal supports unicode (most modern terminals).
const promptPrefixUnicode = "𝗺"

// promptPrefixASCII is the fallback prefix for terminals without unicode support.
const promptPrefixASCII = "m"

// promptChevronUnicode is the guillemet separator used in the prompt.
const promptChevronUnicode = "»"

// promptChevronASCII is the fallback chevron for terminals without unicode support.
const promptChevronASCII = ">"

// StateAuthRequired is the indicator shown in the REPL prompt when servers require authentication.
// This is displayed prominently in uppercase because it requires user action (running 'auth login').
// Exported for use by external tools that need to interpret REPL output.
const StateAuthRequired = "[AUTH REQUIRED]"

// maxContextNameLength is the maximum length for context names in the prompt.
// Longer names are truncated with smart ellipsis to preserve distinguishing suffix.
const maxContextNameLength = 28

// commandExecutionTimeout is the timeout for individual REPL command execution.
// Set to 5 minutes to allow for long-running tool calls while still providing
// a safety net against hung operations.
const commandExecutionTimeout = 5 * time.Minute

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
	authRequired     bool   // Whether any servers require authentication
	useUnicode       bool   // Whether to use unicode characters in prompt
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
	ctxName, ctxErr := loadCurrentContextWithError()
	if ctxErr != nil {
		logger.Debug("Failed to load current context: %v", ctxErr)
	}

	repl := &REPL{
		client:           client,
		logger:           logger,
		notificationChan: make(chan mcp.JSONRPCNotification, 10),
		stopChan:         make(chan struct{}),
		commandRegistry:  commands.NewRegistry(),
		currentContext:   ctxName,
		useUnicode:       detectUnicodeSupport(),
	}

	// Register all commands
	repl.registerCommands()

	return repl
}

// loadCurrentContextWithError retrieves the current context name from storage.
// Returns the context name and any error encountered.
func loadCurrentContextWithError() (string, error) {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return "", fmt.Errorf("failed to initialize context storage: %w", err)
	}

	name, err := storage.GetCurrentContextName()
	if err != nil {
		return "", fmt.Errorf("failed to get current context: %w", err)
	}

	return name, nil
}

// detectUnicodeSupport checks if the terminal likely supports unicode characters.
// Returns true for most modern terminals, false for dumb terminals or when uncertain.
func detectUnicodeSupport() bool {
	term := os.Getenv("TERM")
	lang := os.Getenv("LANG")
	lcAll := os.Getenv("LC_ALL")

	// Dumb terminals or no terminal don't support unicode
	if term == "" || term == "dumb" {
		return false
	}

	// Check for UTF-8 in locale settings
	for _, v := range []string{lang, lcAll} {
		if strings.Contains(strings.ToLower(v), "utf-8") || strings.Contains(strings.ToLower(v), "utf8") {
			return true
		}
	}

	// Common terminals that support unicode
	// Note: vt100 is intentionally excluded as it's a legacy terminal without unicode support
	unicodeTerminals := []string{"xterm", "screen", "tmux", "alacritty", "kitty", "iterm"}
	termLower := strings.ToLower(term)
	for _, ut := range unicodeTerminals {
		if strings.Contains(termLower, ut) {
			return true
		}
	}

	// Default to true for most modern environments
	return true
}

// buildPrompt creates the REPL prompt with the current context.
// Format examples:
//   - "𝗺 »" - no context set
//   - "𝗺 mycontext »" - context set
//   - "𝗺 [AUTH REQUIRED] »" - no context, auth required
//   - "𝗺 mycontext [AUTH REQUIRED] »" - context set, auth required
//
// The AUTH REQUIRED status is displayed prominently in uppercase to draw attention
// since it requires user action (run 'auth login'). No status is shown when connected
// normally to keep the prompt clean.
//
// Long context names are truncated to maxContextNameLength characters with smart ellipsis.
// Falls back to ASCII characters if terminal doesn't support unicode.
func (r *REPL) buildPrompt() string {
	r.mu.RLock()
	ctx := r.currentContext
	authReq := r.authRequired
	useUnicode := r.useUnicode
	r.mu.RUnlock()

	// Select prefix and chevron based on unicode support
	prefix := promptPrefixASCII
	chevron := promptChevronASCII
	if useUnicode {
		prefix = promptPrefixUnicode
		chevron = promptChevronUnicode
	}

	var parts []string
	parts = append(parts, prefix)

	if ctx != "" {
		parts = append(parts, truncateContextName(ctx))
	}

	// Only show AUTH REQUIRED when authentication is needed
	if authReq {
		parts = append(parts, StateAuthRequired)
	}

	parts = append(parts, chevron)

	return strings.Join(parts, " ") + " "
}

// truncateContextName truncates long context names to fit in the prompt.
// Uses smart truncation that preserves both the start and end of the name,
// replacing the middle with "..." to keep distinguishing prefixes and suffixes.
// Example: "production-us-east-1-cluster" becomes "production-...cluster"
func truncateContextName(name string) string {
	if len(name) <= maxContextNameLength {
		return name
	}

	// Keep more of the start (60%) and less of the end (40%) after ellipsis
	ellipsis := "..."
	available := maxContextNameLength - len(ellipsis)
	startLen := (available * 3) / 5 // 60% of available space
	endLen := available - startLen  // 40% of available space

	return name[:startLen] + ellipsis + name[len(name)-endLen:]
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

// reconnectToEndpoint reconnects the client to a new endpoint.
// This is called when switching to a context with a different endpoint.
// Returns nil if the endpoint hasn't changed.
//
// If authentication fails (401), this method will automatically attempt to
// re-authenticate using the auth handler, then retry the connection.
func (r *REPL) reconnectToEndpoint(ctx context.Context, newEndpoint string) error {
	currentEndpoint := r.client.GetEndpoint()
	if currentEndpoint == newEndpoint {
		r.logger.Debug("Same endpoint, skipping reconnection")
		return nil // Same endpoint, no reconnection needed
	}

	// Show connecting indicator
	r.logger.Output("Connecting...")

	err := r.client.Reconnect(ctx, newEndpoint)
	if err != nil {
		// Check if this is an authentication error
		if isAuthError(err) {
			r.logger.Info("Authentication required for new endpoint")

			// Attempt to re-authenticate
			if authErr := r.authenticateForEndpoint(ctx, newEndpoint); authErr != nil {
				return fmt.Errorf("authentication failed: %w", authErr)
			}

			// Retry connection after authentication
			r.logger.Output("Retrying connection...")
			if retryErr := r.client.Reconnect(ctx, newEndpoint); retryErr != nil {
				return fmt.Errorf("failed to reconnect after authentication: %w", retryErr)
			}
		} else {
			return fmt.Errorf("failed to reconnect: %w", err)
		}
	}

	// Refresh the tab completer with new tools/resources/prompts
	if r.rl != nil {
		r.rl.Config.AutoComplete = r.createCompleter()
	}

	// Check auth status for the new endpoint
	r.checkAuthRequired()

	r.logger.Success("Connected")
	return nil
}

// isAuthError checks if an error is related to authentication (401 Unauthorized).
func isAuthError(err error) bool {
	return pkgoauth.IsOAuthUnauthorizedError(err)
}

// authenticateForEndpoint triggers OAuth authentication for the given endpoint.
// It sets up the mcp-go OAuth transport and triggers login if needed.
func (r *REPL) authenticateForEndpoint(ctx context.Context, endpoint string) error {
	if !isRemoteEndpoint(endpoint) {
		return nil
	}

	handler := api.GetAuthHandler()
	if handler == nil {
		return fmt.Errorf("authentication handler not available - try restarting with 'muster agent --repl'")
	}

	if sessionID := handler.GetSessionID(); sessionID != "" {
		r.client.SetHeader(api.ClientSessionIDHeader, sessionID)
	}

	// Set up mcp-go OAuth transport for this endpoint
	oauthCfg, agentStore, err := agentoauth.SetupOAuthConfig(endpoint)
	if err != nil {
		return fmt.Errorf("failed to set up OAuth: %w", err)
	}
	r.client.SetOAuthConfig(*oauthCfg, agentStore)

	// Check if we already have a valid token -- no login needed
	if handler.HasValidToken(endpoint) {
		r.logger.Info("Using existing authentication token")
		return nil
	}

	r.logger.Info("Starting OAuth login flow...")
	r.logger.Info("A browser window will open for authentication.")

	if err := handler.Login(ctx, endpoint); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	r.logger.Success("Authentication successful")
	return nil
}

// isRemoteEndpoint checks if an endpoint URL points to a remote server.
// It properly parses the URL and checks only the hostname, avoiding false positives
// when "localhost" appears in the path or query string.
func isRemoteEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		// If we can't parse the URL, assume it's remote for safety
		return true
	}

	host := strings.ToLower(u.Hostname())
	return host != "localhost" && host != "127.0.0.1" && host != "::1"
}

// checkAuthRequired checks if any servers require authentication and updates the prompt.
// Prints an actionable hint when auth status changes to requiring authentication.
func (r *REPL) checkAuthRequired() {
	authInfos := r.client.GetAuthRequired()
	authRequired := len(authInfos) > 0

	r.mu.Lock()
	changed := r.authRequired != authRequired
	r.authRequired = authRequired
	r.mu.Unlock()

	if changed {
		r.updatePrompt()

		// Print actionable hint when auth becomes required
		if authRequired {
			serverNames := make([]string, 0, len(authInfos))
			for _, info := range authInfos {
				serverNames = append(serverNames, info.Server)
			}
			r.logger.Info("Authentication required for: %s", strings.Join(serverNames, ", "))
			r.logger.Info("Run 'auth login' to authenticate")
		}
	}
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
	r.commandRegistry.Register("context", commands.NewContextCommand(r.client, r.logger, transport, r.setCurrentContext, r.reconnectToEndpoint))
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
	commandCtx, commandCancel := context.WithTimeout(context.Background(), commandExecutionTimeout)
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

	// Check initial auth status for prompt display
	r.checkAuthRequired()

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

			// Update completer and auth status if items changed
			switch notification.Method {
			case "notifications/tools/list_changed",
				"notifications/resources/list_changed",
				"notifications/prompts/list_changed":
				if r.rl != nil {
					r.rl.Config.AutoComplete = r.createCompleter()
				}
				// Resources changed - auth status may have updated
				if notification.Method == "notifications/resources/list_changed" {
					r.checkAuthRequired()
				}
			}

			// Refresh readline prompt for continued interaction
			if r.rl != nil {
				r.rl.Refresh()
			}
		}
	}
}
