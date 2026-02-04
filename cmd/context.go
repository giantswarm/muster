package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	musterctx "github.com/giantswarm/muster/internal/context"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	contextAddEndpoint      string
	contextAddSetCurrent    bool
	contextDeleteForce      bool
	contextQuiet            bool
	contextShowOutputFormat string
	contextUpdateEndpoint   string
)

// contextCmd represents the context command group
var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage muster contexts",
	Long: `Manage named contexts for different muster endpoints.

Contexts provide a convenient way to work with multiple muster aggregator
endpoints without specifying --endpoint for every command. Similar to
kubectl's context management.

Examples:
  muster context                              # List all contexts
  muster context list                         # List all contexts (alias: ls)
  muster context current                      # Show current context
  muster context use production               # Switch to context (alias: switch)
  muster context add staging --endpoint <url> # Add new context
  muster context add staging --endpoint <url> --use  # Add and switch
  muster context update staging --endpoint <url>     # Update context (alias: set)
  muster context delete staging               # Remove a context (alias: rm)
  muster context delete staging --force       # Remove without confirmation
  muster context rename staging stage         # Rename a context
  muster context show production              # Show details (alias: describe)
  muster context show production -o json      # Show as JSON

Context Configuration:
  Contexts are stored in ~/.config/muster/contexts.yaml

Precedence (highest to lowest):
  1. --endpoint flag
  2. --context flag  
  3. MUSTER_CONTEXT environment variable
  4. current-context from contexts.yaml
  5. Local fallback (http://localhost:8090/mcp)`,
	Args: cobra.NoArgs,
	RunE: runContextList,
}

// contextListCmd lists all contexts
var contextListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all contexts",
	Long: `List all configured contexts.

The current context is marked with an asterisk (*).

Examples:
  muster context list
  muster context ls`,
	Args: cobra.NoArgs,
	RunE: runContextList,
}

// contextCurrentCmd shows the current context
var contextCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show current context name",
	Long: `Display the name of the currently active context.

Returns nothing if no context is set.

Examples:
  muster context current`,
	Args: cobra.NoArgs,
	RunE: runContextCurrent,
}

// contextUseCmd switches the current context
var contextUseCmd = &cobra.Command{
	Use:     "use <name>",
	Aliases: []string{"switch"},
	Short:   "Switch to a different context",
	Long: `Set the current context to the specified name.

The context must already exist. Use 'muster context add' to create new contexts.

Examples:
  muster context use production
  muster context switch staging`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeContextNames,
	RunE:              runContextUse,
}

// contextAddCmd adds a new context
var contextAddCmd = &cobra.Command{
	Use:   "add <name> --endpoint <url>",
	Short: "Add a new context",
	Long: `Add a new named context pointing to a muster endpoint.

Context names must:
  - Be between 1 and 63 characters
  - Contain only lowercase letters, numbers, and hyphens
  - Start and end with an alphanumeric character

Examples:
  muster context add local --endpoint http://localhost:8090/mcp
  muster context add staging --endpoint https://muster-staging.example.com/mcp
  muster context add production --endpoint https://muster.example.com/mcp --use`,
	Args: cobra.ExactArgs(1),
	RunE: runContextAdd,
}

// contextDeleteCmd removes a context
var contextDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Aliases: []string{"rm", "remove"},
	Short:   "Delete a context",
	Long: `Remove a context by name.

If the deleted context was the current context, the current context will be cleared.

By default, this command asks for confirmation. Use --force to skip the prompt.

Examples:
  muster context delete staging
  muster context delete staging --force
  muster context rm staging -f`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeContextNames,
	RunE:              runContextDelete,
}

// contextRenameCmd renames a context
var contextRenameCmd = &cobra.Command{
	Use:   "rename <old-name> <new-name>",
	Short: "Rename a context",
	Long: `Rename an existing context.

If the renamed context was the current context, the current context will be updated.

Examples:
  muster context rename staging stage
  muster context rename prod production`,
	Args: cobra.ExactArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			// Completing old name - suggest existing contexts
			return getContextNamesForCompletion(), cobra.ShellCompDirectiveNoFileComp
		}
		// Completing new name - no suggestions
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: runContextRename,
}

// contextShowCmd shows details of a context
var contextShowCmd = &cobra.Command{
	Use:     "show <name>",
	Aliases: []string{"describe", "get"},
	Short:   "Show context details",
	Long: `Display detailed information about a specific context.

Supports multiple output formats via --output flag.

Examples:
  muster context show production
  muster context describe staging
  muster context show production --output json
  muster context show production -o yaml`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeContextNames,
	RunE:              runContextShow,
}

// contextUpdateCmd updates an existing context
var contextUpdateCmd = &cobra.Command{
	Use:     "update <name> --endpoint <url>",
	Aliases: []string{"set"},
	Short:   "Update an existing context",
	Long: `Update the endpoint or settings of an existing context.

Examples:
  muster context update staging --endpoint https://new-staging.example.com/mcp
  muster context set production --endpoint https://muster.example.com/mcp`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeContextNames,
	RunE:              runContextUpdate,
}

