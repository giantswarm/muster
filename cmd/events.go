package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/cli"

	"github.com/spf13/cobra"
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
	// Handle follow mode via client-side polling.
	if eventsFollow {
		return followEvents(ctx, executor, toolArgs)
	}

	return executor.Execute(ctx, "core_events", toolArgs)
}

// followEventPollInterval is how often --follow polls core_events for new events.
const followEventPollInterval = 2 * time.Second

// seenEventCap bounds the dedup set during a follow session.
// ponytail: a burst of more than this many new events between two polls could
// in theory reprint the oldest few; acceptable for an interactive stream and
// far cheaper than parsing/keying on exact timestamps the display format drops.
const seenEventCap = 4096

// followEvents implements `muster events --follow` by polling the core_events
// tool on an interval and printing only events it has not shown yet. This
// replaces the never-implemented MCP server-push notification path: there is no
// server-side streaming, so the client simply re-queries and de-duplicates.
func followEvents(ctx context.Context, executor *cli.ToolExecutor, baseArgs map[string]interface{}) error {
	fmt.Fprintln(os.Stderr, "Streaming events (press Ctrl+C to stop)...")

	tracker := newFollowTracker()
	first := true
	for {
		raw, err := executor.ExecuteJSON(ctx, "core_events", baseArgs)
		if err != nil {
			return fmt.Errorf("failed to query events: %w", err)
		}

		for _, line := range tracker.newLines(raw) {
			fmt.Println(line)
		}

		if first {
			fmt.Fprintln(os.Stderr, "--- following new events ---")
			first = false
		}

		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "\nStopped following events.")
			return nil
		case <-time.After(followEventPollInterval):
		}
	}
}

// followTracker de-duplicates events across polls and renders the unseen ones
// as display lines in chronological order.
type followTracker struct {
	seen      map[string]struct{}
	seenOrder []string
}

func newFollowTracker() *followTracker {
	return &followTracker{
		seen:      make(map[string]struct{}, seenEventCap),
		seenOrder: make([]string, 0, seenEventCap),
	}
}

func (t *followTracker) markSeen(key string) {
	t.seen[key] = struct{}{}
	t.seenOrder = append(t.seenOrder, key)
	if len(t.seenOrder) > seenEventCap {
		oldest := t.seenOrder[0]
		t.seenOrder = t.seenOrder[1:]
		delete(t.seen, oldest)
	}
}

// newLines returns formatted lines for events in raw that have not been seen
// before. core_events returns events newest-first, so the slice is walked in
// reverse to produce a natural, chronological stream.
func (t *followTracker) newLines(raw interface{}) []string {
	events := toEventMaps(raw)
	var lines []string
	for i := len(events) - 1; i >= 0; i-- {
		key := eventKey(events[i])
		if _, ok := t.seen[key]; ok {
			continue
		}
		t.markSeen(key)
		lines = append(lines, formatFollowEvent(events[i]))
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

// eventKey builds a stable identity for an event from its display fields so the
// follow loop can de-duplicate across polls.
func eventKey(ev map[string]interface{}) string {
	fields := []string{"timestamp", "resource_type", "resource_name", "namespace", "reason", "message", "type"}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		if v, ok := ev[f].(string); ok {
			parts = append(parts, v)
		} else {
			parts = append(parts, "")
		}
	}
	return strings.Join(parts, "\x1f")
}

// formatFollowEvent renders a single event as one line for the follow stream.
func formatFollowEvent(ev map[string]interface{}) string {
	get := func(k string) string {
		if v, ok := ev[k].(string); ok {
			return v
		}
		return ""
	}
	return fmt.Sprintf("[%s] %s %s/%s: %s - %s (%s)",
		get("timestamp"), get("resource_type"), get("namespace"),
		get("resource_name"), get("reason"), get("message"), get("type"))
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
