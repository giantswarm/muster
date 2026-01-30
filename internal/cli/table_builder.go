package cli

import (
	"fmt"
	"sort"
	"strings"
)

// TableBuilder handles cell formatting and styling for table display.
// It provides specialized formatting for different types of data commonly
// encountered in muster operations, including status indicators, metadata,
// and resource-specific information. The builder applies consistent styling
// and iconography to create professional-looking table output.
type TableBuilder struct{}

// NewTableBuilder creates a new table builder instance.
// The builder is stateless and can be reused for multiple formatting operations.
//
// Returns:
//   - *TableBuilder: New table builder ready for use
func NewTableBuilder() *TableBuilder {
	return &TableBuilder{}
}

// FormatCellValuePlain formats individual cell values as plain text without ANSI styling.
// This is used for kubectl-style table output where we don't want color codes.
//
// Args:
//   - column: The column name/type to determine formatting rules
//   - value: The raw value to format
//   - rowContext: The full row data for context-aware formatting (may be nil)
//
// Returns:
//   - string: Formatted value as plain text
func (b *TableBuilder) FormatCellValuePlain(column string, value interface{}, rowContext map[string]interface{}) string {
	if value == nil {
		return "-"
	}

	strValue := fmt.Sprintf("%v", value)
	colLower := strings.ToLower(column)

	switch colLower {
	case "name", "label", "id", "workflow", "execution_id", "workflow_name", "resource_name":
		return strValue
	case "health", "status":
		// Return "-" for empty health/status values
		if strValue == "" {
			return "-"
		}
		return strValue
	case "available":
		return b.formatAvailableStatusPlain(value)
	case "autostart":
		return b.formatAutoStartStatusPlain(value)
	case "state":
		// State represents infrastructure state for MCPServers and services
		// MCPServer values are context-appropriate: Running/Connected/Starting/Connecting/Stopped/Disconnected/Failed
		serverType := b.getServerTypeFromContext(rowContext)
		if serverType != "" {
			return b.formatStateForServerTypePlain(strValue, serverType)
		}
		return b.formatStatePlain(strValue)
	case "sessionauth":
		// Per-user authentication status for OAuth-protected servers
		return b.formatSessionAuthPlain(strValue)
	case "sessionstatus":
		// Per-user session connection status
		return b.formatSessionStatusPlain(strValue)
	case "toolscount":
		// Number of tools available for the session
		if strValue == "" || strValue == "0" {
			return "-"
		}
		return strValue
	case "connectedat":
		// When the session connected to the server
		return b.formatTimestampPlain(strValue)
	case "started_at", "completed_at", "timestamp":
		return b.formatTimestampPlain(strValue)
	case "duration_ms":
		return b.formatDurationPlain(value)
	case "metadata":
		return b.formatMetadataPlain(value)
	case "requiredtools", "tools":
		return b.formatToolsListPlain(value)
	case "description":
		return b.formatDescriptionPlain(strValue)
	case "steps":
		return b.formatStepsPlain(value)
	case "timeout":
		// Format timeout values with "s" suffix for seconds
		return b.formatTimeoutPlain(value)
	case "url", "command", "uri", "endpoint":
		// Don't truncate URLs, commands, URIs, or endpoints - show full value
		return strValue
	case "type", "service_type", "servicetype", "servertype":
		// Type fields are typically short, don't truncate
		return strValue
	default:
		if arr, ok := value.([]interface{}); ok {
			return b.formatArrayPlain(arr)
		}
		if obj, ok := value.(map[string]interface{}); ok {
			return b.formatObjectPlain(obj)
		}
		if len(strValue) > 50 {
			return strValue[:47] + "..."
		}
		return strValue
	}
}

// getServerTypeFromContext extracts the server type from row context.
// It delegates to the shared ExtractServerType helper for consistent behavior.
//
// Args:
//   - rowContext: The full row data
//
// Returns:
//   - string: The server type (stdio, streamable-http, sse) or empty string
func (b *TableBuilder) getServerTypeFromContext(rowContext map[string]interface{}) string {
	return ExtractServerType(rowContext)
}

