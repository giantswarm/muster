package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/cli"

	"github.com/spf13/cobra"
)

var startFlags cli.CommandFlags

// Available resource types for start operations
var startResourceTypes = []string{
	api.ResourceTypeService,
	api.ResourceTypeWorkflow,
}

// Dynamic completion for service names
func startServiceNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 || args[0] != api.ResourceTypeService {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Reuse the completion logic from get.go
	return getResourceNameCompletion(cmd, args, toComplete)
}

// Dynamic completion for workflow names
func startWorkflowNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 || args[0] != api.ResourceTypeWorkflow {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Get workflow names using the same pattern as getResourceNameCompletion
	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormatJSON,
		Quiet:      true,
		ConfigPath: startFlags.ConfigPath,
	})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx := context.Background()
	err = executor.Connect(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = executor.Close() }()

	// Get workflow list
	names, err := getResourceNames(ctx, executor, "core_workflow_list", api.ResourceTypeWorkflow)
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
	Use:   "start <type> <name> [--arg=value ...]",
	Short: "Start a resource",
	Long: `Start a resource in the muster environment.

Available resource types:
  service   - Start a service by its name
  workflow  - Execute a workflow with optional parameters

Workflow arguments are passed as --name=value or --name value flags. The flags
muster itself declares (--endpoint, --auth, --output and the others listed
below) are never passed on. To pass a workflow argument that shares a name with
one of them, put it after "--": everything after the separator is an argument.

Examples:
  muster start service prometheus
  muster start service vault
  muster start workflow deploy-app --environment=production --replicas=3
  muster start workflow auth-setup --cluster=test
  muster start workflow sync --endpoint http://muster:8090/mcp -- --endpoint=https://target

Note: The aggregator server must be running (use 'muster serve') before using these commands.`,
	Args: cobra.MinimumNArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return startResourceTypes, cobra.ShellCompDirectiveNoFileComp
		}
		if len(args) == 1 {
			if args[0] == api.ResourceTypeService {
				return startServiceNameCompletion(cmd, args, toComplete)
			}
			if args[0] == api.ResourceTypeWorkflow {
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
	api.ResourceTypeService: "core_service_start",
	// Note: workflows use workflow_<workflow-name> pattern, handled separately
}

func init() {
	rootCmd.AddCommand(startCmd)
	cli.RegisterCommonFlags(startCmd, &startFlags)
}

func runStart(cmd *cobra.Command, args []string) error {
	resourceType := args[0]
	resourceName := args[1]

	opts, err := startFlags.ToExecutorOptions()
	if err != nil {
		return err
	}

	executor, err := cli.NewToolExecutor(opts)
	if err != nil {
		return err
	}
	defer func() { _ = executor.Close() }()

	ctx := cmd.Context()
	if err := executor.Connect(ctx); err != nil {
		return err
	}

	if resourceType == api.ResourceTypeWorkflow {
		// Execute workflow using workflow_<workflow-name> pattern. Its
		// arguments are the flags start does not declare: cobra dropped
		// them, the command line still has them.
		toolName := fmt.Sprintf("workflow_%s", resourceName)
		return executor.Execute(ctx, toolName, parseDynamicArgs(cmd, os.Args[1:], stringValue))
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
