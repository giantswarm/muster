package cmd

import (
	"context"
	"fmt"
	"muster/internal/cli"
	"muster/internal/config"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var (
	createOutputFormat string
	createQuiet        bool
	createConfigPath   string
)

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
		ConfigPath: createConfigPath,
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

	// Add flags to the command
	createCmd.PersistentFlags().StringVarP(&createOutputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	createCmd.PersistentFlags().BoolVarP(&createQuiet, "quiet", "q", false, "Suppress non-essential output")
	createCmd.PersistentFlags().StringVar(&createConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
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

// parseMCPServerParameters extracts MCPServer parameters from raw command line arguments
// This function handles the new flat field structure for stdio, streamable-http, and sse types.
func parseMCPServerParameters(_, mcpServerName string) map[string]interface{} {
	args := map[string]interface{}{
		"name": mcpServerName,
	}

	// Get raw command line arguments from os.Args
	rawArgs := os.Args

	// Parse known flags for MCPServers
	for i, arg := range rawArgs {
		if strings.HasPrefix(arg, "--") {
			// Remove the -- prefix
			flagName := strings.TrimPrefix(arg, "--")

			// Handle flags with values
			if i+1 < len(rawArgs) && !strings.HasPrefix(rawArgs[i+1], "--") {
				flagValue := rawArgs[i+1]

				switch flagName {
				case "type":
					args["type"] = flagValue
				case "autoStart", "auto-start":
					args["autoStart"] = flagValue == "true"
				case "command":
					args["command"] = flagValue
				case "args":
					// Parse comma-separated args
					if flagValue != "" {
						argsList := strings.Split(flagValue, ",")
						for j := range argsList {
							argsList[j] = strings.TrimSpace(argsList[j])
						}
						args["args"] = argsList
					}
				case "url":
					args["url"] = flagValue
				case "timeout":
					if timeout, err := strconv.Atoi(flagValue); err == nil {
						args["timeout"] = timeout
					}
				case "tool-prefix", "toolPrefix":
					args["toolPrefix"] = flagValue
				case "description":
					args["description"] = flagValue
				case "env":
					// Parse key=value format for environment variables
					if strings.Contains(flagValue, "=") {
						parts := strings.SplitN(flagValue, "=", 2)
						if len(parts) == 2 {
							if args["env"] == nil {
								args["env"] = map[string]string{}
							}
							args["env"].(map[string]string)[parts[0]] = parts[1]
						}
					}
				case "header":
					// Parse key=value format for HTTP headers
					if strings.Contains(flagValue, "=") {
						parts := strings.SplitN(flagValue, "=", 2)
						if len(parts) == 2 {
							if args["headers"] == nil {
								args["headers"] = map[string]string{}
							}
							args["headers"].(map[string]string)[parts[0]] = parts[1]
						}
					}
				}
			} else {
				// Boolean flags without values
				switch flagName {
				case "autoStart", "auto-start":
					args["autoStart"] = true
				}
			}
		}
	}

	return args
}

func runCreate(cmd *cobra.Command, args []string) error {
	resourceType := args[0]

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormat(createOutputFormat),
		Quiet:      createQuiet,
		ConfigPath: createConfigPath,
	})
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
		mcpServerArgs := parseMCPServerParameters("", mcpServerName)

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