// normalizeState returns the canonical display name for a state.
// This helper is used by both styled and plain formatters to ensure consistency.
func (b *TableBuilder) normalizeState(state string) string {
	switch strings.ToLower(state) {
	case "running":
		return "Running"
	case "connected":
		return "Connected"
	case "stopped":
		return "Stopped"
	case "disconnected":
		return "Disconnected"
	case "starting":
		return "Starting"
	case "stopping":
		return "Stopping"
	case "failed":
		return "Failed"
	case "error":
		return "Error"
	case "auth_required":
		return "Auth Required"
	case "unreachable":
		return "Unreachable"
	case "waiting":
		return "Waiting"
	case "retrying":
		return "Retrying"
	default:
		return state
	}
}

// normalizeStateForServerType returns the canonical display name for a state,
// using context-appropriate terminology based on server type.
// For local stdio servers, it uses "Running/Stopped" terminology.
// For remote servers (streamable-http/sse), it uses "Connected/Disconnected" terminology.
func (b *TableBuilder) normalizeStateForServerType(state string, isRemote bool) string {
	switch strings.ToLower(state) {
	case "running", "connected":
		if isRemote {
			return "Connected"
		}
		return "Running"
	case "stopped", "disconnected":
		if isRemote {
			return "Disconnected"
		}
		return "Stopped"
	case "starting":
		if isRemote {
			return "Connecting"
		}
		return "Starting"
	case "stopping":
		if isRemote {
			return "Disconnecting"
		}
		return "Stopping"
	case "failed":
		return "Failed"
	case "error":
		return "Error"
	case "auth_required":
		return "Auth Required"
	case "unreachable":
		return "Unreachable"
	case "waiting":
		return "Waiting"
	case "retrying":
		return "Retrying"
	default:
		return state
	}
}

// SimplifyToolName removes common prefixes to make tool names more readable.
// Many muster tools have predictable prefixes that add noise to table display.
// This function strips common prefixes to improve readability.
//
// Args:
//   - toolName: The full tool name to simplify
//
// Returns:
//   - string: Simplified tool name with common prefixes removed
func (b *TableBuilder) SimplifyToolName(toolName string) string {
	// Remove common prefixes
	prefixes := []string{"x_kubernetes_", "x_", "core_", "mcp_"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(toolName, prefix) {
			return strings.TrimPrefix(toolName, prefix)
		}
	}
	return toolName
}

// SortDataByName sorts data by the first column (usually name/id).
// This provides consistent ordering in tables, making it easier for users
// to find specific resources.
//
// Args:
//   - data: Array of data objects to sort
//   - columns: Column names, with the first used for sorting
//
// Returns:
//   - []interface{}: Sorted data array
func (b *TableBuilder) SortDataByName(data []interface{}, columns []string) []interface{} {
	sort.SliceStable(data, func(i, j int) bool {
		iMap, iOk := data[i].(map[string]interface{})
		jMap, jOk := data[j].(map[string]interface{})
		if iOk && jOk {
			// Use the first column (usually name/id) for sorting
			if len(columns) > 0 {
				iVal := fmt.Sprintf("%v", iMap[columns[0]])
				jVal := fmt.Sprintf("%v", jMap[columns[0]])
				return strings.ToLower(iVal) < strings.ToLower(jVal)
			}
		}
		return false
	})
	return data
}

// normalizeTimestamp simplifies ISO 8601 timestamps by removing microseconds
// and timezone information. Converts "2024-01-01T12:34:56.789Z" to "2024-01-01 12:34:56".
func (b *TableBuilder) normalizeTimestamp(timestamp string) string {
	if timestamp == "" || timestamp == "-" {
		return "-"
	}

	if strings.Contains(timestamp, "T") {
		parts := strings.Split(timestamp, "T")
		if len(parts) == 2 {
			timePart := parts[1]
			if dotIndex := strings.Index(timePart, "."); dotIndex != -1 {
				timePart = timePart[:dotIndex]
			}
			timePart = strings.TrimSuffix(timePart, "Z")
			return parts[0] + " " + timePart
		}
	}

	return timestamp
}

