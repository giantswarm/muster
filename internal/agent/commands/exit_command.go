package commands

import (
	"context"
	"fmt"
)

// ExitCommand handles REPL exit
type ExitCommand struct {
	*BaseCommand
}

// NewExitCommand creates a new exit command
func NewExitCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *ExitCommand {
	return &ExitCommand{
		BaseCommand: NewBaseCommand(client, output, transport),
	}
}

// Execute exits the REPL
func (e *ExitCommand) Execute(ctx context.Context, args []string) error {
	// Return special "exit" error to signal REPL shutdown
	return fmt.Errorf("exit")
}

// Usage returns the usage string
func (e *ExitCommand) Usage() string {
	return "exit"
}

// Description returns the command description
func (e *ExitCommand) Description() string {
	return "Exit the REPL"
}

// Completions returns possible completions
func (e *ExitCommand) Completions(input string) []string {
	return []string{}
}

// Aliases returns command aliases
func (e *ExitCommand) Aliases() []string {
	return []string{"quit", "q"}
}
