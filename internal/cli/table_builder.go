package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/text"
)

// TableBuilder handles cell formatting and styling
type TableBuilder struct{}

// NewTableBuilder creates a new table builder
func NewTableBuilder() *TableBuilder {
	return &TableBuilder{}
}

// FormatCellValue formats individual cell values with appropriate styling
func (b *TableBuilder) FormatCellValue(column string, value interface{}) interface{} {
	if value == nil {
		return text.FgHiBlack.Sprint("-")
	}

	strValue := fmt.Sprintf("%v", value)
	colLower := strings.ToLower(column)

	// Handle different column types with enhanced formatting
	switch colLower {
	case "name", "label", "id", "workflow", "capability":
		// Primary identifiers - make them prominent
		return text.FgHiCyan.Sprint(strValue)
	case "health", "status":
		return b.formatHealthStatus(strValue)
	case "available":
		return b.formatAvailableStatus(value)
	case "state":
		return b.formatState(strValue)
	case "metadata":
		return b.formatMetadata(value)
	case "requiredtools", "tools":
		return b.formatToolsList(value)
	case "description":
		return b.formatDescription(strValue)
	case "type", "service_type", "servicetype", "servertype", "capabilitytype":
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

// formatHealthStatus adds color coding to health status
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

// formatAvailableStatus formats boolean availability
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

// formatState formats service state with icons
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

// formatMetadata extracts useful information from metadata objects
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

// formatToolsList shows actual tool names instead of "[N items]"
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

// SimplifyToolName removes common prefixes to make tool names more readable
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

// formatDescription truncates long descriptions appropriately
func (b *TableBuilder) formatDescription(desc string) interface{} {
	if len(desc) <= 50 {
		return desc
	}
	return desc[:45] + text.FgHiBlack.Sprint("...")
}

// formatType adds subtle styling to types
func (b *TableBuilder) formatType(typ string) interface{} {
	return text.FgCyan.Sprint(typ)
}

// formatSteps formats workflow steps with a count
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

// formatArray provides clean display of arrays
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

// formatObject provides clean display of objects
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

// SortDataByName sorts data by the first column (usually name/id)
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

// GetResourceIcon returns an appropriate icon for the resource type
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
	case "capabilities":
		return text.FgMagenta.Sprint("🟣")
	default:
		return text.FgHiBlack.Sprint("⚫")
	}
}

// Pluralize adds 's' to a word if it doesn't already end with 's'
func (b *TableBuilder) Pluralize(word string) string {
	if strings.HasSuffix(word, "s") {
		return word
	}
	return word + "s"
}
