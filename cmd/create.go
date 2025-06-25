package cmd

import (
	"fmt"
	"io"
	"muster/internal/cli"
	"os"

	"github.com/spf13/cobra"
)

var (
	createOutputFormat string
	createQuiet        bool
)

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

	// Read resource definition
	definition, err := readResourceFile(resourceFile)
	if err != nil {
		return fmt.Errorf("failed to read resource file: %w", err)
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

	arguments := map[string]interface{}{
		"definition": definition,
	}

	return executor.Execute(ctx, toolName, arguments)
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
