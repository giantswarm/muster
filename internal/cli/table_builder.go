package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/text"
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

// FormatCellValue formats individual cell values with appropriate styling and icons.
// It applies different formatting rules based on the column name and data type,
// providing consistent and readable output across all muster tables.
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
//   - interface{}: Formatted value with styling applied
func (b *TableBuilder) FormatCellValue(column string, value interface{}) interface{} {
	if value == nil {
		return text.FgHiBlack.Sprint("-")
	}

	strValue := fmt.Sprintf("%v", value)
	colLower := strings.ToLower(column)

	// Handle different column types with enhanced formatting
	switch colLower {
	case "name", "label", "id", "workflow", "execution_id":
		// Primary identifiers - make them prominent
		return text.FgHiCyan.Sprint(strValue)
	case "workflow_name":
		// Workflow name in executions - make it distinct
		return text.FgHiWhite.Sprint(strValue)
	case "health", "status":
		return b.formatHealthStatus(strValue)
	case "available":
		return b.formatAvailableStatus(value)
	case "state":
		return b.formatState(strValue)
	case "started_at", "completed_at":
		return b.formatTimestamp(strValue)
	case "duration_ms":
		return b.formatDuration(value)
	case "metadata":
		return b.formatMetadata(value)
	case "requiredtools", "tools":
		return b.formatToolsList(value)
	case "description":
		return b.formatDescription(strValue)
	case "type", "service_type", "servicetype", "servertype":
		return b.formatType(strValue)
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
		if len(strValue) > 30 {
			return strValue[:27] + text.FgHiBlack.Sprint("...")
		}
		return strValue
	}
}

// formatHealthStatus adds color coding and icons to health status values.
// This provides immediate visual feedback about the health state of services
// and components.
//
// Args:
//   - status: The health status string to format
//
// Returns:
//   - interface{}: Formatted status with appropriate color and icon
func (b *TableBuilder) formatHealthStatus(status string) interface{} {
	switch strings.ToLower(status) {
	case "healthy":
		return text.FgGreen.Sprint("✅ " + status)
	case "unhealthy":
		return text.FgRed.Sprint("❌ " + status)
	case "warning":
		return text.FgYellow.Sprint("⚠️  " + status)
	case "running":
		return text.FgGreen.Sprint("🟢 " + status)
	case "stopped":
		return text.FgRed.Sprint("🔴 " + status)
	case "starting":
		return text.FgYellow.Sprint("🟡 " + status)
	default:
		return status
	}
}

// formatAvailableStatus formats boolean availability with clear visual indicators.
// This is commonly used for capabilities and services to show their availability status.
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
			return text.FgGreen.Sprint("✅ Available")
		}
		return text.FgRed.Sprint("❌ Unavailable")
	case string:
		if v == "true" {
			return text.FgGreen.Sprint("✅ Available")
		}
		return text.FgRed.Sprint("❌ Unavailable")
	default:
		return fmt.Sprintf("%v", value)
	}
}

// formatState formats service state with descriptive icons.
// This provides clear visual indication of service lifecycle states.
//
// Args:
//   - state: The service state string to format
//
// Returns:
//   - interface{}: Formatted state with appropriate icon and color
func (b *TableBuilder) formatState(state string) interface{} {
	switch strings.ToLower(state) {
	case "running":
		return text.FgGreen.Sprint("▶️  Running")
	case "stopped":
		return text.FgRed.Sprint("⏹️  Stopped")
	case "starting":
		return text.FgYellow.Sprint("⏳ Starting")
	case "stopping":
		return text.FgYellow.Sprint("⏸️  Stopping")
	default:
		return state
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
		return text.FgHiBlack.Sprint("-")
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
					parts = append(parts, text.FgGreen.Sprint("enabled"))
				} else {
					parts = append(parts, text.FgRed.Sprint("disabled"))
				}
			}
		}

		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}

	return text.FgHiBlack.Sprint("[metadata]")
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
		return text.FgHiBlack.Sprint("-")
	}

	if toolsArray, ok := value.([]interface{}); ok {
		if len(toolsArray) == 0 {
			return text.FgHiBlack.Sprint("none")
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
	return desc[:45] + text.FgHiBlack.Sprint("...")
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
	return text.FgCyan.Sprint(typ)
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
		return text.FgHiBlack.Sprint("-")
	}

	if stepsArray, ok := value.([]interface{}); ok {
		count := len(stepsArray)
		if count == 0 {
			return text.FgHiBlack.Sprint("No steps")
		}
		return text.FgBlue.Sprintf("%d steps", count)
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
		return text.FgHiBlack.Sprint("[]")
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
	return text.FgBlue.Sprintf("[%d items]", len(arr))
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
		return text.FgHiBlack.Sprint("{}")
	}

	// Look for common display fields
	displayFields := []string{"name", "type", "status", "id"}
	for _, field := range displayFields {
		if value, exists := obj[field]; exists && value != nil {
			return fmt.Sprintf("%v", value)
		}
	}

	// Fallback to indicating it's an object
	return text.FgBlue.Sprintf("{%d fields}", len(obj))
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
		return text.FgGreen.Sprint("🟢")
	case "serviceClasses":
		return text.FgYellow.Sprint("🟡")
	case "mcpServers":
		return text.FgRed.Sprint("🔴")
	case "workflows":
		return text.FgBlue.Sprint("🔵")
	case "executions":
		return text.FgCyan.Sprint("🔄")

	default:
		return text.FgHiBlack.Sprint("⚫")
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
	if timestamp == "" || timestamp == "-" {
		return text.FgHiBlack.Sprint("-")
	}

	// Remove microseconds and timezone for cleaner display
	// Convert "2024-01-01T12:34:56.789Z" to "2024-01-01 12:34:56"
	if strings.Contains(timestamp, "T") {
		parts := strings.Split(timestamp, "T")
		if len(parts) == 2 {
			timePart := parts[1]
			// Remove microseconds and timezone
			if dotIndex := strings.Index(timePart, "."); dotIndex != -1 {
				timePart = timePart[:dotIndex]
			}
			if strings.HasSuffix(timePart, "Z") {
				timePart = strings.TrimSuffix(timePart, "Z")
			}
			return text.FgHiBlack.Sprint(parts[0] + " " + timePart)
		}
	}

	return text.FgHiBlack.Sprint(timestamp)
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
	if value == nil {
		return text.FgHiBlack.Sprint("-")
	}

	// Convert to float64 for calculation
	var durationMs float64
	switch v := value.(type) {
	case int:
		durationMs = float64(v)
	case int64:
		durationMs = float64(v)
	case float64:
		durationMs = v
	case string:
		// Try to parse string as number
		if parsed, err := fmt.Sscanf(v, "%f", &durationMs); parsed != 1 || err != nil {
			return v // Return as-is if can't parse
		}
	default:
		return fmt.Sprintf("%v", value)
	}

	// Format based on duration
	if durationMs < 1000 {
		return text.FgGreen.Sprintf("%.0fms", durationMs)
	} else if durationMs < 60000 {
		return text.FgYellow.Sprintf("%.1fs", durationMs/1000)
	} else if durationMs < 3600000 {
		return text.FgCyan.Sprintf("%.1fm", durationMs/60000)
	} else {
		return text.FgRed.Sprintf("%.1fh", durationMs/3600000)
	}
}