// parseDurationMs attempts to parse a duration value to milliseconds.
// Returns the duration in ms, whether parsing succeeded, and the original string if it was a string.
func (b *TableBuilder) parseDurationMs(value interface{}) (float64, bool, string) {
	if value == nil {
		return 0, false, ""
	}

	switch v := value.(type) {
	case int:
		return float64(v), true, ""
	case int64:
		return float64(v), true, ""
	case float64:
		return v, true, ""
	case string:
		var durationMs float64
		if parsed, err := fmt.Sscanf(v, "%f", &durationMs); parsed == 1 && err == nil {
			return durationMs, true, ""
		}
		return 0, false, v
	default:
		return 0, false, fmt.Sprintf("%v", value)
	}
}

// normalizeDuration formats duration in milliseconds to a human-readable string.
func (b *TableBuilder) normalizeDuration(durationMs float64) string {
	if durationMs < 1000 {
		return fmt.Sprintf("%.0fms", durationMs)
	} else if durationMs < 60000 {
		return fmt.Sprintf("%.1fs", durationMs/1000)
	} else if durationMs < 3600000 {
		return fmt.Sprintf("%.1fm", durationMs/60000)
	}
	return fmt.Sprintf("%.1fh", durationMs/3600000)
}

// Plain text formatting methods for kubectl-style output

// formatAvailableStatusPlain returns availability status as plain text.
func (b *TableBuilder) formatAvailableStatusPlain(value interface{}) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "Available"
		}
		return "Unavailable"
	case string:
		if v == "true" {
			return "Available"
		}
		return "Unavailable"
	default:
		return fmt.Sprintf("%v", value)
	}
}

// formatAutoStartStatusPlain returns autoStart status as plain text.
func (b *TableBuilder) formatAutoStartStatusPlain(value interface{}) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "Yes"
		}
		return "No"
	case string:
		if v == "true" {
			return "Yes"
		}
		return "No"
	default:
		return fmt.Sprintf("%v", value)
	}
}

// formatStatePlain returns state as plain text.
func (b *TableBuilder) formatStatePlain(state string) string {
	return b.normalizeState(state)
}

// formatStateForServerTypePlain returns state with context-appropriate terminology as plain text.
func (b *TableBuilder) formatStateForServerTypePlain(state string, serverType string) string {
	return b.normalizeStateForServerType(state, IsRemoteServerType(serverType))
}

// formatTimestampPlain formats timestamp as plain text.
func (b *TableBuilder) formatTimestampPlain(timestamp string) string {
	return b.normalizeTimestamp(timestamp)
}

// formatSessionAuthPlain formats per-user authentication status as plain text.
// This shows the user's auth status for OAuth-protected servers.
//
// Possible values:
//   - authenticated: User has successfully authenticated
//   - auth_required: Server requires authentication, user has not authenticated
//   - token_expired: User's token has expired, re-authentication needed
//   - unknown: Auth status cannot be determined
func (b *TableBuilder) formatSessionAuthPlain(auth string) string {
	if auth == "" {
		return "-"
	}

	switch strings.ToLower(auth) {
	case "authenticated":
		return "OK"
	case "auth_required":
		return "Required"
	case "token_expired":
		return "Expired"
	case "unknown":
		return "-"
	default:
		return auth
	}
}

// formatSessionStatusPlain formats per-user session connection status as plain text.
//
// Possible values:
//   - connected: Session is connected to the server
//   - disconnected: Session is not connected
//   - pending_auth: Waiting for user authentication
//   - failed: Session connection failed
func (b *TableBuilder) formatSessionStatusPlain(status string) string {
	if status == "" {
		return "-"
	}

	switch strings.ToLower(status) {
	case "connected":
		return "Connected"
	case "disconnected":
		return "Disconnected"
	case "pending_auth":
		return "Pending Auth"
	case "failed":
		return "Failed"
	default:
		return status
	}
}

// formatDurationPlain formats duration as plain text.
func (b *TableBuilder) formatDurationPlain(value interface{}) string {
	durationMs, ok, fallback := b.parseDurationMs(value)
	if !ok {
		if fallback == "" {
			return "-"
		}
		return fallback
	}
	return b.normalizeDuration(durationMs)
}

