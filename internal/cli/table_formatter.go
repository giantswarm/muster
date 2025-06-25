package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// TableFormatter handles table creation and optimization
type TableFormatter struct {
	options ExecutorOptions
	builder *TableBuilder
}

// NewTableFormatter creates a new table formatter
func NewTableFormatter(options ExecutorOptions) *TableFormatter {
	return &TableFormatter{
		options: options,
		builder: NewTableBuilder(),
	}
}

// FormatData formats data according to its type (object or array)
func (f *TableFormatter) FormatData(data interface{}) error {
	switch d := data.(type) {
	case map[string]interface{}:
		return f.formatTableFromObject(d)
	case []interface{}:
		return f.formatTableFromArray(d)
	default:
		// Simple value, just print it
		fmt.Printf("%v\n", data)
		return nil
	}
}

// formatTableFromObject handles object data that might contain arrays
func (f *TableFormatter) formatTableFromObject(data map[string]interface{}) error {
	// Check for common wrapper patterns like {"services": [...], "total": N}
	arrayKey := f.findArrayKey(data)
	if arrayKey != "" {
		if arr, ok := data[arrayKey].([]interface{}); ok {
			// Just format the array - the summary will be handled by formatTableFromArray
			return f.formatTableFromArray(arr)
		}
	}

	// No array found, format as key-value pairs
	return f.formatKeyValueTable(data)
}

// findArrayKey looks for common array keys in wrapped objects
func (f *TableFormatter) findArrayKey(data map[string]interface{}) string {
	arrayKeys := []string{"services", "serviceClasses", "mcpServers", "workflows", "capabilities", "items", "results"}

	for _, key := range arrayKeys {
		if value, exists := data[key]; exists {
			if _, isArray := value.([]interface{}); isArray {
				return key
			}
		}
	}
	return ""
}

// formatTableFromArray creates a table from an array of objects
func (f *TableFormatter) formatTableFromArray(data []interface{}) error {
	if len(data) == 0 {
		fmt.Printf("%s %s\n",
			text.FgYellow.Sprint("📋"),
			text.FgYellow.Sprint("No items found"))
		return nil
	}

	// Get the first object to determine columns
	firstObj, ok := data[0].(map[string]interface{})
	if !ok {
		// Array of simple values
		return f.formatSimpleList(data)
	}

	// Determine table type and optimize columns
	columns := f.optimizeColumns(firstObj)
	resourceType := f.detectResourceType(firstObj)

	// Create professional table
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)

	// Set headers with colors and icons
	headers := make([]interface{}, len(columns))
	for i, col := range columns {
		headers[i] = text.FgHiCyan.Sprint(strings.ToUpper(col))
	}
	t.AppendHeader(headers)

	// Add rows with enhanced formatting - sort by name field if present
	sortedData := f.builder.SortDataByName(data, columns)
	for _, item := range sortedData {
		if itemMap, ok := item.(map[string]interface{}); ok {
			row := make([]interface{}, len(columns))
			for i, col := range columns {
				row[i] = f.builder.FormatCellValue(col, itemMap[col])
			}
			t.AppendRow(row)
		}
	}

	t.Render()

	// Add summary line with icon based on resource type
	icon := f.builder.GetResourceIcon(resourceType)
	resourceName := f.builder.Pluralize(resourceType)
	fmt.Printf("\n%s %s %s %s\n",
		icon,
		text.FgHiBlue.Sprint("Total:"),
		text.FgHiWhite.Sprint(len(data)),
		text.FgHiBlue.Sprint(resourceName))

	return nil
}

// optimizeColumns determines the best columns to show based on the data type
func (f *TableFormatter) optimizeColumns(sample map[string]interface{}) []string {
	// Extract all available keys
	var allKeys []string
	for key := range sample {
		allKeys = append(allKeys, key)
	}
	sort.Strings(allKeys)

	// Always prioritize name/ID fields first
	nameFields := []string{"name", "label", "id", "workflow", "capability"}
	var columns []string

	// Add the primary identifier first (Name/ID/Label)
	for _, nameField := range nameFields {
		if f.keyExists(sample, nameField) {
			columns = append(columns, nameField)
			break // Only add one primary identifier
		}
	}

	// Define priority columns for different resource types (excluding name fields already added)
	priorityColumns := map[string][]string{
		"services":       {"health", "state", "service_type", "metadata"},
		"serviceClasses": {"available", "serviceType", "description", "requiredTools"},
		"mcpServers":     {"state", "serverType", "description"},
		"workflows":      {"status", "description", "steps"},
		"capabilities":   {"available", "capabilityType", "description"},
		"generic":        {"status", "type", "description", "available"},
	}

	// Detect resource type and use optimized columns
	resourceType := f.detectResourceType(sample)
	if priorities, exists := priorityColumns[resourceType]; exists {
		// Add priority columns that exist (and haven't been added yet)
		for _, col := range priorities {
			if f.keyExists(sample, col) && !f.containsString(columns, col) {
				columns = append(columns, col)
			}
		}
	}

	// For complex resource types, limit columns to prevent wrapping
	maxColumns := 6
	if resourceType == "serviceClasses" || resourceType == "mcpServers" {
		maxColumns = 5 // More conservative for wider data
	}

	// Add remaining columns alphabetically if we have space
	if len(columns) < maxColumns {
		remaining := f.getRemainingKeys(allKeys, columns)
		spaceLeft := maxColumns - len(columns)
		if spaceLeft > 0 && len(remaining) > 0 {
			addCount := f.min(spaceLeft, len(remaining))
			columns = append(columns, remaining[:addCount]...)
		}
	}

	return columns
}

