package filesystem

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// CreateEvent logs an event for the given object in filesystem mode.
func (f *Client) CreateEvent(ctx context.Context, obj client.Object, reason, message, eventType string) error {
	logging.Info("event", "Event for %s/%s: %s - %s (%s)",
		obj.GetNamespace(), obj.GetName(), reason, message, eventType)

	return f.writeEventToFile(obj.GetNamespace(), obj.GetName(), obj.GetObjectKind().GroupVersionKind().Kind, reason, message, eventType)
}

// CreateEventForCRD logs an event for a CRD by type, name, and namespace in filesystem mode.
func (f *Client) CreateEventForCRD(ctx context.Context, crdType, name, namespace, reason, message, eventType string) error {
	logging.Info("event", "Event for %s %s/%s: %s - %s (%s)",
		crdType, namespace, name, reason, message, eventType)

	return f.writeEventToFile(namespace, name, crdType, reason, message, eventType)
}

// QueryEvents retrieves events based on filtering options from filesystem storage.
func (f *Client) QueryEvents(ctx context.Context, options api.EventQueryOptions) (*api.EventQueryResult, error) {
	eventsDir := filepath.Join(f.basePath, "events")

	if err := os.MkdirAll(eventsDir, 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("failed to create events directory: %w", err)
	}

	var allEvents []api.EventResult

	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read events directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "events-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(eventsDir, entry.Name())
		fileEvents, err := f.readEventsFromFile(filePath)
		if err != nil {
			logging.Debug("fs-client", "Failed to read events from %s: %v", filePath, err)
			continue
		}

		allEvents = append(allEvents, fileEvents...)
	}

	legacyEvents, err := f.readLegacyEventsLog(eventsDir)
	if err == nil {
		allEvents = append(allEvents, legacyEvents...)
	}

	filteredEvents := f.filterEvents(allEvents, options)

	sort.Slice(filteredEvents, func(i, j int) bool {
		return filteredEvents[i].Timestamp.After(filteredEvents[j].Timestamp)
	})

	totalCount := len(filteredEvents)

	initialEvents := filteredEvents
	if options.Limit > 0 && len(filteredEvents) > options.Limit {
		initialEvents = filteredEvents[:options.Limit]
	}

	return &api.EventQueryResult{
		Events:     initialEvents,
		TotalCount: totalCount,
	}, nil
}

// readEventsFromFile reads events from a daily JSON file (one JSON event per line).
func (f *Client) readEventsFromFile(filePath string) ([]api.EventResult, error) {
	data, err := os.ReadFile(filePath) //nolint:gosec
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return []api.EventResult{}, nil
	}

	var events []api.EventResult
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event api.EventResult
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			logging.Debug("fs-client", "Failed to parse event JSON: %v", err)
			continue
		}
		events = append(events, event)
	}

	return events, nil
}

// readLegacyEventsLog reads events from the legacy events.log file.
func (f *Client) readLegacyEventsLog(eventsDir string) ([]api.EventResult, error) {
	legacyFile := filepath.Join(eventsDir, "events.log")
	data, err := os.ReadFile(legacyFile) //nolint:gosec
	if err != nil {
		return nil, err
	}

	var events []api.EventResult
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		event, err := f.parseLegacyEventLine(line)
		if err != nil {
			continue
		}
		events = append(events, event)
	}

	return events, nil
}

// parseLegacyEventLine parses a line from the legacy events.log format:
//
//	[2024-01-15T14:30:00Z] MCPServer default/prometheus: MCPServerStarted - MCPServer prometheus started successfully (Normal)
func (f *Client) parseLegacyEventLine(line string) (api.EventResult, error) {
	if !strings.HasPrefix(line, "[") {
		return api.EventResult{}, fmt.Errorf("invalid format")
	}

	parts := strings.SplitN(line, "] ", 2)
	if len(parts) != 2 {
		return api.EventResult{}, fmt.Errorf("invalid timestamp format")
	}

	timestampStr := parts[0][1:]
	timestamp, err := time.Parse(time.RFC3339, timestampStr)
	if err != nil {
		return api.EventResult{}, fmt.Errorf("invalid timestamp: %w", err)
	}

	remaining := parts[1]

	objectParts := strings.SplitN(remaining, ": ", 2)
	if len(objectParts) != 2 {
		return api.EventResult{}, fmt.Errorf("invalid object format")
	}

	objectInfo := strings.SplitN(objectParts[0], " ", 2)
	if len(objectInfo) != 2 {
		return api.EventResult{}, fmt.Errorf("invalid object info")
	}

	kind := objectInfo[0]
	namespaceAndName := strings.SplitN(objectInfo[1], "/", 2)
	if len(namespaceAndName) != 2 {
		return api.EventResult{}, fmt.Errorf("invalid namespace/name format")
	}

	namespace := namespaceAndName[0]
	name := namespaceAndName[1]

	eventDetails := objectParts[1]

	var eventType string
	if strings.HasSuffix(eventDetails, ")") {
		lastParen := strings.LastIndex(eventDetails, "(")
		if lastParen > 0 {
			eventType = eventDetails[lastParen+1 : len(eventDetails)-1]
			eventDetails = strings.TrimSpace(eventDetails[:lastParen])
		}
	}

	reasonAndMessage := strings.SplitN(eventDetails, " - ", 2)
	if len(reasonAndMessage) != 2 {
		return api.EventResult{}, fmt.Errorf("invalid reason/message format")
	}

	reason := reasonAndMessage[0]
	message := reasonAndMessage[1]

	return api.EventResult{
		Timestamp: timestamp,
		Namespace: namespace,
		InvolvedObject: api.ObjectReference{
			Kind:      kind,
			Name:      name,
			Namespace: namespace,
		},
		Reason:  reason,
		Message: message,
		Type:    eventType,
		Source:  "muster",
	}, nil
}

