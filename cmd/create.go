package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/giantswarm/muster/internal/cli"

	"github.com/spf13/cobra"
)

var createFlags cli.CommandFlags

// Available resource types for create operations
var createResourceTypes = []string{
	"serviceclass",
	"workflow",
	"service",   // Added service creation
	"mcpserver", // Added MCPServer creation
}

// Dynamic completion for ServiceClass names (for service creation)
func createServiceClassNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 || args[0] != "service" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Get ServiceClass names using the same pattern as getResourceNameCompletion
	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormatJSON,
		Quiet:      true,
		ConfigPath: createFlags.ConfigPath,
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

	// Get ServiceClass list
	names, err := getResourceNames(ctx, executor, "core_serviceclass_list", "serviceclass")
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

// createCmd represents the create command
var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a resource",
	Long: `Create a resource in the muster environment.

Available resource types:
  serviceclass  - Create a ServiceClass definition
  workflow      - Create a Workflow definition
  service       - Create a service instance from a ServiceClass
  mcpserver     - Create an MCP server definition (stdio, streamable-http, or sse)

Examples:
  muster create serviceclass example-service
  muster create workflow example-workflow
  muster create service my-service-instance mimir-port-forward --managementCluster=gazelle --localPort=18009
  muster create mcpserver my-stdio-server --type=stdio --command=npx --args="@modelcontextprotocol/server-git" --autoStart=true
  muster create mcpserver my-http-server --type=streamable-http --url=https://api.example.com/mcp --timeout=30
  muster create mcpserver my-sse-server --type=sse --url=https://sse.example.com/mcp --timeout=60

Note: The aggregator server must be running (use 'muster serve') before using these commands.`,
	Args: cobra.MinimumNArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return createResourceTypes, cobra.ShellCompDirectiveNoFileComp
		}
		if len(args) == 1 && args[0] == "service" {
			return createServiceClassNameCompletion(cmd, args, toComplete)
		}
		// MCPServer no longer uses subcommands - name is provided directly
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	DisableFlagsInUseLine: true,
	FParseErrWhitelist: cobra.FParseErrWhitelist{
		UnknownFlags: true, // Allow unknown flags for service creation parameters and MCPServer flags
	},
	RunE: runCreate,
}

// Resource type mappings for create operations
var createResourceMappings = map[string]string{
	"serviceclass": "core_serviceclass_create",
	"workflow":     "core_workflow_create",
	// Note: service creation uses core_service_create, handled separately
}

func init() {
	rootCmd.AddCommand(createCmd)
	cli.RegisterCommonFlags(createCmd, &createFlags)
}

