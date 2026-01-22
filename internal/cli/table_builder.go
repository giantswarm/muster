package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/text"
)

// emojiDisabled caches whether emoji display is disabled via environment variable.
// Check NO_EMOJI or MUSTER_NO_EMOJI environment variables.
var emojiDisabled = os.Getenv("NO_EMOJI") != "" || os.Getenv("MUSTER_NO_EMOJI") != ""

// IsEmojiDisabled returns true if emoji display is disabled via environment variables.
// Users can set NO_EMOJI=1 or MUSTER_NO_EMOJI=1 to disable emoji in output.
func IsEmojiDisabled() bool {
	return emojiDisabled
}

// stateIcon returns an appropriate icon for the given state, respecting NO_EMOJI setting.
func stateIcon(emoji, fallback string) string {
	if emojiDisabled {
		return fallback
	}
	return emoji
}

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

// FormatCellValue formats individual cell values with ANSI color styling and icons.
// It applies different formatting rules based on the column name and data type,
// providing consistent and readable output across all muster tables.
//
// NOTE: This method is currently unused as table output uses FormatCellValuePlain
// for kubectl-style plain text formatting. It is preserved for potential future
// color output support (e.g., --color=auto|always|never flag).
//
// The formatter recognizes common column types and applies appropriate styling:
//   - Status fields get color-coded icons
//   - Names and IDs get prominent highlighting
//   - Arrays and objects get compact representation
//   - Long text gets truncated with ellipsis
//
// Args:
//   - column: The column name/type to determine formatting rules
//   - value: The raw value to format
//
// Returns:
//   - interface{}: Formatted value with ANSI styling applied
func (b *TableBuilder) FormatCellValue(column string, value interface{}) interface{} {
	return b.FormatCellValueWithContext(column, value, nil)
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

// FormatCellValueWithContext formats individual cell values with ANSI color styling and row context.
// This allows for context-aware formatting where the display depends on other
// fields in the same row (e.g., state terminology depends on server type).
//
// NOTE: This method is currently unused as table output uses FormatCellValuePlain
// for kubectl-style plain text formatting. It is preserved for potential future
// color output support (e.g., --color=auto|always|never flag).
//
// Args:
//   - column: The column name/type to determine formatting rules
//   - value: The raw value to format
//   - rowContext: The full row data for context-aware formatting (may be nil)
//
// Returns:
//   - interface{}: Formatted value with ANSI styling applied
func (b *TableBuilder) FormatCellValueWithContext(column string, value interface{}, rowContext map[string]interface{}) interface{} {
	if value == nil {
		return text.Faint.Sprint("-")
	}

	strValue := fmt.Sprintf("%v", value)
	colLower := strings.ToLower(column)

	// Handle different column types with enhanced formatting
	switch colLower {
	case "name", "label", "id", "workflow", "execution_id":
		// Primary identifiers - make them prominent
		return text.Colors{text.FgHiBlue, text.Bold}.Sprint(strValue)
	case "workflow_name":
		// Workflow name in executions - make it distinct
		return text.Bold.Sprint(strValue)
	case "resource_name":
		// Resource names in events - make them prominent
		return text.Colors{text.FgHiBlue, text.Bold}.Sprint(strValue)
	case "health", "status":
		return b.formatHealthStatus(strValue)
	case "available":
		return b.formatAvailableStatus(value)
	case "autostart":
		return b.formatAutoStartStatus(value)
	case "state":
		// State represents infrastructure state for MCPServers and services
		// MCPServer values are context-appropriate: Running/Connected/Starting/Connecting/Stopped/Disconnected/Failed
		serverType := b.getServerTypeFromContext(rowContext)
		if serverType != "" {
			return b.formatStateForServerType(strValue, serverType)
		}
		return b.formatState(strValue)
	case "started_at", "completed_at", "timestamp":
		return b.formatTimestamp(strValue)
	case "duration_ms":
		return b.formatDuration(value)
	case "reason":
		return b.formatEventReason(strValue)
	case "type":
		return b.formatEventType(strValue)
	case "resource_type":
		return b.formatResourceType(strValue)
	case "message":
		return b.formatEventMessage(strValue)
	case "metadata":
		return b.formatMetadata(value)
	case "requiredtools", "tools":
		return b.formatToolsList(value)
	case "description":
		return b.formatDescription(strValue)
	case "service_type", "servicetype", "servertype":
		return b.formatType(strValue)
	case "command":
		return b.formatCommand(strValue)
	case "url":
		return b.formatURL(strValue)
	case "endpoint":
		return b.formatEndpoint(column, value)
	case "steps":
		return b.formatSteps(value)
	default:
		// Default formatting - handle arrays and objects better
		if arr, ok := value.([]interface{}); ok {
			return b.formatArray(arr)
		}
		if obj, ok := value.(map[string]interface{}); ok {
			return b.formatObject(obj)
		}
		// Default string truncation
		if len(strValue) > 50 {
			return strValue[:47] + text.Faint.Sprint("...")
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

// formatHealthStatus adds color coding and icons to health status values.
// This provides immediate visual feedback about the health state of services
// and components. Respects NO_EMOJI/MUSTER_NO_EMOJI environment variables.
//
// Args:
//   - status: The health status string to format
//
// Returns:
//   - interface{}: Formatted status with appropriate color and icon
func (b *TableBuilder) formatHealthStatus(status string) interface{} {
	switch strings.ToLower(status) {
	case "healthy":
		return text.Colors{text.FgHiGreen, text.Bold}.Sprint(stateIcon("✅ ", "[OK] ") + status)
	case "unhealthy":
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("❌ ", "[BAD] ") + status)
	case "warning":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("⚠️  ", "[WARN] ") + status)
	case "running":
		return text.Colors{text.FgHiGreen, text.Bold}.Sprint(stateIcon("🟢 ", "[RUN] ") + status)
	case "stopped":
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("🔴 ", "[STOP] ") + status)
	case "starting":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("🟡 ", "[INIT] ") + status)
	default:
		return status
	}
}

