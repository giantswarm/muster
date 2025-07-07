package cmd

import (
	"context"
	"fmt"
	"muster/internal/cli"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	startOutputFormat string
	startQuiet        bool
	startConfigPath   string
)

// Available resource types for start operations
var startResourceTypes = []string{
	"service",
	"workflow",
}

// Dynamic completion for service names
func startServiceNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 || args[0] != "service" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Reuse the completion logic from get.go
	return getResourceNameCompletion(cmd, args, toComplete)
}

// Dynamic completion for workflow names
func startWorkflowNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 || args[0] != "workflow" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Get workflow names using the same pattern as getResourceNameCompletion
	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormatJSON,
		Quiet:      true,
		ConfigPath: startConfigPath,
	})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx := context.Background()
	err = executor.Connect(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer executor.Close()

	// Get workflow list
	names, err := getResourceNames(ctx, executor, "core_workflow_list", "workflow")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Filter by what the user has typed so far
	var completions []string
	for _, name := range names {
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(toComplete)) {
			completions = append(completions, name)
		}
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a resource",
	Long: `Start a resource in the muster environment.

Available resource types:
  service   - Start a service by its name
  workflow  - Execute a workflow with optional parameters

Examples:
  muster start service prometheus
  muster start service vault
  muster start workflow deploy-app --environment=production --replicas=3
  muster start workflow auth-setup --cluster=test

Note: The aggregator server must be running (use 'muster serve') before using these commands.`,
	Args: cobra.MinimumNArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return startResourceTypes, cobra.ShellCompDirectiveNoFileComp
		}
		if len(args) == 1 {
			if args[0] == "service" {
				return startServiceNameCompletion(cmd, args, toComplete)
			}
			if args[0] == "workflow" {
				return startWorkflowNameCompletion(cmd, args, toComplete)
			}
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	DisableFlagsInUseLine: true,
	FParseErrWhitelist: cobra.FParseErrWhitelist{
		UnknownFlags: true, // Allow unknown flags for workflow parameters
	},
	RunE: runStart,
}

// Resource type mappings for start operations
var startResourceMappings = map[string]string{
	"service": "core_service_start",
	// Note: workflows use workflow_<workflow-name> pattern, handled separately
}

func init() {
	rootCmd.AddCommand(startCmd)

	// Add flags to the command
	startCmd.PersistentFlags().StringVarP(&startOutputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	startCmd.PersistentFlags().BoolVarP(&startQuiet, "quiet", "q", false, "Suppress non-essential output")
	startCmd.PersistentFlags().StringVar(&startConfigPath, "config-path", "", "Custom configuration directory path")
}

// parseWorkflowParameters extracts workflow parameters from raw command line arguments
// Looks for --param=value or --param value patterns after the workflow name
func parseWorkflowParameters(workflowName string) map[string]interface{} {
	params := make(map[string]interface{})

	// Find the workflow name in os.Args and parse everything after it
	args := os.Args
	workflowIndex := -1

	for i, arg := range args {
		if arg == workflowName && i > 0 && args[i-1] == "workflow" {
			workflowIndex = i
			break
		}
	}

	if workflowIndex == -1 || workflowIndex+1 >= len(args) {
		return params
	}

	// Parse arguments after the workflow name
	workflowArgs := args[workflowIndex+1:]

	for i := 0; i < len(workflowArgs); i++ {
		arg := workflowArgs[i]

		// Handle --param=value format
		if strings.HasPrefix(arg, "--") {
			paramArg := strings.TrimPrefix(arg, "--")

			// Skip known flags
			if paramArg == "output" || paramArg == "quiet" ||
				strings.HasPrefix(paramArg, "output=") ||
				strings.HasPrefix(paramArg, "quiet=") {
				// Skip this and potentially next argument
				if !strings.Contains(paramArg, "=") && i+1 < len(workflowArgs) && !strings.HasPrefix(workflowArgs[i+1], "--") {
					i++ // Skip the value too
				}
				continue
			}

			if strings.Contains(paramArg, "=") {
				// --param=value format
				parts := strings.SplitN(paramArg, "=", 2)
				if len(parts) == 2 {
					params[parts[0]] = parts[1]
				}
			} else {
				// --param value format (check next argument)
				if i+1 < len(workflowArgs) && !strings.HasPrefix(workflowArgs[i+1], "--") {
					params[paramArg] = workflowArgs[i+1]
					i++ // Skip the next argument since we consumed it
				} else {
					// Boolean flag
					params[paramArg] = "true"
				}
			}
		}
	}

	return params
}

func runStart(cmd *cobra.Command, args []string) error {
	resourceType := args[0]
	resourceName := args[1]

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormat(startOutputFormat),
		Quiet:      startQuiet,
		ConfigPath: startConfigPath,
	})
	if err != nil {
		return err
	}
	defer executor.Close()

	ctx := cmd.Context()
	if err := executor.Connect(ctx); err != nil {
		return err
	}

	if resourceType == "workflow" {
		// Execute workflow using workflow_<workflow-name> pattern
		toolName := fmt.Sprintf("workflow_%s", resourceName)

		// Parse workflow parameters from command line arguments
		workflowParams := parseWorkflowParameters(resourceName)

		return executor.Execute(ctx, toolName, workflowParams)
	}

	// Handle other resource types (services)
	toolName, exists := startResourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: service, workflow", resourceType)
	}

	toolArgs := map[string]interface{}{
		"name": resourceName,
	}

	return executor.Execute(ctx, toolName, toolArgs)
}
