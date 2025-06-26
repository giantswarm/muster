package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// TableFormatter handles table creation and optimization for muster CLI output.
// It provides intelligent formatting for different types of data structures,
// automatically optimizing column layouts and applying consistent styling.
// The formatter can handle both simple arrays and complex nested objects,
// adapting the display format to provide the best user experience.
type TableFormatter struct {
	// options contains formatting preferences and execution settings
	options ExecutorOptions
	// builder provides cell-level formatting and styling utilities
	builder *TableBuilder
}

// NewTableFormatter creates a new table formatter with the specified options.
// The formatter uses the provided options to determine output behavior,
// including format selection and verbosity settings.
//
// Parameters:
//   - options: Configuration options for formatting behavior
//
// Returns:
//   - *TableFormatter: Configured table formatter ready for use
func NewTableFormatter(options ExecutorOptions) *TableFormatter {
	return &TableFormatter{
		options: options,
		builder: NewTableBuilder(),
	}
}

// FormatData formats data according to its type and structure.
// It intelligently handles different data types including objects, arrays,
// and simple values, applying the most appropriate formatting strategy
// for optimal readability and information density.
//
// Parameters:
//   - data: The data to format (can be object, array, or simple value)
//
// Returns:
//   - error: Formatting error, if any
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

// formatTableFromObject handles object data that might contain arrays.
// It looks for common wrapper patterns and extracts array data for table
// display, or formats the object as key-value pairs if no arrays are found.
//
// Parameters:
//   - data: Object data to format
//
// Returns:
//   - error: Formatting error, if any
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

// findArrayKey looks for common array keys in wrapped objects.
// Many muster API responses wrap arrays in objects with predictable key names.
// This function identifies those patterns to extract the relevant data.
//
// Parameters:
//   - data: Object data to search for array keys
//
// Returns:
//   - string: The key name containing array data, or empty string if none found
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

// formatTableFromArray creates a professional table from an array of objects.
// It automatically optimizes column selection, applies consistent formatting,
// sorts data for better readability, and adds summary information with
// appropriate icons and styling.
//
// Parameters:
//   - data: Array of objects to display as a table
//
// Returns:
//   - error: Formatting error, if any
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

// optimizeColumns determines the best columns to show based on the data type.
// It analyzes the data structure and selects the most relevant columns for
// display, prioritizing key fields and limiting the total number of columns
// to prevent layout issues. Different resource types get specialized column
// selection logic.
//
// Parameters:
//   - sample: Sample data object used to determine available columns
//
// Returns:
//   - []string: Optimized list of column names for table display
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

// detectResourceType attempts to determine what type of muster resource this is.
// It analyzes the object structure and field names to identify the resource
// type, which is used for specialized formatting and column optimization.
//
// Parameters:
//   - sample: Sample data object to analyze
//
// Returns:
//   - string: Detected resource type (services, workflows, etc.) or "generic"
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

// formatKeyValueTable formats an object as key-value pairs.
// This is used for single objects or complex data that doesn't fit well
// in array-based tables. It provides a clean property-value layout.
//
// Parameters:
//   - data: Object data to format as key-value pairs
//
// Returns:
//   - error: Formatting error, if any
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

// isWorkflowData checks if the data represents a workflow.
// It analyzes the object structure to identify workflow-specific patterns
// and fields, enabling specialized formatting for workflow data.
//
// Parameters:
//   - data: Object data to check for workflow characteristics
//
// Returns:
//   - bool: true if the data appears to be workflow-related
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

// formatWorkflowDetails provides a clean, readable format for workflow data.
// It creates a specialized layout for workflows, showing basic information,
// input parameters, and workflow steps in an organized and readable format.
//
// Parameters:
//   - data: Workflow data to format
//
// Returns:
//   - error: Formatting error, if any
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

// displayWorkflowInputs shows the input parameters in a readable format.
// It extracts input schema information and displays parameter details
// including types, descriptions, and requirement status in a structured table.
//
// Parameters:
//   - workflowData: Workflow data containing input schema information
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

// displayWorkflowSteps shows the workflow steps in a readable format.
// It extracts workflow steps and displays them in a sequential table
// showing step numbers, tools used, and descriptions for easy understanding.
//
// Parameters:
//   - workflowData: Workflow data containing steps information
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

// formatSimpleList formats an array of simple values.
// This handles arrays that contain primitive values rather than objects,
// displaying each value on a separate line.
//
// Parameters:
//   - data: Array of simple values to display
//
// Returns:
//   - error: Formatting error, if any
func (f *TableFormatter) formatSimpleList(data []interface{}) error {
	for _, item := range data {
		fmt.Println(item)
	}
	return nil
}

// Helper methods

// keyExists checks if a key exists in a map.
// This is a utility function used throughout the formatter for safe key access.
//
// Parameters:
//   - data: Map to check for key existence
//   - key: Key name to look for
//
// Returns:
//   - bool: true if the key exists in the map
func (f *TableFormatter) keyExists(data map[string]interface{}, key string) bool {
	_, exists := data[key]
	return exists
}

// getRemainingKeys returns keys that haven't been used yet.
// This helps in column optimization by identifying available but unused columns.
//
// Parameters:
//   - allKeys: Complete list of available keys
//   - usedKeys: Keys that have already been selected
//
// Returns:
//   - []string: List of remaining unused keys
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

// containsString checks if a string slice contains a specific item.
// This is a utility function for string slice operations.
//
// Parameters:
//   - slice: String slice to search in
//   - item: Item to look for
//
// Returns:
//   - bool: true if the item is found in the slice
func (f *TableFormatter) containsString(slice []string, item string) bool {
	for _, i := range slice {
		if i == item {
			return true
		}
	}
	return false
}

// min returns the smaller of two integers.
// This is a utility function for integer comparisons.
//
// Parameters:
//   - a, b: Integers to compare
//
// Returns:
//   - int: The smaller of the two values
func (f *TableFormatter) min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