// formatAvailableStatus formats boolean availability with clear visual indicators.
// This is commonly used for capabilities and services to show their availability status.
// Respects NO_EMOJI/MUSTER_NO_EMOJI environment variables.
//
// Args:
//   - value: The availability value (boolean or string)
//
// Returns:
//   - interface{}: Formatted availability status with color and icon
func (b *TableBuilder) formatAvailableStatus(value interface{}) interface{} {
	switch v := value.(type) {
	case bool:
		if v {
			return text.Colors{text.FgHiGreen, text.Bold}.Sprint(stateIcon("✅ ", "[YES] ") + "Available")
		}
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("❌ ", "[NO] ") + "Unavailable")
	case string:
		if v == "true" {
			return text.Colors{text.FgHiGreen, text.Bold}.Sprint(stateIcon("✅ ", "[YES] ") + "Available")
		}
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("❌ ", "[NO] ") + "Unavailable")
	default:
		return fmt.Sprintf("%v", value)
	}
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

// formatState formats service state with descriptive icons.
// This provides clear visual indication of service lifecycle states.
// Respects NO_EMOJI/MUSTER_NO_EMOJI environment variables for terminal compatibility.
//
// Args:
//   - state: The service state string to format
//
// Returns:
//   - interface{}: Formatted state with appropriate icon and color
func (b *TableBuilder) formatState(state string) interface{} {
	normalized := b.normalizeState(state)
	switch strings.ToLower(state) {
	case "running", "connected":
		return text.Colors{text.FgHiGreen, text.Bold}.Sprint(stateIcon("▶️  ", "[RUN] ") + normalized)
	case "stopped", "disconnected":
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("⏹️  ", "[STOP] ") + normalized)
	case "starting":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("⏳ ", "[INIT] ") + normalized)
	case "stopping":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("⏸️  ", "[HALT] ") + normalized)
	case "failed":
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("❌ ", "[FAIL] ") + normalized)
	case "error":
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("⚠️  ", "[ERR] ") + normalized)
	case "auth_required":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("🔐 ", "[AUTH] ") + normalized)
	case "unreachable":
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("🚫 ", "[UNRCH] ") + normalized)
	case "waiting":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("⏳ ", "[WAIT] ") + normalized)
	case "retrying":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("🔄 ", "[RETRY] ") + normalized)
	default:
		return normalized
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

