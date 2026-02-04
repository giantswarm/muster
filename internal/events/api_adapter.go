package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/cli"
	"github.com/giantswarm/muster/internal/client"
	"github.com/giantswarm/muster/pkg/logging"
)

// Adapter implements the EventManagerHandler interface using the unified MusterClient.
// It bridges the events package functionality with the API service locator pattern.
// It also implements the ToolProvider interface to expose event querying capabilities
// through the aggregator.
type Adapter struct {
	generator *EventGenerator
}

// NewAdapter creates a new events adapter using the provided MusterClient.
func NewAdapter(musterClient client.MusterClient) *Adapter {
	return &Adapter{
		generator: NewEventGenerator(musterClient),
	}
}

// Register registers this adapter with the API service locator.
// This method follows the standard pattern used by all service adapters.
func (a *Adapter) Register() {
	api.RegisterEventManager(a)
	logging.Debug("events", "Event manager adapter registered with API")
}

// CreateEvent creates an event for a specific object reference.
// Implements EventManagerHandler.CreateEvent.
func (a *Adapter) CreateEvent(ctx context.Context, objectRef api.ObjectReference, reason, message, eventType string) error {
	logging.Debug("events", "Creating event for %s %s/%s: %s - %s (%s)",
		objectRef.Kind, objectRef.Namespace, objectRef.Name, reason, message, eventType)

	data := EventData{
		Name:      objectRef.Name,
		Namespace: objectRef.Namespace,
	}

	return a.generator.CRDEvent(objectRef.Kind, objectRef.Name, objectRef.Namespace, EventReason(reason), data)
}

// CreateEventForCRD creates an event for a CRD by type, name, and namespace.
// Implements EventManagerHandler.CreateEventForCRD.
func (a *Adapter) CreateEventForCRD(ctx context.Context, crdType, name, namespace, reason, message, eventType string) error {
	logging.Debug("events", "Creating CRD event for %s %s/%s: %s - %s (%s)",
		crdType, namespace, name, reason, message, eventType)

	data := EventData{
		Name:      name,
		Namespace: namespace,
	}

	return a.generator.CRDEvent(crdType, name, namespace, EventReason(reason), data)
}

// QueryEvents retrieves events based on filtering options.
// Implements EventManagerHandler.QueryEvents.
func (a *Adapter) QueryEvents(ctx context.Context, options api.EventQueryOptions) (*api.EventQueryResult, error) {
	logging.Debug("events", "Querying events with options: resourceType=%s, resourceName=%s, namespace=%s, eventType=%s, limit=%d",
		options.ResourceType, options.ResourceName, options.Namespace, options.EventType, options.Limit)

	// Delegate to the underlying MusterClient
	result, err := a.generator.client.QueryEvents(ctx, options)
	if err != nil {
		logging.Error("events", err, "Failed to query events")
		return nil, err
	}

	logging.Debug("events", "Retrieved %d events (total: %d)", len(result.Events), result.TotalCount)
	return result, nil
}

// IsKubernetesMode returns true if the event manager is using Kubernetes mode.
// Implements EventManagerHandler.IsKubernetesMode.
func (a *Adapter) IsKubernetesMode() bool {
	return a.generator.IsKubernetesMode()
}

// GetGenerator returns the underlying EventGenerator for advanced usage scenarios.
// This method is not part of the EventManagerHandler interface but provides
// access to advanced event generation features when needed.
func (a *Adapter) GetGenerator() *EventGenerator {
	return a.generator
}

// ToolProvider implementation

// GetTools returns metadata for all tools this provider offers.
// Implements api.ToolProvider.GetTools.
func (a *Adapter) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		{
			Name:        "events",
			Description: "List and filter events for muster resources",
			Args: []api.ArgMetadata{
				{
					Name:        "resourceType",
					Type:        "string",
					Required:    false,
					Description: "Filter by resource type (MCPServer, ServiceClass, Workflow, ServiceInstance)",
				},
				{
					Name:        "resourceName",
					Type:        "string",
					Required:    false,
					Description: "Filter by resource name",
				},
				{
					Name:        "namespace",
					Type:        "string",
					Required:    false,
					Description: "Filter by namespace",
				},
				{
					Name:        "eventType",
					Type:        "string",
					Required:    false,
					Description: "Filter by event type (Normal, Warning)",
				},
				{
					Name:        "since",
					Type:        "string",
					Required:    false,
					Description: "Show events after this time (duration like '1h' or RFC3339 timestamp)",
				},
				{
					Name:        "until",
					Type:        "string",
					Required:    false,
					Description: "Show events before this time (RFC3339 timestamp)",
				},
				{
					Name:        "limit",
					Type:        "number",
					Required:    false,
					Description: "Maximum number of events to return",
					Default:     50,
				},
				{
					Name:        "follow",
					Type:        "boolean",
					Required:    false,
					Description: "Stream new events as they occur (follow mode)",
					Default:     false,
				},
			},
		},
	}
}