func init() {
	rootCmd.AddCommand(contextCmd)
	contextCmd.AddCommand(contextListCmd)
	contextCmd.AddCommand(contextCurrentCmd)
	contextCmd.AddCommand(contextUseCmd)
	contextCmd.AddCommand(contextAddCmd)
	contextCmd.AddCommand(contextDeleteCmd)
	contextCmd.AddCommand(contextRenameCmd)
	contextCmd.AddCommand(contextShowCmd)
	contextCmd.AddCommand(contextUpdateCmd)

	// Global context flags
	contextCmd.PersistentFlags().BoolVarP(&contextQuiet, "quiet", "q", false, "Suppress non-essential output")

	// Add-specific flags
	contextAddCmd.Flags().StringVar(&contextAddEndpoint, "endpoint", "", "Endpoint URL for the context (required)")
	contextAddCmd.Flags().BoolVar(&contextAddSetCurrent, "use", false, "Set as current context after adding")
	_ = contextAddCmd.MarkFlagRequired("endpoint")

	// Delete-specific flags
	contextDeleteCmd.Flags().BoolVarP(&contextDeleteForce, "force", "f", false, "Skip confirmation prompt")

	// Show-specific flags
	contextShowCmd.Flags().StringVarP(&contextShowOutputFormat, "output", "o", "text", "Output format (text, json, yaml)")

	// Update-specific flags
	contextUpdateCmd.Flags().StringVar(&contextUpdateEndpoint, "endpoint", "", "New endpoint URL for the context (required)")
	_ = contextUpdateCmd.MarkFlagRequired("endpoint")
}

// completeContextNames provides shell completion for context names
func completeContextNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return getContextNamesForCompletion(), cobra.ShellCompDirectiveNoFileComp
}

// getContextNamesForCompletion returns context names for shell completion
func getContextNamesForCompletion() []string {
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

func runContextList(cmd *cobra.Command, args []string) error {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return fmt.Errorf("failed to initialize context storage: %w", err)
	}

	config, err := storage.Load()
	if err != nil {
		return fmt.Errorf("failed to load contexts: %w", err)
	}

	if len(config.Contexts) == 0 {
		if !contextQuiet {
			fmt.Println("No contexts configured yet.")
			fmt.Println("")
			fmt.Println("Get started by adding your first context:")
			fmt.Println("  muster context add local --endpoint http://localhost:8090/mcp")
			fmt.Println("  muster context add prod --endpoint https://muster.example.com/mcp")
			fmt.Println("")
			fmt.Println("Then activate it:")
			fmt.Println("  muster context use local")
		}
		return nil
	}

	// Use tabwriter for aligned output
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CURRENT\tNAME\tENDPOINT")

	for _, ctx := range config.Contexts {
		current := ""
		if ctx.Name == config.CurrentContext {
			current = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", current, ctx.Name, ctx.Endpoint)
	}

	return w.Flush()
}

func runContextCurrent(cmd *cobra.Command, args []string) error {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return fmt.Errorf("failed to initialize context storage: %w", err)
	}

	name, err := storage.GetCurrentContextName()
	if err != nil {
		return fmt.Errorf("failed to get current context: %w", err)
	}

	if name == "" {
		// No current context - output nothing (useful for scripting)
		return nil
	}

	fmt.Println(name)
	return nil
}

func runContextUse(cmd *cobra.Command, args []string) error {
	name := args[0]

	storage, err := musterctx.NewStorage()
	if err != nil {
		return fmt.Errorf("failed to initialize context storage: %w", err)
	}

	if err := storage.SetCurrentContext(name); err != nil {
		// Provide helpful message if context doesn't exist
		var notFoundErr *musterctx.ContextNotFoundError
		if errors.As(err, &notFoundErr) {
			return fmt.Errorf("context %q not found. Use 'muster context list' to see available contexts", name)
		}
		return fmt.Errorf("failed to set current context: %w", err)
	}

	if !contextQuiet {
		fmt.Printf("Switched to context %q\n", name)
	}
	return nil
}

func runContextAdd(cmd *cobra.Command, args []string) error {
	name := args[0]

	storage, err := musterctx.NewStorage()
	if err != nil {
		return fmt.Errorf("failed to initialize context storage: %w", err)
	}

	if err := storage.AddContext(name, contextAddEndpoint, nil); err != nil {
		return fmt.Errorf("failed to add context: %w", err)
	}

	if !contextQuiet {
		fmt.Printf("Context %q added.\n", name)
	}

	// Set as current context if --use flag is provided
	if contextAddSetCurrent {
		if err := storage.SetCurrentContext(name); err != nil {
			return fmt.Errorf("failed to set current context: %w", err)
		}
		if !contextQuiet {
			fmt.Printf("Switched to context %q\n", name)
		}
	} else if !contextQuiet {
		// Suggest setting as current if no current context
		currentName, _ := storage.GetCurrentContextName()
		if currentName == "" {
			fmt.Printf("\nTo use this context, run:\n")
			fmt.Printf("  muster context use %s\n", name)
		}
	}

	return nil
}

