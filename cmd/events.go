package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"muster/internal/cli"
	"muster/internal/config"

	"github.com/spf13/cobra"
)

var (
	eventsOutputFormat string
	eventsQuiet        bool
	eventsConfigPath   string
	eventsResourceType string
	eventsResourceName string
	eventsNamespace    string
	eventsEventType    string
	eventsSince        string
	eventsUntil        string
	eventsLimit        int
	eventsFollow       bool
	eventsEndpoint     string
	eventsContext      string
	eventsAuthMode     string
)

// eventsCmd represents the events command
var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "List events for muster resources",
	Long: `List and filter events for muster resources in both Kubernetes and filesystem modes.

This command provides access to event history for all muster components including
MCPServers, ServiceClasses, Workflows, and Service instances. Events are automatically
generated during resource lifecycle operations and can be queried with various filters.

Filtering Options:
  --resource-type     Filter by resource type (mcpserver, serviceclass, workflow, service)
  --resource-name     Filter by specific resource name
  --namespace         Filter by namespace (default: all namespaces)
  --type              Filter by event type (Normal, Warning)
  --since             Show events after this time (1h, 30m, 2024-01-15T10:00:00Z)
  --until             Show events before this time (2024-01-15T18:00:00Z)
  --limit             Limit number of events returned (default: 50)
  --follow, -f        Stream new events as they occur (follow mode)

Examples:
  # List all recent events
  muster events

  # Filter by resource type
  muster events --resource-type mcpserver
  muster events --resource-type serviceclass

  # Filter by specific resource
  muster events --resource-type mcpserver --resource-name prometheus

  # Filter by namespace
  muster events --namespace default
  muster events --namespace muster-system

  # Filter by time range
  muster events --since 1h
  muster events --since 2024-01-15T10:00:00Z --until 2024-01-15T18:00:00Z

  # Filter by event type
  muster events --type Warning
  muster events --type Normal

  # Combine filters and change output format
  muster events --resource-type mcpserver --namespace default --limit 20 --output json

  # Stream new events (follow mode)
  muster events --follow
  muster events --resource-type mcpserver --follow

  # Stream events with filters
  muster events --namespace default --type Warning --follow

Note: The aggregator server must be running (use 'muster serve') before using this command.`,
	Args:                  cobra.NoArgs,
	DisableFlagsInUseLine: true,
	RunE:                  runEvents,
}

