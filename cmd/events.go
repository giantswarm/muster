package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/cli"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

var (
	eventsFlags        cli.CommandFlags
	eventsResourceType string
	eventsResourceName string
	eventsNamespace    string
	eventsEventType    string
	eventsSince        string
	eventsUntil        string
	eventsLimit        int
	eventsFollow       bool
)

// eventsCmd represents the events command
var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "List events for muster resources",
	Long: `List and filter events for muster resources in both Kubernetes and filesystem modes.

This command provides access to event history for muster components including
MCPServers and Workflows. Events are automatically generated during resource
lifecycle operations and can be queried with various filters.

Filtering Options:
  --resource-type     Filter by resource type (mcpserver, workflow)
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
  muster events --resource-type workflow

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
	cli.RegisterCommonFlags(eventsCmd, &eventsFlags)

	// Filtering flags
	eventsCmd.PersistentFlags().StringVar(&eventsResourceType, "resource-type", "", "Filter by resource type (mcpserver, workflow)")
	eventsCmd.PersistentFlags().StringVar(&eventsResourceName, "resource-name", "", "Filter by resource name")
	eventsCmd.PersistentFlags().StringVar(&eventsNamespace, "namespace", "", "Filter by namespace")
	eventsCmd.PersistentFlags().StringVar(&eventsEventType, "type", "", "Filter by event type (Normal, Warning)")
	eventsCmd.PersistentFlags().StringVar(&eventsSince, "since", "", "Show events after this time (e.g., 1h, 30m, 2024-01-15T10:00:00Z)")
	eventsCmd.PersistentFlags().StringVar(&eventsUntil, "until", "", "Show events before this time (e.g., 2024-01-15T18:00:00Z)")
	eventsCmd.PersistentFlags().IntVar(&eventsLimit, "limit", 50, "Limit number of events returned")
	eventsCmd.PersistentFlags().BoolVarP(&eventsFollow, "follow", "f", false, "Stream new events as they occur")

	// Add shell completion for resource types
	_ = eventsCmd.PersistentFlags().SetAnnotation("resource-type", cobra.BashCompCustom, []string{"__muster_events_resource_types"})
}

func runEvents(cmd *cobra.Command, args []string) error {
	// Validate resource type if provided
	if eventsResourceType != "" {
		validTypes := []string{api.ResourceTypeMCPServer, api.ResourceTypeWorkflow}
		if !contains(validTypes, strings.ToLower(eventsResourceType)) {
			return fmt.Errorf("invalid resource type '%s'. Valid types: %s", eventsResourceType, strings.Join(validTypes, ", "))
		}
		// Normalize the resource type for consistency
		eventsResourceType = strings.ToLower(eventsResourceType)
		// Convert to the expected CRD Kind format
		switch eventsResourceType {
		case api.ResourceTypeMCPServer:
			eventsResourceType = "MCPServer"
		case api.ResourceTypeWorkflow:
			eventsResourceType = "Workflow"
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

	opts, err := eventsFlags.ToExecutorOptions()
	if err != nil {
		return err
	}
	// Follow mode needs server-initiated notifications, which the streamable-http
	// transport only delivers when a standalone listening stream is open.
	if eventsFollow {
		opts.ContinuousListening = true
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
	// Handle follow mode via server-pushed MCP notifications.
	if eventsFollow {
		return followEvents(ctx, executor, toolArgs, opts.Format)
	}

	return executor.Execute(ctx, "core_events", toolArgs)
}

// eventFollowNotificationMethod is the JSON-RPC notification method the
// aggregator pushes new events on. Must match
// aggregator.eventFollowNotificationMethod.
const eventFollowNotificationMethod = "notifications/muster/event"

// followEvents implements `muster events --follow` using real server push: it
// calls core_events with follow=true (which returns the events seen so far and
// registers a server-side watch for this session), prints those, then prints
// every subsequent event the server pushes as an MCP notification. The server
// sources events from a Kubernetes watch or filesystem fsnotify watch — there
// is no client-side polling.
//
// The output format is honored: `json` emits one JSON object per line
// (newline-delimited JSON), `yaml` emits one YAML document per event, and the
// table/wide formats emit an aligned one-line-per-event human stream.
func followEvents(ctx context.Context, executor *cli.ToolExecutor, baseArgs map[string]interface{}, format cli.OutputFormat) error {
	fmt.Fprintln(os.Stderr, "Streaming events (press Ctrl+C to stop)...")

	// Register the notification handler before starting the follow so events
	// pushed immediately after the initial query are not missed.
	pushed := make(chan map[string]interface{}, 256)
	executor.OnNotification(func(n cli.MCPNotification) {
		if n.Method != eventFollowNotificationMethod {
			return
		}
		select {
		case pushed <- n.Params.AdditionalFields:
		case <-ctx.Done():
		}
	})
	defer executor.OnNotification(nil)

	baseArgs["follow"] = true
	raw, err := executor.ExecuteJSON(ctx, "core_events", baseArgs)
	if err != nil {
		return fmt.Errorf("failed to start event follow: %w", err)
	}
	for _, line := range initialFollowLines(raw, format) {
		fmt.Println(line)
	}

	fmt.Fprintln(os.Stderr, "--- following new events ---")
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "\nStopped following events.")
			return nil
		case ev := <-pushed:
			fmt.Println(formatFollowEvent(ev, format))
		}
	}
}

// initialFollowLines renders the events returned by the initial core_events
// query (newest-first) as display lines in chronological (oldest-first) order.
func initialFollowLines(raw interface{}, format cli.OutputFormat) []string {
	events := toEventMaps(raw)
	lines := make([]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		lines = append(lines, formatFollowEvent(events[i], format))
	}
	return lines
}

// toEventMaps coerces the parsed core_events JSON result into a slice of event
// maps. The tool returns a JSON array of objects; anything else yields nil.
func toEventMaps(raw interface{}) []map[string]interface{} {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	events := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			events = append(events, m)
		}
	}
	return events
}

// stdoutIsTTY reports whether stdout is an interactive terminal. It gates ANSI
// coloring of the human follow stream so piped/redirected output stays clean.
var stdoutIsTTY = term.IsTerminal(int(os.Stdout.Fd()))

// formatFollowEvent renders a single streamed event according to the output
// format: a JSON object (json), a YAML document (yaml), or an aligned human
// line (table/wide). The human line orders fields to match the static table
// (timestamp, type, resource, reason, message) and highlights Warnings on a TTY.
func formatFollowEvent(ev map[string]interface{}, format cli.OutputFormat) string {
	switch format {
	case cli.OutputFormatJSON:
		b, err := json.Marshal(ev)
		if err != nil {
			return fmt.Sprintf("{\"error\":%q}", err.Error())
		}
		return string(b)
	case cli.OutputFormatYAML:
		b, err := yaml.Marshal(ev)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "---\n" + strings.TrimRight(string(b), "\n")
	default:
		return humanFollowLine(ev)
	}
}

// humanFollowLine renders one event as a single aligned text line for the
// table/wide follow stream.
func humanFollowLine(ev map[string]interface{}) string {
	get := func(k string) string {
		if v, ok := ev[k].(string); ok {
			return v
		}
		return ""
	}
	eventType := get("type")
	line := fmt.Sprintf("[%s] %-7s %s %s/%s: %s - %s",
		get("timestamp"), eventType, get("resource_type"), get("namespace"),
		get("resource_name"), get("reason"), get("message"))
	if stdoutIsTTY && eventType == "Warning" {
		return text.FgYellow.Sprint(line)
	}
	return line
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