// formatStateForServerType formats service state with context-appropriate terminology.
// For local stdio servers, it uses "Running/Stopped" terminology.
// For remote servers (streamable-http/sse), it uses "Connected/Disconnected" terminology.
// Respects NO_EMOJI/MUSTER_NO_EMOJI environment variables for terminal compatibility.
//
// Args:
//   - state: The service state string to format
//   - serverType: The type of server (stdio, streamable-http, sse)
//
// Returns:
//   - interface{}: Formatted state with appropriate icon, color, and terminology
func (b *TableBuilder) formatStateForServerType(state string, serverType string) interface{} {
	isRemote := IsRemoteServerType(serverType)
	normalized := b.normalizeStateForServerType(state, isRemote)

	switch strings.ToLower(state) {
	case "running", "connected":
		if isRemote {
			return text.Colors{text.FgHiGreen, text.Bold}.Sprint(stateIcon("🔗 ", "[CONN] ") + normalized)
		}
		return text.Colors{text.FgHiGreen, text.Bold}.Sprint(stateIcon("▶️  ", "[RUN] ") + normalized)
	case "stopped", "disconnected":
		if isRemote {
			return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("⚪ ", "[DISC] ") + normalized)
		}
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("⏹️  ", "[STOP] ") + normalized)
	case "starting":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("⏳ ", "[INIT] ") + normalized)
	case "stopping":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("⏸️  ", "[HALT] ") + normalized)
	case "failed":
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("❌ ", "[FAIL] ") + normalized)
	case "error":
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("⚠️  ", "[ERR] ") + normalized)
	case "auth_required":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("🔐 ", "[AUTH] ") + normalized)
	case "unreachable":
		return text.Colors{text.FgHiRed, text.Bold}.Sprint(stateIcon("🚫 ", "[UNRCH] ") + normalized)
	case "waiting":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("⏳ ", "[WAIT] ") + normalized)
	case "retrying":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("🔄 ", "[RETRY] ") + normalized)
	default:
		return normalized
	}
}

// formatMetadata extracts and formats useful information from metadata objects.
// Metadata objects often contain nested information that needs to be summarized
// for table display. This function extracts the most relevant fields.
//
// Args:
//   - value: The metadata object to format
//
// Returns:
//   - interface{}: Formatted metadata summary
func (b *TableBuilder) formatMetadata(value interface{}) interface{} {
	if value == nil {
		return text.Faint.Sprint("-")
	}

	// Handle metadata object
	if metaMap, ok := value.(map[string]interface{}); ok {
		var parts []string

		// Extract icon if available
		if icon, exists := metaMap["icon"]; exists && icon != nil {
			parts = append(parts, fmt.Sprintf("%v", icon))
		}

		// Extract type
		if typ, exists := metaMap["type"]; exists && typ != nil {
			parts = append(parts, fmt.Sprintf("%v", typ))
		}

		// Extract enabled status
		if enabled, exists := metaMap["enabled"]; exists {
			if enabledBool, ok := enabled.(bool); ok {
				if enabledBool {
					parts = append(parts, text.Colors{text.FgHiGreen, text.Bold}.Sprint("enabled"))
				} else {
					parts = append(parts, text.Colors{text.FgHiRed, text.Bold}.Sprint("disabled"))
				}
			}
		}

		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}

	return text.Faint.Sprint("[metadata]")
}

