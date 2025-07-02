package cmd

import (
	"fmt"
	"io"
	"muster/internal/cli"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	createOutputFormat string
	createQuiet        bool
)

// WorkflowDefinition represents the structure of a workflow YAML file
type WorkflowDefinition struct {
	Name        string                 `yaml:"name"`
	Description string                 `yaml:"description,omitempty"`
	Args        map[string]interface{} `yaml:"args,omitempty"`
	Steps       []interface{}          `yaml:"steps"`
}

// createCmd represents the create command
var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a resource",
	Long: `Create a resource in the muster environment.

Available resource types:
  workflow - Create a new workflow from a definition file

Examples:
  muster create workflow workflows/new-workflow.yaml
  muster create workflow - < workflow.yaml

Note: The aggregator server must be running (use 'muster serve') before using these commands.`,
	Args: cobra.ExactArgs(2),
	RunE: runCreate,
}

// Resource type mappings for create operations
var createResourceMappings = map[string]string{
	"workflow": "core_workflow_create",
}

func init() {
	rootCmd.AddCommand(createCmd)

	// Add flags to the command
	createCmd.PersistentFlags().StringVarP(&createOutputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	createCmd.PersistentFlags().BoolVarP(&createQuiet, "quiet", "q", false, "Suppress non-essential output")
}

func runCreate(cmd *cobra.Command, args []string) error {
	resourceType := args[0]
	resourceFile := args[1]

	// Validate resource type
	toolName, exists := createResourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: workflow", resourceType)
	}

	// Read and parse resource definition
	var toolArgs map[string]interface{}
	var err error

	switch resourceType {
	case "workflow":
		toolArgs, err = parseWorkflowDefinition(resourceFile)
		if err != nil {
			return fmt.Errorf("failed to parse workflow definition: %w", err)
		}
	default:
		// Fallback to raw definition for other resource types
		definition, err := readResourceFile(resourceFile)
		if err != nil {
			return fmt.Errorf("failed to read resource file: %w", err)
		}
		toolArgs = map[string]interface{}{
			"definition": definition,
		}
	}

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format: cli.OutputFormat(createOutputFormat),
		Quiet:  createQuiet,
	})
	if err != nil {
		return err
	}
	defer executor.Close()

	ctx := cmd.Context()
	if err := executor.Connect(ctx); err != nil {
		return err
	}

	return executor.Execute(ctx, toolName, toolArgs)
}

// parseWorkflowDefinition parses a workflow YAML file and converts it to tool arguments
func parseWorkflowDefinition(filename string) (map[string]interface{}, error) {
	// Read the YAML file
	yamlContent, err := readResourceFile(filename)
	if err != nil {
		return nil, err
	}

	// Parse YAML into WorkflowDefinition structure
	var workflowDef WorkflowDefinition
	if err := yaml.Unmarshal([]byte(yamlContent), &workflowDef); err != nil {
		return nil, fmt.Errorf("failed to parse workflow YAML: %w", err)
	}

	// Convert to tool arguments format expected by core_workflow_create
	toolArgs := map[string]interface{}{
		"name":  workflowDef.Name,
		"steps": workflowDef.Steps,
	}

	// Add optional fields if present
	if workflowDef.Description != "" {
		toolArgs["description"] = workflowDef.Description
	}

	if workflowDef.Args != nil {
		toolArgs["args"] = workflowDef.Args
	}

	return toolArgs, nil
}

// readResourceFile reads a resource definition file or from stdin
func readResourceFile(filename string) (string, error) {
	var reader io.Reader

	if filename == "-" {
		reader = os.Stdin
	} else {
		file, err := os.Open(filename)
		if err != nil {
			return "", err
		}
		defer file.Close()
		reader = file
	}

	content, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}

	return string(content), nil
}