// detectResourceType attempts to determine what type of resource this is
func (f *TableFormatter) detectResourceType(sample map[string]interface{}) string {
	// Look for distinctive fields
	if f.keyExists(sample, "health") && f.keyExists(sample, "service_type") {
		return "services"
	}
	if f.keyExists(sample, "serviceType") && f.keyExists(sample, "requiredTools") {
		return "serviceClasses"
	}
	// Check for server-related fields for mcpServers
	if f.keyExists(sample, "serverType") || f.keyExists(sample, "serverCommand") ||
		(f.keyExists(sample, "type") && f.keyExists(sample, "command")) ||
		(f.keyExists(sample, "available") && f.keyExists(sample, "category")) {
		return "mcpServers"
	}
	// Check for workflow-related fields
	if f.keyExists(sample, "steps") || f.keyExists(sample, "workflow") ||
		(f.keyExists(sample, "name") && f.keyExists(sample, "version") && f.keyExists(sample, "description")) {
		return "workflows"
	}
	if f.keyExists(sample, "capabilityType") {
		return "capabilities"
	}
	return "generic"
}

// formatKeyValueTable formats an object as key-value pairs
func (f *TableFormatter) formatKeyValueTable(data map[string]interface{}) error {
	// Check if this is workflow data and handle it specially
	if f.isWorkflowData(data) {
		return f.formatWorkflowDetails(data)
	}

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.FgHiCyan.Sprint("PROPERTY"),
		text.FgHiCyan.Sprint("VALUE"),
	})

	// Sort keys for consistent output
	var keys []string
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := f.builder.FormatCellValue(key, data[key])
		t.AppendRow(table.Row{
			text.FgYellow.Sprint(key),
			value,
		})
	}

	t.Render()
	return nil
}

// isWorkflowData checks if the data represents a workflow
func (f *TableFormatter) isWorkflowData(data map[string]interface{}) bool {
	// Check for workflow-specific fields
	if _, hasWorkflow := data["workflow"]; hasWorkflow {
		return true
	}

	// Check if it has workflow-like structure (name, steps, etc.)
	hasName := f.keyExists(data, "name")
	hasSteps := f.keyExists(data, "steps")
	hasInputSchema := f.keyExists(data, "inputschema") || f.keyExists(data, "InputSchema")

	return hasName && (hasSteps || hasInputSchema)
}

// formatWorkflowDetails provides a clean, readable format for workflow data
func (f *TableFormatter) formatWorkflowDetails(data map[string]interface{}) error {
	// Extract workflow data from the "workflow" field if it exists
	var workflowData map[string]interface{}
	if workflow, exists := data["workflow"]; exists {
		if workflowMap, ok := workflow.(map[string]interface{}); ok {
			workflowData = workflowMap
		}
	} else {
		workflowData = data
	}

	// Create main info table
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.FgHiCyan.Sprint("PROPERTY"),
		text.FgHiCyan.Sprint("VALUE"),
	})

	// Display basic workflow information
	basicFields := []string{"Name", "name", "Description", "description", "Version", "version"}
	for _, field := range basicFields {
		if value, exists := workflowData[field]; exists && value != nil {
			t.AppendRow(table.Row{
				text.FgYellow.Sprint(strings.ToLower(field)),
				text.FgHiWhite.Sprint(fmt.Sprintf("%v", value)),
			})
		}
	}

	t.Render()

	// Display Input Parameters if they exist
	f.displayWorkflowInputs(workflowData)

	// Display Steps if they exist
	f.displayWorkflowSteps(workflowData)

	return nil
}

