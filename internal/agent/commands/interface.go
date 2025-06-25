// Package commands provides a shared interface for REPL command implementations.
//
// This package defines the Command interface that all REPL commands must implement,
// enabling a clean registry pattern and improved testability. Commands are responsible
// for their own parsing, execution, and completion logic.
package commands

import (
	"context"
)

// Command represents a REPL command that can be executed interactively.
type Command interface {
	// Execute runs the command with the given arguments
	Execute(ctx context.Context, args []string) error

	// Usage returns the usage string for the command
	Usage() string

	// Description returns a brief description of what the command does
	Description() string

	// Completions returns possible completions for the command
	// The input parameter is the current partial input for context
	Completions(input string) []string

	// Aliases returns alternative names for this command
	Aliases() []string
}

// OutputLogger defines the interface for structured command output.
// This separates user-facing output from system logging.
type OutputLogger interface {
	// User-facing output (goes to stdout, no timestamps)
	Output(format string, args ...interface{})     // For command results
	OutputLine(format string, args ...interface{}) // Same as Output but with newline

	// System messages (structured logging with timestamps)
	Info(format string, args ...interface{})    // Status messages
	Debug(format string, args ...interface{})   // Debug information
	Error(format string, args ...interface{})   // Error messages
	Success(format string, args ...interface{}) // Success messages

	// Configuration
	SetVerbose(verbose bool)
}

// Registry manages available commands for the REPL.
type Registry struct {
	commands map[string]Command
	aliases  map[string]string // alias -> primary command name
}

// NewRegistry creates a new command registry.
func NewRegistry() *Registry {
	return &Registry{
		commands: make(map[string]Command),
		aliases:  make(map[string]string),
	}
}

// Register adds a command to the registry.
func (r *Registry) Register(name string, cmd Command) {
	r.commands[name] = cmd

	// Register aliases
	for _, alias := range cmd.Aliases() {
		r.aliases[alias] = name
	}
}

// Get retrieves a command by name or alias.
func (r *Registry) Get(name string) (Command, bool) {
	// Check direct command name first
	if cmd, exists := r.commands[name]; exists {
		return cmd, true
	}

	// Check aliases
	if primary, exists := r.aliases[name]; exists {
		if cmd, exists := r.commands[primary]; exists {
			return cmd, true
		}
	}

	return nil, false
}

// List returns all registered command names.
func (r *Registry) List() []string {
	var names []string
	for name := range r.commands {
		names = append(names, name)
	}
	return names
}

// AllCompletions returns all possible command completions.
func (r *Registry) AllCompletions() []string {
	var completions []string

	// Add command names
	for name := range r.commands {
		completions = append(completions, name)
	}

	// Add aliases
	for alias := range r.aliases {
		completions = append(completions, alias)
	}

	return completions
}