func init() {
	rootCmd.AddCommand(eventsCmd)

	// Add flags to the command
	eventsCmd.PersistentFlags().StringVarP(&eventsOutputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	eventsCmd.PersistentFlags().BoolVarP(&eventsQuiet, "quiet", "q", false, "Suppress non-essential output")
	eventsCmd.PersistentFlags().StringVar(&eventsConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")

	// Filtering flags
	eventsCmd.PersistentFlags().StringVar(&eventsResourceType, "resource-type", "", "Filter by resource type (mcpserver, serviceclass, workflow, service)")
	eventsCmd.PersistentFlags().StringVar(&eventsResourceName, "resource-name", "", "Filter by resource name")
	eventsCmd.PersistentFlags().StringVar(&eventsNamespace, "namespace", "", "Filter by namespace")
	eventsCmd.PersistentFlags().StringVar(&eventsEventType, "type", "", "Filter by event type (Normal, Warning)")
	eventsCmd.PersistentFlags().StringVar(&eventsSince, "since", "", "Show events after this time (e.g., 1h, 30m, 2024-01-15T10:00:00Z)")
	eventsCmd.PersistentFlags().StringVar(&eventsUntil, "until", "", "Show events before this time (e.g., 2024-01-15T18:00:00Z)")
	eventsCmd.PersistentFlags().IntVar(&eventsLimit, "limit", 50, "Limit number of events returned")
	eventsCmd.PersistentFlags().BoolVarP(&eventsFollow, "follow", "f", false, "Stream new events as they occur")

	// Add shell completion for resource types
	eventsCmd.PersistentFlags().SetAnnotation("resource-type", cobra.BashCompCustom, []string{"__muster_events_resource_types"})

	// Auth flags
	eventsCmd.PersistentFlags().StringVar(&eventsEndpoint, "endpoint", cli.GetDefaultEndpoint(), "Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)")
	eventsCmd.PersistentFlags().StringVar(&eventsContext, "context", "", "Use a specific context (env: MUSTER_CONTEXT)")
	eventsCmd.PersistentFlags().StringVar(&eventsAuthMode, "auth", "", "Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)")
}

func runEvents(cmd *cobra.Command, args []string) error {
	// Validate resource type if provided
	if eventsResourceType != "" {
		validTypes := []string{"mcpserver", "serviceclass", "workflow", "service"}
		if !contains(validTypes, strings.ToLower(eventsResourceType)) {
			return fmt.Errorf("invalid resource type '%s'. Valid types: %s", eventsResourceType, strings.Join(validTypes, ", "))
		}
		// Normalize the resource type for consistency
		eventsResourceType = strings.ToLower(eventsResourceType)
		// Convert to the expected CRD Kind format
		switch eventsResourceType {
		case "mcpserver":
			eventsResourceType = "MCPServer"
		case "serviceclass":
			eventsResourceType = "ServiceClass"
		case "workflow":
			eventsResourceType = "Workflow"
		case "service":
			eventsResourceType = "ServiceInstance"
		}
	}

	// Validate and normalize event type if provided
	if eventsEventType != "" {
		switch strings.ToLower(eventsEventType) {
		case "normal":
			eventsEventType = "Normal"
		case "warning":
			eventsEventType = "Warning"
		default:
			return fmt.Errorf("invalid event type '%s'. Valid types: Normal, Warning", eventsEventType)
		}
	}

	// Parse time filters
	var sinceTime, untilTime *time.Time
	if eventsSince != "" {
		since, err := cli.ParseTimeFilter(eventsSince)
		if err != nil {
			return fmt.Errorf("invalid --since value '%s': %w", eventsSince, err)
		}
		sinceTime = &since
	}

	if eventsUntil != "" {
		until, err := cli.ParseTimeFilter(eventsUntil)
		if err != nil {
			return fmt.Errorf("invalid --until value '%s': %w", eventsUntil, err)
		}
		untilTime = &until
	}

	// Validate limit
	if eventsLimit < 0 {
		return fmt.Errorf("limit must be a positive number, got %d", eventsLimit)
	}

	// Parse auth mode (uses environment variable as default if not specified)
	authMode, err := cli.GetAuthModeWithOverride(eventsAuthMode)
	if err != nil {
		return err
	}

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormat(eventsOutputFormat),
		Quiet:      eventsQuiet,
		ConfigPath: eventsConfigPath,
		Endpoint:   eventsEndpoint,
		Context:    eventsContext,
		AuthMode:   authMode,
	})
	if err != nil {
		return err
	}
	defer executor.Close()

	ctx := cmd.Context()
	if err := executor.Connect(ctx); err != nil {
		return err
	}

	// Build tool arguments for the core_events tool
	toolArgs := make(map[string]interface{})

	if eventsResourceType != "" {
		toolArgs["resourceType"] = eventsResourceType
	}
	if eventsResourceName != "" {
		toolArgs["resourceName"] = eventsResourceName
	}
	if eventsNamespace != "" {
		toolArgs["namespace"] = eventsNamespace
	}
	if eventsEventType != "" {
		toolArgs["eventType"] = eventsEventType
	}
	if sinceTime != nil {
		toolArgs["since"] = sinceTime.Format(time.RFC3339)
	}
	if untilTime != nil {
		toolArgs["until"] = untilTime.Format(time.RFC3339)
	}
	if eventsLimit > 0 {
		toolArgs["limit"] = eventsLimit
	}
	// Handle follow mode with notifications
	if eventsFollow {
		toolArgs["follow"] = true
		return followEventsWithNotifications(ctx, executor, toolArgs)
	}

	return executor.Execute(ctx, "core_events", toolArgs)
}

// followEventsWithNotifications implements streaming via MCP notifications for the --follow flag
func followEventsWithNotifications(ctx context.Context, executor *cli.ToolExecutor, baseArgs map[string]interface{}) error {
	fmt.Printf("Streaming events (Press Ctrl+C to stop)...\n\n")

	// Get the agent client to access notification channel
	client := executor.GetClient()
	if client == nil {
		return fmt.Errorf("failed to get MCP client for streaming")
	}

	// Execute the core_events tool with follow=true - this will start the streaming on the server side
	if err := executor.Execute(ctx, "core_events", baseArgs); err != nil {
		return fmt.Errorf("failed to start event streaming: %w", err)
	}

	fmt.Printf("\n--- Following new events ---\n")

	// Listen for event notifications from the server
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("\nStopped following events.\n")
			return nil
		case notification := <-client.NotificationChan:
			// Handle event notifications
			if notification.Method == "events/new_event" {
				// Parse the event from the notification params
				displayStreamedEvent(notification.Params)
			}
		}
	}
}

// displayStreamedEvent formats and displays a single streamed event
func displayStreamedEvent(params interface{}) {
	// For now, just print the notification as-is since we don't have the proper MCP notification mechanism working yet
	fmt.Printf("New event notification: %+v\n", params)
}

// contains checks if a string slice contains a specific string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