// displayWorkflowInputs shows the input parameters in a readable format
func (f *TableFormatter) displayWorkflowInputs(workflowData map[string]interface{}) {
	inputSchemaFields := []string{"InputSchema", "inputSchema", "inputs", "parameters"}

	var inputSchema map[string]interface{}
	for _, field := range inputSchemaFields {
		if schema, exists := workflowData[field]; exists && schema != nil {
			if schemaMap, ok := schema.(map[string]interface{}); ok {
				inputSchema = schemaMap
				break
			}
		}
	}

	if inputSchema == nil {
		return
	}

	fmt.Printf("\n%s\n", text.FgHiCyan.Sprint("📝 Input Parameters:"))

	// Look for properties in the schema
	if properties, exists := inputSchema["properties"]; exists {
		if propsMap, ok := properties.(map[string]interface{}); ok {
			t := table.NewWriter()
			t.SetOutputMirror(os.Stdout)
			t.SetStyle(table.StyleRounded)
			t.AppendHeader(table.Row{
				text.FgHiCyan.Sprint("PARAMETER"),
				text.FgHiCyan.Sprint("TYPE"),
				text.FgHiCyan.Sprint("DESCRIPTION"),
				text.FgHiCyan.Sprint("REQUIRED"),
			})

			// Get required fields
			var required []string
			if reqField, exists := inputSchema["required"]; exists {
				if reqArray, ok := reqField.([]interface{}); ok {
					for _, req := range reqArray {
						if reqStr, ok := req.(string); ok {
							required = append(required, reqStr)
						}
					}
				}
			}

			// Sort parameter names
			var paramNames []string
			for paramName := range propsMap {
				paramNames = append(paramNames, paramName)
			}
			sort.Strings(paramNames)

			for _, paramName := range paramNames {
				if paramDef, ok := propsMap[paramName].(map[string]interface{}); ok {
					paramType := "string"
					if typ, exists := paramDef["type"]; exists {
						paramType = fmt.Sprintf("%v", typ)
					}

					description := "-"
					if desc, exists := paramDef["description"]; exists {
						description = fmt.Sprintf("%v", desc)
						if len(description) > 40 {
							description = description[:37] + "..."
						}
					}

					isRequired := "No"
					for _, req := range required {
						if req == paramName {
							isRequired = text.FgYellow.Sprint("Yes")
							break
						}
					}

					t.AppendRow(table.Row{
						text.FgHiWhite.Sprint(paramName),
						text.FgCyan.Sprint(paramType),
						description,
						isRequired,
					})
				}
			}

			t.Render()
		}
	}
}

// displayWorkflowSteps shows the workflow steps in a readable format
func (f *TableFormatter) displayWorkflowSteps(workflowData map[string]interface{}) {
	stepsFields := []string{"Steps", "steps", "actions"}

	var steps []interface{}
	for _, field := range stepsFields {
		if stepsData, exists := workflowData[field]; exists && stepsData != nil {
			if stepsArray, ok := stepsData.([]interface{}); ok {
				steps = stepsArray
				break
			}
		}
	}

	if len(steps) == 0 {
		return
	}

	fmt.Printf("\n%s\n", text.FgHiCyan.Sprint("🔄 Workflow Steps:"))

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.FgHiCyan.Sprint("STEP"),
		text.FgHiCyan.Sprint("TOOL"),
		text.FgHiCyan.Sprint("DESCRIPTION"),
	})

	for i, step := range steps {
		if stepMap, ok := step.(map[string]interface{}); ok {
			stepNum := fmt.Sprintf("%d", i+1)

			tool := "-"
			if toolName, exists := stepMap["Tool"]; exists {
				tool = fmt.Sprintf("%v", toolName)
				// Simplify tool name for display
				tool = f.builder.SimplifyToolName(tool)
			}

			description := "-"
			if desc, exists := stepMap["Description"]; exists && desc != nil {
				description = fmt.Sprintf("%v", desc)
			} else if id, exists := stepMap["ID"]; exists && id != nil {
				description = fmt.Sprintf("Execute %v", id)
			}

			if len(description) > 50 {
				description = description[:47] + "..."
			}

			t.AppendRow(table.Row{
				text.FgYellow.Sprint(stepNum),
				text.FgCyan.Sprint(tool),
				description,
			})
		}
	}

	t.Render()
}

// formatSimpleList formats an array of simple values
func (f *TableFormatter) formatSimpleList(data []interface{}) error {
	for _, item := range data {
		fmt.Println(item)
	}
	return nil
}

// Helper methods
func (f *TableFormatter) keyExists(data map[string]interface{}, key string) bool {
	_, exists := data[key]
	return exists
}

func (f *TableFormatter) getRemainingKeys(allKeys, usedKeys []string) []string {
	usedSet := make(map[string]bool)
	for _, key := range usedKeys {
		usedSet[key] = true
	}

	var remaining []string
	for _, key := range allKeys {
		if !usedSet[key] {
			remaining = append(remaining, key)
		}
	}
	return remaining
}

func (f *TableFormatter) containsString(slice []string, item string) bool {
	for _, i := range slice {
		if i == item {
			return true
		}
	}
	return false
}

func (f *TableFormatter) min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