// parseServiceParameters extracts service parameters from raw command line arguments
// Looks for --param=value or --param value patterns after the service class name
func parseServiceParameters(serviceClassName string) map[string]interface{} {
	params := make(map[string]interface{})

	// Find the service class name in os.Args and parse everything after it
	args := os.Args
	serviceClassIndex := -1

	for i, arg := range args {
		if arg == serviceClassName && i > 1 && args[i-2] == "service" {
			serviceClassIndex = i
			break
		}
	}

	if serviceClassIndex == -1 || serviceClassIndex+1 >= len(args) {
		return params
	}

	// Parse arguments after the service class name
	serviceArgs := args[serviceClassIndex+1:]

	for i := 0; i < len(serviceArgs); i++ {
		arg := serviceArgs[i]

		// Handle --param=value format
		if strings.HasPrefix(arg, "--") {
			paramArg := strings.TrimPrefix(arg, "--")

			// Skip known flags
			if paramArg == "output" || paramArg == "quiet" ||
				strings.HasPrefix(paramArg, "output=") ||
				strings.HasPrefix(paramArg, "quiet=") {
				// Skip this and potentially next argument
				if !strings.Contains(paramArg, "=") && i+1 < len(serviceArgs) && !strings.HasPrefix(serviceArgs[i+1], "--") {
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
				if i+1 < len(serviceArgs) && !strings.HasPrefix(serviceArgs[i+1], "--") {
					params[paramArg] = serviceArgs[i+1]
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

// parseMCPServerParameters extracts MCPServer parameters from raw command line arguments.
// This function handles the flat field structure for stdio, streamable-http, and sse types.
// Supports both --flag=value and --flag value formats.
//
// Note: We use os.Args directly instead of cobra flags for the following reasons:
// 1. MCPServer flags are dynamic and vary by type (stdio vs streamable-http vs sse)
// 2. We need to support arbitrary key-value pairs for env and headers
// 3. The create command serves multiple resource types with different flag requirements
// 4. Using UnknownFlags: true with cobra requires manual argument parsing anyway
func parseMCPServerParameters(mcpServerName string) map[string]interface{} {
	result := map[string]interface{}{
		"name": mcpServerName,
	}

	rawArgs := os.Args

	for i := 0; i < len(rawArgs); i++ {
		arg := rawArgs[i]
		if !strings.HasPrefix(arg, "--") {
			continue
		}

		// Remove the -- prefix
		flagPart := strings.TrimPrefix(arg, "--")

		var flagName, flagValue string
		hasValue := false

		// Handle --flag=value format
		if idx := strings.Index(flagPart, "="); idx != -1 {
			flagName = flagPart[:idx]
			flagValue = flagPart[idx+1:]
			hasValue = true
		} else {
			flagName = flagPart
			// Handle --flag value format
			if i+1 < len(rawArgs) && !strings.HasPrefix(rawArgs[i+1], "--") {
				flagValue = rawArgs[i+1]
				hasValue = true
				i++ // Skip the next argument since we consumed it
			}
		}

		// Process the flag
		processMCPServerFlag(result, flagName, flagValue, hasValue)
	}

	return result
}

// getOrCreateStringMap retrieves an existing map[string]string from args, or creates one if it doesn't exist.
// This helper prevents nil map panics when adding key-value pairs to env or headers.
func getOrCreateStringMap(args map[string]interface{}, key string) map[string]string {
	if args[key] == nil {
		args[key] = make(map[string]string)
	}
	if m, ok := args[key].(map[string]string); ok {
		return m
	}
	// Fallback: create new map if type assertion fails (shouldn't happen in practice)
	args[key] = make(map[string]string)
	return args[key].(map[string]string)
}

// processMCPServerFlag handles individual flag processing for MCPServer parameters
func processMCPServerFlag(args map[string]interface{}, flagName, flagValue string, hasValue bool) {
	switch flagName {
	case "type":
		if hasValue {
			args["type"] = flagValue
		}
	case "autoStart", "auto-start":
		if hasValue {
			args["autoStart"] = flagValue == "true"
		} else {
			args["autoStart"] = true
		}
	case "command":
		if hasValue {
			args["command"] = flagValue
		}
	case "args":
		if hasValue && flagValue != "" {
			argsList := strings.Split(flagValue, ",")
			for j := range argsList {
				argsList[j] = strings.TrimSpace(argsList[j])
			}
			args["args"] = argsList
		}
	case "url":
		if hasValue {
			args["url"] = flagValue
		}
	case "timeout":
		if hasValue {
			if timeout, err := strconv.Atoi(flagValue); err == nil {
				args["timeout"] = timeout
			}
		}
	case "tool-prefix", "toolPrefix":
		if hasValue {
			args["toolPrefix"] = flagValue
		}
	case "description":
		if hasValue {
			args["description"] = flagValue
		}
	case "env":
		if hasValue && strings.Contains(flagValue, "=") {
			parts := strings.SplitN(flagValue, "=", 2)
			if len(parts) == 2 {
				envMap := getOrCreateStringMap(args, "env")
				envMap[parts[0]] = parts[1]
			}
		}
	case "header":
		if hasValue && strings.Contains(flagValue, "=") {
			parts := strings.SplitN(flagValue, "=", 2)
			if len(parts) == 2 {
				headersMap := getOrCreateStringMap(args, "headers")
				headersMap[parts[0]] = parts[1]
			}
		}
	}
}

func runCreate(cmd *cobra.Command, args []string) error {
	resourceType := args[0]

	opts, err := createFlags.ToExecutorOptions()
	if err != nil {
		return err
	}

	executor, err := cli.NewToolExecutor(opts)
	if err != nil {
		return err
	}
	defer executor.Close()

	ctx := cmd.Context()
	if err := executor.Connect(ctx); err != nil {
		return err
	}

	if resourceType == "service" {
		// Handle service creation: muster create service <instance-name> <serviceclass-name> --arg1=value1 --arg2=value2
		if len(args) < 3 {
			return fmt.Errorf("service creation requires: muster create service <instance-name> <serviceclass-name> [--arg=value...]")
		}

		instanceName := args[1]
		serviceClassName := args[2]

		// Parse service parameters from command line arguments
		serviceParams := parseServiceParameters(serviceClassName)

		// Create the service instance
		toolArgs := map[string]interface{}{
			"name":             instanceName,
			"serviceClassName": serviceClassName,
			"args":             serviceParams,
		}

		return executor.Execute(ctx, "core_service_create", toolArgs)
	}

	if resourceType == "mcpserver" {
		// Handle MCPServer creation: muster create mcpserver <name> --type <type> [options]
		if len(args) < 2 {
			return fmt.Errorf("MCPServer creation requires: muster create mcpserver <name> --type <type> [options]")
		}

		mcpServerName := args[1]

		// Parse MCPServer-specific parameters from command line arguments
		mcpServerArgs := parseMCPServerParameters(mcpServerName)

		return executor.Execute(ctx, "core_mcpserver_create", mcpServerArgs)
	}

	// Handle other resource types (serviceclass, workflow)
	if len(args) < 2 {
		return fmt.Errorf("resource name is required")
	}

	resourceName := args[1]
	toolName, exists := createResourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: serviceclass, workflow, service, mcpserver", resourceType)
	}

	toolArgs := map[string]interface{}{
		"name": resourceName,
	}

	return executor.Execute(ctx, toolName, toolArgs)
}