// formatTimeoutPlain formats timeout values with "s" suffix for seconds.
// This provides clear units for timeout configuration values.
func (b *TableBuilder) formatTimeoutPlain(value interface{}) string {
	if value == nil {
		return "-"
	}

	switch v := value.(type) {
	case float64:
		if v == 0 {
			return "-"
		}
		// Format as integer if it's a whole number
		if v == float64(int(v)) {
			return fmt.Sprintf("%ds", int(v))
		}
		return fmt.Sprintf("%.1fs", v)
	case int:
		if v == 0 {
			return "-"
		}
		return fmt.Sprintf("%ds", v)
	case int64:
		if v == 0 {
			return "-"
		}
		return fmt.Sprintf("%ds", v)
	case string:
		if v == "" || v == "0" {
			return "-"
		}
		// If already has a suffix, return as-is
		if strings.HasSuffix(v, "s") || strings.HasSuffix(v, "m") || strings.HasSuffix(v, "h") {
			return v
		}
		return v + "s"
	default:
		strVal := fmt.Sprintf("%v", v)
		if strVal == "" || strVal == "0" {
			return "-"
		}
		return strVal + "s"
	}
}

// formatMetadataPlain formats metadata as plain text.
func (b *TableBuilder) formatMetadataPlain(value interface{}) string {
	if value == nil {
		return "-"
	}

	if metaMap, ok := value.(map[string]interface{}); ok {
		var parts []string
		if icon, exists := metaMap["icon"]; exists && icon != nil {
			parts = append(parts, fmt.Sprintf("%v", icon))
		}
		if typ, exists := metaMap["type"]; exists && typ != nil {
			parts = append(parts, fmt.Sprintf("%v", typ))
		}
		if enabled, exists := metaMap["enabled"]; exists {
			if enabledBool, ok := enabled.(bool); ok {
				if enabledBool {
					parts = append(parts, "enabled")
				} else {
					parts = append(parts, "disabled")
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}

	return "[metadata]"
}

// formatToolsListPlain formats tools list as plain text.
func (b *TableBuilder) formatToolsListPlain(value interface{}) string {
	if value == nil {
		return "-"
	}

	if toolsArray, ok := value.([]interface{}); ok {
		if len(toolsArray) == 0 {
			return "none"
		}

		var toolNames []string
		for _, tool := range toolsArray {
			if toolStr, ok := tool.(string); ok {
				simplified := b.SimplifyToolName(toolStr)
				toolNames = append(toolNames, simplified)
			}
		}

		if len(toolNames) <= 2 {
			return strings.Join(toolNames, ", ")
		}
		return fmt.Sprintf("%s, %s (+%d more)", toolNames[0], toolNames[1], len(toolNames)-2)
	}

	return fmt.Sprintf("%v", value)
}

// formatDescriptionPlain formats description as plain text with truncation.
func (b *TableBuilder) formatDescriptionPlain(desc string) string {
	if len(desc) <= 50 {
		return desc
	}
	return desc[:47] + "..."
}

// formatStepsPlain formats steps as plain text.
func (b *TableBuilder) formatStepsPlain(value interface{}) string {
	if value == nil {
		return "-"
	}

	if stepsArray, ok := value.([]interface{}); ok {
		count := len(stepsArray)
		if count == 0 {
			return "No steps"
		}
		return fmt.Sprintf("%d steps", count)
	}

	return fmt.Sprintf("%v", value)
}

// formatArrayPlain formats array as plain text.
func (b *TableBuilder) formatArrayPlain(arr []interface{}) string {
	if len(arr) == 0 {
		return "[]"
	}

	if len(arr) <= 2 {
		var items []string
		for _, item := range arr {
			items = append(items, fmt.Sprintf("%v", item))
		}
		return strings.Join(items, ", ")
	}

	return fmt.Sprintf("[%d items]", len(arr))
}

// formatObjectPlain formats object as plain text.
func (b *TableBuilder) formatObjectPlain(obj map[string]interface{}) string {
	if len(obj) == 0 {
		return "{}"
	}

	displayFields := []string{"name", "type", "status", "id"}
	for _, field := range displayFields {
		if value, exists := obj[field]; exists && value != nil {
			return fmt.Sprintf("%v", value)
		}
	}

	return fmt.Sprintf("{%d fields}", len(obj))
}