// ExecuteTool executes a tool by name.
// Implements api.ToolProvider.ExecuteTool.
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
	case "events":
		return a.handleEventsQuery(ctx, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

// formatEventForDisplay converts an EventResult to a map suitable for CLI table display.
func formatEventForDisplay(event api.EventResult) map[string]interface{} {
	eventMap := map[string]interface{}{
		"timestamp":     event.Timestamp.Format("2006-01-02 15:04:05"),
		"resource_type": event.InvolvedObject.Kind,
		"resource_name": event.InvolvedObject.Name,
		"namespace":     event.Namespace,
		"reason":        event.Reason,
		"message":       event.Message,
		"type":          event.Type,
	}
	// Only include count if it's greater than 1 (useful for Kubernetes mode)
	if event.Count > 1 {
		eventMap["count"] = event.Count
	}
	return eventMap
}

// handleEventsQuery handles the events query tool execution.
func (a *Adapter) handleEventsQuery(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	// Build query options from arguments
	options := api.EventQueryOptions{}

	if resourceType, ok := args["resourceType"].(string); ok && resourceType != "" {
		options.ResourceType = resourceType
	}

	if resourceName, ok := args["resourceName"].(string); ok && resourceName != "" {
		options.ResourceName = resourceName
	}

	if namespace, ok := args["namespace"].(string); ok && namespace != "" {
		options.Namespace = namespace
	}

	if eventType, ok := args["eventType"].(string); ok && eventType != "" {
		options.EventType = eventType
	}

	if since, ok := args["since"].(string); ok && since != "" {
		sinceTime, err := cli.ParseTimeFilter(since)
		if err != nil {
			return &api.CallToolResult{
				IsError: true,
				Content: []interface{}{fmt.Sprintf("Invalid 'since' time format: %v", err)},
			}, nil
		}
		options.Since = &sinceTime
	}

	if until, ok := args["until"].(string); ok && until != "" {
		untilTime, err := cli.ParseTimeFilter(until)
		if err != nil {
			return &api.CallToolResult{
				IsError: true,
				Content: []interface{}{fmt.Sprintf("Invalid 'until' time format: %v", err)},
			}, nil
		}
		options.Until = &untilTime
	}

	if limit, ok := args["limit"].(float64); ok && limit > 0 {
		options.Limit = int(limit)
	} else if limit, ok := args["limit"].(int); ok && limit > 0 {
		options.Limit = limit
	}

	// Check for follow mode
	if follow, ok := args["follow"].(bool); ok && follow {
		return a.handleEventsStreaming(ctx, options)
	}

	// Execute the query
	result, err := a.QueryEvents(ctx, options)
	if err != nil {
		return &api.CallToolResult{
			IsError: true,
			Content: []interface{}{fmt.Sprintf("Failed to query events: %v", err)},
		}, nil
	}

	// Convert events to a format suitable for CLI table display
	var events []interface{}
	for _, event := range result.Events {
		events = append(events, formatEventForDisplay(event))
	}

	logging.Debug("events", "Formatted %d events for CLI display", len(events))

	// Return the events array directly for proper table formatting
	// If no events, return empty array instead of nil to ensure consistent array type
	if len(events) == 0 {
		events = []interface{}{}
	}

	// Marshal events to JSON string for CLI consumption
	eventsJSON, err := json.Marshal(events)
	if err != nil {
		return &api.CallToolResult{
			IsError: true,
			Content: []interface{}{fmt.Sprintf("Failed to marshal events to JSON: %v", err)},
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{string(eventsJSON)},
	}, nil
}

// handleEventsStreaming handles follow mode for event streaming
func (a *Adapter) handleEventsStreaming(ctx context.Context, options api.EventQueryOptions) (*api.CallToolResult, error) {
	// Get initial events first
	result, err := a.QueryEvents(ctx, options)
	if err != nil {
		return &api.CallToolResult{
			IsError: true,
			Content: []interface{}{fmt.Sprintf("Failed to query initial events: %v", err)},
		}, nil
	}

	// Format initial events for display
	var initialEvents []interface{}
	for _, event := range result.Events {
		initialEvents = append(initialEvents, formatEventForDisplay(event))
	}

	// Start background streaming
	go a.streamEventsInBackground(ctx, options)

	// Return initial events immediately
	initialEventsJSON, err := json.Marshal(initialEvents)
	if err != nil {
		return &api.CallToolResult{
			IsError: true,
			Content: []interface{}{fmt.Sprintf("Failed to marshal initial events: %v", err)},
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{string(initialEventsJSON)},
	}, nil
}

// streamEventsInBackground monitors for new events and sends notifications
func (a *Adapter) streamEventsInBackground(ctx context.Context, options api.EventQueryOptions) {
	// Track the last seen timestamp to avoid duplicates
	var lastSeenTime time.Time
	if options.Since != nil {
		lastSeenTime = *options.Since
	} else {
		lastSeenTime = time.Now()
	}

	// Check for new events every 2 seconds
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	logging.Debug("events", "Started background event streaming")

	for {
		select {
		case <-ctx.Done():
			logging.Debug("events", "Stopped background event streaming")
			return
		case <-ticker.C:
			// Create options for querying new events since lastSeenTime
			newOptions := options
			newOptions.Since = &lastSeenTime
			newOptions.Until = nil // Remove until filter for streaming

			// Query for new events
			result, err := a.QueryEvents(ctx, newOptions)
			if err != nil {
				logging.Debug("events", "Error querying new events: %v", err)
				continue
			}

			// Process new events
			for _, event := range result.Events {
				if event.Timestamp.After(lastSeenTime) {
					// Format event for notification using shared helper
					_ = formatEventForDisplay(event)

					// Log the new event (MCP notification support is not yet implemented)
					logging.Info("events", "New event: [%s] %s %s/%s: %s - %s (%s)",
						event.Timestamp.Format("2006-01-02 15:04:05"),
						event.InvolvedObject.Kind,
						event.Namespace,
						event.InvolvedObject.Name,
						event.Reason,
						event.Message,
						event.Type)

					lastSeenTime = event.Timestamp
				}
			}
		}
	}
}