// filterEvents applies the given options to filter events.
func (f *Client) filterEvents(events []api.EventResult, options api.EventQueryOptions) []api.EventResult {
	var filtered []api.EventResult

	for _, event := range events {
		if options.ResourceType != "" && event.InvolvedObject.Kind != options.ResourceType {
			continue
		}
		if options.ResourceName != "" && event.InvolvedObject.Name != options.ResourceName {
			continue
		}
		if options.Namespace != "" && event.Namespace != options.Namespace {
			continue
		}
		if options.EventType != "" && event.Type != options.EventType {
			continue
		}
		if options.Since != nil && event.Timestamp.Before(*options.Since) {
			continue
		}
		if options.Until != nil && event.Timestamp.After(*options.Until) {
			continue
		}

		filtered = append(filtered, event)
	}

	return filtered
}

// writeEventToFile writes event information to both legacy and JSON formats.
func (f *Client) writeEventToFile(namespace, name, kind, reason, message, eventType string) error {
	eventsDir := filepath.Join(f.basePath, "events")
	if err := os.MkdirAll(eventsDir, 0755); err != nil { //nolint:gosec
		logging.Debug("fs-client", "Failed to create events directory: %v", err)
		return nil
	}

	timestamp := time.Now()

	if err := f.writeLegacyEvent(eventsDir, timestamp, namespace, name, kind, reason, message, eventType); err != nil {
		logging.Debug("fs-client", "Failed to write legacy event: %v", err)
	}

	if err := f.writeJSONEvent(eventsDir, timestamp, namespace, name, kind, reason, message, eventType); err != nil {
		logging.Debug("fs-client", "Failed to write JSON event: %v", err)
	}

	f.cleanupOldEventFiles(eventsDir)

	return nil
}

// writeLegacyEvent appends a line to the legacy events.log format.
func (f *Client) writeLegacyEvent(eventsDir string, timestamp time.Time, namespace, name, kind, reason, message, eventType string) error {
	eventsFile := filepath.Join(eventsDir, "events.log")
	eventLine := fmt.Sprintf("[%s] %s %s/%s: %s - %s (%s)\n",
		timestamp.Format(time.RFC3339), kind, namespace, name, reason, message, eventType)

	file, err := os.OpenFile(eventsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	_, err = file.WriteString(eventLine)
	return err
}

// writeJSONEvent appends a JSON-per-line entry to the daily events file.
func (f *Client) writeJSONEvent(eventsDir string, timestamp time.Time, namespace, name, kind, reason, message, eventType string) error {
	dateStr := timestamp.Format("2006-01-02")
	jsonFile := filepath.Join(eventsDir, fmt.Sprintf("events-%s.json", dateStr))

	event := api.EventResult{
		Timestamp: timestamp,
		Namespace: namespace,
		InvolvedObject: api.ObjectReference{
			Kind:      kind,
			Name:      name,
			Namespace: namespace,
		},
		Reason:  reason,
		Message: message,
		Type:    eventType,
		Source:  "muster",
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(jsonFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	_, err = file.WriteString(string(eventJSON) + "\n")
	return err
}

// cleanupOldEventFiles removes event files older than 30 days.
func (f *Client) cleanupOldEventFiles(eventsDir string) {
	cutoffDate := time.Now().AddDate(0, 0, -30)
	cutoffDateStr := cutoffDate.Format("2006-01-02")

	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "events-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Filename format is events-YYYY-MM-DD.json (21 chars total).
		filename := entry.Name()
		if len(filename) < 21 {
			continue
		}

		dateStr := filename[7:17]
		if dateStr < cutoffDateStr {
			filePath := filepath.Join(eventsDir, filename)
			if err := os.Remove(filePath); err != nil {
				logging.Debug("fs-client", "Failed to remove old event file %s: %v", filePath, err)
			} else {
				logging.Debug("fs-client", "Removed old event file: %s", filename)
			}
		}
	}
}
