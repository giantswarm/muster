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

	"github.com/chzyer/readline"
	"github.com/mark3labs/mcp-go/mcp"
)

// REPL represents the Read-Eval-Print Loop for MCP interaction
type REPL struct {
	client           *Client
	logger           *Logger
	rl               *readline.Instance
	notificationChan chan mcp.JSONRPCNotification
	stopChan         chan struct{}
	wg               sync.WaitGroup
	commandRegistry  *commands.Registry
}

// NewREPL creates a new REPL instance
func NewREPL(client *Client, logger *Logger) *REPL {
	repl := &REPL{
		client:           client,
		logger:           logger,
		notificationChan: make(chan mcp.JSONRPCNotification, 10),
		stopChan:         make(chan struct{}),
		commandRegistry:  commands.NewRegistry(),
	}

	// Register all commands
	repl.registerCommands()

	return repl
}

// registerCommands registers all available commands
func (r *REPL) registerCommands() {
	// Create transport adapter for commands
	transport := &transportAdapter{client: r.client}

	// Register all commands
	r.commandRegistry.Register("help", commands.NewHelpCommand(r.client, r.logger, transport, r.commandRegistry))
	r.commandRegistry.Register("list", commands.NewListCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("describe", commands.NewDescribeCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("call", commands.NewCallCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("get", commands.NewGetCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("prompt", commands.NewPromptCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("filter", commands.NewFilterCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("notifications", commands.NewNotificationsCommand(r.client, r.logger, transport))
	r.commandRegistry.Register("exit", commands.NewExitCommand(r.client, r.logger, transport))
}

// transportAdapter adapts Client to TransportInterface
type transportAdapter struct {
	client *Client
}

func (t *transportAdapter) SupportsNotifications() bool {
	return t.client.SupportsNotifications()
}

// executeCommand parses and executes a command using the registry
func (r *REPL) executeCommand(input string) error {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	commandName := strings.ToLower(parts[0])
	args := parts[1:]

	// Handle special case for ? alias
	if commandName == "?" {
		commandName = "help"
	}

	// Get command from registry
	command, exists := r.commandRegistry.Get(commandName)
	if !exists {
		return fmt.Errorf("unknown command: %s. Type 'help' for available commands", parts[0])
	}

	// Create a separate context for command execution with a reasonable timeout
	// This prevents tool calls from being canceled by agent lifecycle events
	// but still allows for reasonable timeouts and manual cancellation
	commandCtx, commandCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer commandCancel()

	// Execute the command
	return command.Execute(commandCtx, args)
}

// Run starts the REPL
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

	// Set up readline with tab completion
	completer := r.createCompleter()
	historyFile := filepath.Join(os.TempDir(), ".muster_agent_history")

	config := &readline.Config{
		Prompt:          "MCP> ",
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

	// Main REPL loop
	for {
		// Check if context is cancelled
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

		// Read input
		line, err := r.rl.Readline()
		if err == readline.ErrInterrupt {
			if len(line) == 0 {
				continue
			}
		} else if err == io.EOF {
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
			continue
		}

		// Parse and execute command
		if err := r.executeCommand(input); err != nil {
			if err.Error() == "exit" {
				if r.client.SupportsNotifications() {
					close(r.stopChan)
					r.wg.Wait()
				}
				r.logger.Info("Goodbye!")
				return nil
			}
			r.logger.Error("Error: %v", err)
		}

		fmt.Println()
	}
}

// notificationListener handles notifications in the background
func (r *REPL) notificationListener(ctx context.Context) {
	defer r.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopChan:
			return
		case notification := <-r.notificationChan:
			// Temporarily pause readline
			if r.rl != nil {
				r.rl.Stdout().Write([]byte("\r\033[K"))
			}

			// Handle the notification (this will log it)
			if err := r.client.handleNotification(ctx, notification); err != nil {
				r.logger.Error("Failed to handle notification: %v", err)
			}

			// Update completer if items changed
			switch notification.Method {
			case "notifications/tools/list_changed",
				"notifications/resources/list_changed",
				"notifications/prompts/list_changed":
				if r.rl != nil {
					r.rl.Config.AutoComplete = r.createCompleter()
				}
			}

			// Refresh readline prompt
			if r.rl != nil {
				r.rl.Refresh()
			}
		}
	}
}