func runContextDelete(cmd *cobra.Command, args []string) error {
	name := args[0]

	storage, err := musterctx.NewStorage()
	if err != nil {
		return fmt.Errorf("failed to initialize context storage: %w", err)
	}

	// Check if context exists before prompting
	ctx, err := storage.GetContext(name)
	if err != nil {
		return fmt.Errorf("failed to check context: %w", err)
	}
	if ctx == nil {
		return fmt.Errorf("context %q not found", name)
	}

	// Check if this is the current context
	currentName, _ := storage.GetCurrentContextName()
	wasCurrent := currentName == name

	// Prompt for confirmation unless --force is used
	if !contextDeleteForce {
		prompt := fmt.Sprintf("Delete context %q?", name)
		if wasCurrent {
			prompt = fmt.Sprintf("Delete context %q (current context)?", name)
		}
		if !confirmAction(prompt) {
			if !contextQuiet {
				fmt.Println("Aborted.")
			}
			return nil
		}
	}

	if err := storage.DeleteContext(name); err != nil {
		var notFoundErr *musterctx.ContextNotFoundError
		if errors.As(err, &notFoundErr) {
			return fmt.Errorf("context %q not found", name)
		}
		return fmt.Errorf("failed to delete context: %w", err)
	}

	if !contextQuiet {
		fmt.Printf("Context %q deleted.\n", name)

		if wasCurrent {
			fmt.Println("Note: This was the current context. Current context is now unset.")
		}
	}

	return nil
}

func runContextRename(cmd *cobra.Command, args []string) error {
	oldName := args[0]
	newName := args[1]

	storage, err := musterctx.NewStorage()
	if err != nil {
		return fmt.Errorf("failed to initialize context storage: %w", err)
	}

	if err := storage.RenameContext(oldName, newName); err != nil {
		return fmt.Errorf("failed to rename context: %w", err)
	}

	if !contextQuiet {
		fmt.Printf("Context %q renamed to %q.\n", oldName, newName)
	}
	return nil
}

// contextDetails represents the output structure for show command
type contextDetails struct {
	Name     string                     `json:"name" yaml:"name"`
	Endpoint string                     `json:"endpoint" yaml:"endpoint"`
	Current  bool                       `json:"current" yaml:"current"`
	Settings *musterctx.ContextSettings `json:"settings,omitempty" yaml:"settings,omitempty"`
}

func runContextShow(cmd *cobra.Command, args []string) error {
	name := args[0]

	storage, err := musterctx.NewStorage()
	if err != nil {
		return fmt.Errorf("failed to initialize context storage: %w", err)
	}

	config, err := storage.Load()
	if err != nil {
		return fmt.Errorf("failed to load contexts: %w", err)
	}

	ctx := config.GetContext(name)
	if ctx == nil {
		return fmt.Errorf("context %q not found", name)
	}

	isCurrent := config.CurrentContext == name

	switch contextShowOutputFormat {
	case "json":
		output := contextDetails{
			Name:     ctx.Name,
			Endpoint: ctx.Endpoint,
			Current:  isCurrent,
			Settings: ctx.Settings,
		}
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(data))

	case "yaml":
		output := contextDetails{
			Name:     ctx.Name,
			Endpoint: ctx.Endpoint,
			Current:  isCurrent,
			Settings: ctx.Settings,
		}
		data, err := yaml.Marshal(output)
		if err != nil {
			return fmt.Errorf("failed to marshal YAML: %w", err)
		}
		fmt.Print(string(data))

	default: // "text" or any other value
		fmt.Printf("Name:     %s\n", ctx.Name)
		fmt.Printf("Endpoint: %s\n", ctx.Endpoint)

		if isCurrent {
			fmt.Printf("Current:  yes\n")
		}

		if ctx.Settings != nil {
			fmt.Println("Settings:")
			if ctx.Settings.Output != "" {
				fmt.Printf("  output: %s\n", ctx.Settings.Output)
			}
		}
	}

	return nil
}

func runContextUpdate(cmd *cobra.Command, args []string) error {
	name := args[0]

	storage, err := musterctx.NewStorage()
	if err != nil {
		return fmt.Errorf("failed to initialize context storage: %w", err)
	}

	if err := storage.UpdateContext(name, contextUpdateEndpoint, nil); err != nil {
		var notFoundErr *musterctx.ContextNotFoundError
		if errors.As(err, &notFoundErr) {
			return fmt.Errorf("context %q not found. Use 'muster context add' to create a new context", name)
		}
		return fmt.Errorf("failed to update context: %w", err)
	}

	if !contextQuiet {
		fmt.Printf("Context %q updated.\n", name)
	}
	return nil
}

// confirmAction prompts the user for confirmation and returns true if they confirm.
func confirmAction(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}