// formatToolsList formats arrays of tool names for compact display.
// Instead of showing "[N items]", this shows actual tool names up to a limit,
// then indicates how many more tools are available.
//
// Args:
//   - value: The tools array to format
//
// Returns:
//   - interface{}: Formatted tools list with simplified names
func (b *TableBuilder) formatToolsList(value interface{}) interface{} {
	if value == nil {
		return text.Faint.Sprint("-")
	}

	if toolsArray, ok := value.([]interface{}); ok {
		if len(toolsArray) == 0 {
			return text.Faint.Sprint("none")
		}

		var toolNames []string
		for _, tool := range toolsArray {
			if toolStr, ok := tool.(string); ok {
				// Simplify tool names for display
				simplified := b.SimplifyToolName(toolStr)
				toolNames = append(toolNames, simplified)
			}
		}

		if len(toolNames) <= 2 {
			return strings.Join(toolNames, ", ")
		} else {
			// Show first 2 and count
			return fmt.Sprintf("%s, %s (+%d more)",
				toolNames[0], toolNames[1], len(toolNames)-2)
		}
	}

	return fmt.Sprintf("%v", value)
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

// formatDescription truncates long descriptions with ellipsis for table display.
// Descriptions can be very long and need to be truncated to fit in table columns
// while still providing useful information.
//
// Args:
//   - desc: The description string to format
//
// Returns:
//   - interface{}: Formatted description, possibly truncated
func (b *TableBuilder) formatDescription(desc string) interface{} {
	if len(desc) <= 50 {
		return desc
	}
	return desc[:47] + text.Faint.Sprint("...")
}

// formatType adds subtle styling to type information.
// Type fields are important for identifying resource types and get consistent styling.
//
// Args:
//   - typ: The type string to format
//
// Returns:
//   - interface{}: Formatted type with subtle color styling
func (b *TableBuilder) formatType(typ string) interface{} {
	return text.Colors{text.FgHiCyan, text.Bold}.Sprint(typ)
}

// formatSteps formats workflow steps information with a count indicator.
// Workflow steps are typically arrays that are better represented as counts
// rather than the full step details in table format.
//
// Args:
//   - value: The steps array to format
//
// Returns:
//   - interface{}: Formatted steps count with appropriate styling
func (b *TableBuilder) formatSteps(value interface{}) interface{} {
	if value == nil {
		return text.Faint.Sprint("-")
	}

	if stepsArray, ok := value.([]interface{}); ok {
		count := len(stepsArray)
		if count == 0 {
			return text.Faint.Sprint("No steps")
		}
		return text.Colors{text.FgHiBlue, text.Bold}.Sprintf("%d steps", count)
	}

	return fmt.Sprintf("%v", value)
}

// formatArray provides clean display of generic arrays.
// This handles arrays that don't fit into specific categories,
// providing a balance between information and readability.
//
// Args:
//   - arr: The array to format
//
// Returns:
//   - interface{}: Formatted array representation
func (b *TableBuilder) formatArray(arr []interface{}) interface{} {
	if len(arr) == 0 {
		return text.Faint.Sprint("[]")
	}

	// For small arrays, show the items
	if len(arr) <= 2 {
		var items []string
		for _, item := range arr {
			items = append(items, fmt.Sprintf("%v", item))
		}
		return strings.Join(items, ", ")
	}

	// For larger arrays, show count
	return text.Colors{text.FgHiBlue, text.Bold}.Sprintf("[%d items]", len(arr))
}

// formatObject provides clean display of generic objects.
// This handles nested objects by looking for common display fields
// or showing a summary of the object's structure.
//
// Args:
//   - obj: The object map to format
//
// Returns:
//   - interface{}: Formatted object representation
func (b *TableBuilder) formatObject(obj map[string]interface{}) interface{} {
	if len(obj) == 0 {
		return text.Faint.Sprint("{}")
	}

	// Look for common display fields
	displayFields := []string{"name", "type", "status", "id"}
	for _, field := range displayFields {
		if value, exists := obj[field]; exists && value != nil {
			return fmt.Sprintf("%v", value)
		}
	}

	// Fallback to indicating it's an object
	return text.Colors{text.FgHiBlue, text.Bold}.Sprintf("{%d fields}", len(obj))
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

// GetResourceIcon returns an appropriate icon for the resource type.
// Different muster resource types get different colored icons for easy
// visual identification in tables and summaries.
//
// Args:
//   - resourceType: The type of resource (services, workflows, etc.)
//
// Returns:
//   - string: Colored icon appropriate for the resource type
func (b *TableBuilder) GetResourceIcon(resourceType string) string {
	switch resourceType {
	case "services":
		return text.Colors{text.FgHiGreen, text.Bold}.Sprint("🟢")
	case "serviceClasses":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint("🟡")
	case "mcpServers":
		return text.Colors{text.FgHiRed, text.Bold}.Sprint("🔴")
	case "workflows":
		return text.Colors{text.FgHiBlue, text.Bold}.Sprint("🔵")
	case "executions":
		return text.Colors{text.FgHiCyan, text.Bold}.Sprint("🔄")
	case "mcpTool", "mcpTools":
		return text.Colors{text.FgHiMagenta, text.Bold}.Sprint("🔧")
	case "mcpResource", "mcpResources":
		return text.Colors{text.FgHiCyan, text.Bold}.Sprint("📦")
	case "mcpPrompt", "mcpPrompts":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint("📝")
	default:
		return text.Faint.Sprint("⚫")
	}
}

// Pluralize adds 's' to a word if it doesn't already end with 's'.
// This is used for generating consistent plural forms in table summaries
// and resource counting.
//
// Args:
//   - word: The word to potentially pluralize
//
// Returns:
//   - string: Pluralized word
func (b *TableBuilder) Pluralize(word string) string {
	if strings.HasSuffix(word, "s") {
		return word
	}
	return word + "s"
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

// formatTimestamp formats timestamp strings for better readability.
// It simplifies ISO 8601 timestamps by removing microseconds and timezone
// information for cleaner table display.
//
// Args:
//   - timestamp: The timestamp string to format
//
// Returns:
//   - interface{}: Formatted timestamp string
func (b *TableBuilder) formatTimestamp(timestamp string) interface{} {
	normalized := b.normalizeTimestamp(timestamp)
	if normalized == "-" {
		return text.Faint.Sprint("-")
	}
	return normalized
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

// formatDuration formats duration in milliseconds to a human-readable format.
// It converts milliseconds to appropriate units (ms, s, m, h) for better
// understanding of execution times.
//
// Args:
//   - value: The duration value (typically in milliseconds)
//
// Returns:
//   - interface{}: Formatted duration string with appropriate units
func (b *TableBuilder) formatDuration(value interface{}) interface{} {
	durationMs, ok, fallback := b.parseDurationMs(value)
	if !ok {
		if fallback == "" {
			return text.Faint.Sprint("-")
		}
		return fallback
	}

	normalized := b.normalizeDuration(durationMs)

	// Apply color based on duration
	if durationMs < 1000 {
		return text.Colors{text.FgHiGreen, text.Bold}.Sprint(normalized)
	} else if durationMs < 60000 {
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(normalized)
	} else if durationMs < 3600000 {
		return text.Colors{text.FgHiMagenta, text.Bold}.Sprint(normalized)
	}
	return text.Colors{text.FgHiRed, text.Bold}.Sprint(normalized)
}

// formatAutoStartStatus formats boolean autoStart status with clear visual indicators.
// This shows whether an MCP server is configured to start/connect automatically.
// Respects NO_EMOJI/MUSTER_NO_EMOJI environment variables.
//
// Args:
//   - value: The autoStart value (boolean)
//
// Returns:
//   - interface{}: Formatted autoStart status with color and icon
func (b *TableBuilder) formatAutoStartStatus(value interface{}) interface{} {
	switch v := value.(type) {
	case bool:
		if v {
			return text.Colors{text.FgHiGreen, text.Bold}.Sprint(stateIcon("✅ ", "[YES] ") + "Yes")
		}
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("⚪ ", "[NO] ") + "No")
	case string:
		if v == "true" {
			return text.Colors{text.FgHiGreen, text.Bold}.Sprint(stateIcon("✅ ", "[YES] ") + "Yes")
		}
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(stateIcon("⚪ ", "[NO] ") + "No")
	default:
		return fmt.Sprintf("%v", value)
	}
}

// GetAutoStartColumnName returns the appropriate column name based on server type.
// For local stdio servers, it returns "AutoStart".
// For remote servers (streamable-http/sse), it returns "AutoConnect".
//
// Args:
//   - serverType: The type of server (stdio, streamable-http, sse)
//
// Returns:
//   - string: Contextually appropriate column name
func (b *TableBuilder) GetAutoStartColumnName(serverType string) string {
	if IsRemoteServerType(serverType) {
		return "AutoConnect"
	}
	return "AutoStart"
}

// formatCommand formats command strings with appropriate highlighting.
// This provides clear visual indication of the executable being used.
//
// Args:
//   - command: The command string to format
//
// Returns:
//   - interface{}: Formatted command with appropriate styling
func (b *TableBuilder) formatCommand(command string) interface{} {
	if command == "" {
		return text.Faint.Sprint("-")
	}

	// Highlight the command with a subtle color
	return text.Colors{text.FgHiBlue, text.Bold}.Sprint(command)
}

// formatURL formats URL strings with appropriate highlighting.
// This provides clear visual indication of remote endpoints.
//
// Args:
//   - url: The URL string to format
//
// Returns:
//   - interface{}: Formatted URL with appropriate styling
func (b *TableBuilder) formatURL(url string) interface{} {
	if url == "" {
		return text.Faint.Sprint("-")
	}

	// Highlight URLs with a distinct color
	return text.Colors{text.FgHiMagenta, text.Bold}.Sprint(url)
}

// formatEndpoint intelligently displays either a command or a URL based on the available data.
// This function looks at the entire row context to determine the best value to show.
//
// Args:
//   - column: The column name (not used in this implementation)
//   - value: The entire row object to extract endpoint information from
//
// Returns:
//   - interface{}: Formatted endpoint string (command or URL)
func (b *TableBuilder) formatEndpoint(column string, value interface{}) interface{} {
	// For the endpoint column, we need access to the entire row
	// This is a special case where we need more context than just the cell value
	if rowMap, ok := value.(map[string]interface{}); ok {
		// Check server type first
		serverType := ""
		if typ, exists := rowMap["type"]; exists {
			serverType = fmt.Sprintf("%v", typ)
		}

		// For remote servers, prioritize URL
		if serverType == "streamable-http" || serverType == "sse" {
			if url, exists := rowMap["url"]; exists && url != nil {
				urlStr := fmt.Sprintf("%v", url)
				if urlStr != "" {
					return b.formatURL(urlStr)
				}
			}
		}

		// For stdio servers or fallback, show command
		if command, exists := rowMap["command"]; exists && command != nil {
			commandStr := fmt.Sprintf("%v", command)
			if commandStr != "" {
				return b.formatCommand(commandStr)
			}
		}

		// Fallback to URL if command is not available
		if url, exists := rowMap["url"]; exists && url != nil {
			urlStr := fmt.Sprintf("%v", url)
			if urlStr != "" {
				return b.formatURL(urlStr)
			}
		}
	}

	return text.Faint.Sprint("-")
}

// formatEventReason formats event reason with appropriate styling.
func (b *TableBuilder) formatEventReason(reason string) interface{} {
	// Color code based on common event reasons
	return reason
}

// formatEventType formats event type with appropriate styling.
func (b *TableBuilder) formatEventType(eventType string) interface{} {
	switch strings.ToLower(eventType) {
	case "warning":
		return text.Colors{text.FgHiYellow, text.Bold}.Sprint(eventType)
	case "normal":
		return text.Colors{text.FgHiGreen, text.Bold}.Sprint(eventType)
	default:
		return text.Colors{text.FgHiBlue, text.Bold}.Sprint(eventType)
	}
}

// formatResourceType formats resource type with appropriate styling.
func (b *TableBuilder) formatResourceType(resourceType string) interface{} {
	return resourceType
}

// formatEventMessage formats event message with appropriate truncation.
func (b *TableBuilder) formatEventMessage(message string) interface{} {
	// Truncate long messages but preserve readability
	if len(message) > 60 {
		return message[:57] + text.Faint.Sprint("...")
	}
	return message
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
