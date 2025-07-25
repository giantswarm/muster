package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"muster/internal/api"
	"muster/internal/client"
	"muster/pkg/logging"
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

	// Use the generator's CRDEvent method which works with object references
	data := EventData{
		Name:      objectRef.Name,
		Namespace: objectRef.Namespace,
	}

	// Map the object reference to a CRD type if possible
	crdType := objectRef.Kind
	switch objectRef.Kind {
	case "MCPServer":
		return a.generator.CRDEvent("MCPServer", objectRef.Name, objectRef.Namespace, EventReason(reason), data)
	case "ServiceClass":
		return a.generator.CRDEvent("ServiceClass", objectRef.Name, objectRef.Namespace, EventReason(reason), data)
	case "Workflow":
		return a.generator.CRDEvent("Workflow", objectRef.Name, objectRef.Namespace, EventReason(reason), data)
	case "ServiceInstance":
		return a.generator.CRDEvent("ServiceInstance", objectRef.Name, objectRef.Namespace, EventReason(reason), data)
	default:
		// For unknown types, use the general CRDEvent method
		return a.generator.CRDEvent(crdType, objectRef.Name, objectRef.Namespace, EventReason(reason), data)
	}
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
		sinceTime, err := parseTimeString(since)
		if err != nil {
			return &api.CallToolResult{
				IsError: true,
				Content: []interface{}{fmt.Sprintf("Invalid 'since' time format: %v", err)},
			}, nil
		}
		options.Since = &sinceTime
	}

	if until, ok := args["until"].(string); ok && until != "" {
		untilTime, err := parseTimeString(until)
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
		events = append(events, eventMap)
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

// parseTimeString parses time strings in various formats.
func parseTimeString(timeStr string) (time.Time, error) {
	// Try duration format first (e.g., "1h", "30m", "2h30m")
	if duration, err := time.ParseDuration(timeStr); err == nil {
		return time.Now().Add(-duration), nil
	}

	// Try RFC3339 format (e.g., "2024-01-15T10:00:00Z")
	if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
		return t, nil
	}

	// Try date-only format (e.g., "2024-01-15")
	if t, err := time.Parse("2006-01-02", timeStr); err == nil {
		return t, nil
	}

	// Try date-time format without timezone (e.g., "2024-01-15 10:00:00")
	if t, err := time.Parse("2006-01-02 15:04:05", timeStr); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("unsupported time format '%s'. Supported formats: duration (1h, 30m), RFC3339 (2024-01-15T10:00:00Z), date (2024-01-15), or datetime (2024-01-15 10:00:00)", timeStr)
}
