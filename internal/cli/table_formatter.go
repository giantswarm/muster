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
// Args:
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
// Args:
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
// Args:
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
// Args:
//   - data: Object data to search for array keys
//
// Returns:
//   - string: The key name containing array data, or empty string if none found
func (f *TableFormatter) findArrayKey(data map[string]interface{}) string {
	arrayKeys := []string{"services", "serviceClasses", "mcpServers", "workflows", "executions", "capabilities", "items", "results", "tools", "resources", "prompts"}

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
// Args:
//   - data: Array of objects to display as a table
//
// Returns:
//   - error: Formatting error, if any
func (f *TableFormatter) formatTableFromArray(data []interface{}) error {
	if len(data) == 0 {
		fmt.Printf("%s %s\n",
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("📋"),
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("No items found"))
		return nil
	}

	// Get the first object to determine columns
	firstObj, ok := data[0].(map[string]interface{})
	if !ok {
		// Array of simple values
		return f.formatSimpleList(data)
	}

	// Determine table type and optimize columns
	columns := f.optimizeColumns(data)
	resourceType := f.detectResourceType(firstObj)

	// Create professional table
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)

	// Set headers with colors and icons
	headers := make([]interface{}, len(columns))
	for i, col := range columns {
		headers[i] = text.Colors{text.FgHiBlue, text.Bold}.Sprint(strings.ToUpper(col))
	}
	t.AppendHeader(headers)

	// Add rows with enhanced formatting - sort by name field if present
	sortedData := f.builder.SortDataByName(data, columns)
	for _, item := range sortedData {
		if itemMap, ok := item.(map[string]interface{}); ok {
			row := make([]interface{}, len(columns))
			for i, col := range columns {
				// Use context-aware formatting for MCP servers
				if resourceType == "mcpServers" || resourceType == "mcpServer" {
					row[i] = f.builder.FormatCellValueWithContext(col, itemMap[col], itemMap)
				} else {
					row[i] = f.builder.FormatCellValue(col, itemMap[col])
				}
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
		text.Bold.Sprint(len(data)),
		text.FgHiBlue.Sprint(resourceName))

	return nil
}

// optimizeColumns determines the best columns to show based on the data type.
// It analyzes the data structure and selects the most relevant columns for
// display, prioritizing key fields and limiting the total number of columns
// to prevent layout issues. Different resource types get specialized column
// selection logic.
//
// Args:
//   - objects: Objects used to determine available columns
//
// Returns:
//   - []string: Optimized list of column names for table display
func (f *TableFormatter) optimizeColumns(objects []interface{}) []string {
	// Extract all available keys
	var allKeys []string
	for _, obj := range objects {
		castObj, ok := obj.(map[string]interface{})
		if !ok {
			continue
		}
		for key := range castObj {
			allKeys = append(allKeys, key)
		}
	}
	sort.Strings(allKeys)

	sample := objects[0].(map[string]interface{})

	// Always prioritize name/ID fields first
	nameFields := []string{"name", "label", "id", "workflow"}
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
		"mcpServers":     {"type", "description", "autoStart"},
		"workflows":      {"status", "description", "steps"},
		"executions":     {"workflow_name", "status", "started_at", "duration_ms"},
		"event":          {"timestamp", "type", "resource_type", "resource_name", "reason", "message"},
		"mcpTool":        {"description"},
		"mcpResource":    {"uri", "description", "mimeType"},
		"mcpPrompt":      {"description"},
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
	var maxColumns int
	switch resourceType {
	case "serviceClasses":
		maxColumns = 5 // More conservative for wider data
	case "mcpServers":
		maxColumns = 4 // Exactly the columns we want: name, type, description, autoStart
	case "event":
		maxColumns = 6 // Allow more columns for events
	default:
		maxColumns = 6
	}

	// Add remaining columns alphabetically if we have space
	if len(columns) < maxColumns {
		remaining := f.getRemainingKeys(allKeys, columns)

		// Filter out unwanted columns for MCP servers
		if resourceType == "mcpServers" {
			filteredRemaining := []string{}
			unwantedColumns := []string{"args", "command", "url", "env", "headers", "timeout", "toolPrefix", "error"}
			for _, key := range remaining {
				isUnwanted := false
				for _, unwanted := range unwantedColumns {
					if strings.ToLower(key) == strings.ToLower(unwanted) {
						isUnwanted = true
						break
					}
				}
				if !isUnwanted {
					filteredRemaining = append(filteredRemaining, key)
				}
			}
			remaining = filteredRemaining
		}

		spaceLeft := maxColumns - len(columns)
		if spaceLeft > 0 && len(remaining) > 0 {
			addCount := f.min(spaceLeft, len(remaining))
			columns = append(columns, remaining[:addCount]...)
		}
	}

	return columns
}

// detectResourceType analyzes a sample object to determine what type of resource it represents.
// This helps optimize the column selection and formatting for different resource types.
//
// Args:
//   - sample: Sample object to analyze for type detection
//
// Returns:
//   - string: Detected resource type for optimization purposes
func (f *TableFormatter) detectResourceType(sample map[string]interface{}) string {
	// Check for specific field combinations to identify resource types
	if f.keyExists(sample, "timestamp") && f.keyExists(sample, "reason") && f.keyExists(sample, "resource_type") {
		return "event"
	}
	if f.keyExists(sample, "health") && f.keyExists(sample, "state") {
		return "service"
	}
	if f.keyExists(sample, "workflow_name") && f.keyExists(sample, "started_at") {
		return "execution"
	}
	// Check for MCP tools - has inputSchema and name but NOT uri
	if f.keyExists(sample, "inputSchema") && f.keyExists(sample, "name") && !f.keyExists(sample, "uri") {
		return "mcpTool"
	}
	// Check for MCP resources - has uri field
	if f.keyExists(sample, "uri") && (f.keyExists(sample, "mimeType") || f.keyExists(sample, "name")) {
		return "mcpResource"
	}
	// Check for MCP prompts - has arguments field but NOT inputSchema or steps
	if f.keyExists(sample, "arguments") && !f.keyExists(sample, "inputSchema") && !f.keyExists(sample, "steps") {
		return "mcpPrompt"
	}
	// Check for MCP servers by looking at the type field value
	if f.keyExists(sample, "type") {
		if typeVal, ok := sample["type"].(string); ok {
			if typeVal == "stdio" || typeVal == "streamable-http" || typeVal == "sse" {
				return "mcpServers"
			}
		}
	}
	if f.keyExists(sample, "available") && f.keyExists(sample, "serviceType") {
		return "serviceClass"
	}
	if f.keyExists(sample, "serverType") {
		return "mcpServer"
	}
	// Check for workflow-related fields
	if f.keyExists(sample, "steps") || f.keyExists(sample, "workflow") ||
		(f.keyExists(sample, "name") && f.keyExists(sample, "available") && f.keyExists(sample, "description")) {
		return "workflows"
	}

	return "generic"
}

// formatKeyValueTable formats an object as key-value pairs.
// This is used for single objects or complex data that doesn't fit well
// in array-based tables. It provides a clean property-value layout.
//
// Args:
//   - data: Object data to format as key-value pairs
//
// Returns:
//   - error: Formatting error, if any
func (f *TableFormatter) formatKeyValueTable(data map[string]interface{}) error {
	// Check if this is workflow data and handle it specially
	if f.isWorkflowData(data) {
		return f.formatWorkflowDetails(data)
	}

	// Check if this is MCP server data to apply contextual labels
	isMCPServer := f.isMCPServerData(data)
	serverType := ""
	if isMCPServer {
		serverType = f.getServerType(data)
	}

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("PROPERTY"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("VALUE"),
	})

	// Sort keys for consistent output
	var keys []string
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		// Apply contextual formatting for MCP servers
		var value interface{}
		displayKey := key

		if isMCPServer {
			value = f.builder.FormatCellValueWithContext(key, data[key], data)

			// Use contextual property names for remote servers
			if IsRemoteServerType(serverType) {
				switch strings.ToLower(key) {
				case "autostart":
					displayKey = "autoConnect"
				}
			}
		} else {
			value = f.builder.FormatCellValue(key, data[key])
		}

		t.AppendRow(table.Row{
			text.Colors{text.FgHiYellow, text.Bold}.Sprint(displayKey),
			value,
		})
	}

	t.Render()
	return nil
}

// isMCPServerData checks if the data represents an MCP server.
// Delegates to the shared IsMCPServerData helper for consistent behavior.
func (f *TableFormatter) isMCPServerData(data map[string]interface{}) bool {
	return IsMCPServerData(data)
}

// getServerType extracts the server type from MCP server data.
// Delegates to the shared ExtractServerType helper for consistent behavior.
func (f *TableFormatter) getServerType(data map[string]interface{}) string {
	return ExtractServerType(data)
}

// isWorkflowData checks if the data represents a workflow.
// It analyzes the object structure to identify workflow-specific patterns
// and fields, enabling specialized formatting for workflow data.
//
// Args:
//   - data: Object data to check for workflow characteristics
//
// Returns:
//   - bool: true if the data appears to be workflow-related
func (f *TableFormatter) isWorkflowData(data map[string]interface{}) bool {
	// Check for workflow execution result first
	if f.isWorkflowExecutionResult(data) {
		return true
	}

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

// isWorkflowExecutionResult checks if the data represents a workflow execution result
func (f *TableFormatter) isWorkflowExecutionResult(data map[string]interface{}) bool {
	// Check for execution-specific fields
	hasExecutionID := f.keyExists(data, "execution_id")
	hasStatus := f.keyExists(data, "status")
	hasResults := f.keyExists(data, "results")
	hasInput := f.keyExists(data, "input")

	return hasExecutionID && hasStatus && (hasResults || hasInput)
}

// formatWorkflowDetails provides a clean, readable format for workflow data.
// It creates a specialized layout for workflows, showing basic information,
// input args, and workflow steps in an organized and readable format.
//
// Args:
//   - data: Workflow data to format
//
// Returns:
//   - error: Formatting error, if any
func (f *TableFormatter) formatWorkflowDetails(data map[string]interface{}) error {
	// Check if this is a workflow execution result
	if f.isWorkflowExecutionResult(data) {
		return f.formatWorkflowExecutionResult(data)
	}

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
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("PROPERTY"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("VALUE"),
	})

	// Display basic workflow information
	basicFields := []string{"Name", "name", "Description", "description", "Version", "version"}
	for _, field := range basicFields {
		if value, exists := workflowData[field]; exists && value != nil {
			t.AppendRow(table.Row{
				text.Colors{text.FgHiYellow, text.Bold}.Sprint(strings.ToLower(field)),
				text.Bold.Sprint(fmt.Sprintf("%v", value)),
			})
		}
	}

	t.Render()

	// Display Input Args if they exist
	f.displayWorkflowInputs(workflowData)

	// Display Steps if they exist
	f.displayWorkflowSteps(workflowData)

	return nil
}

// formatWorkflowExecutionResult formats workflow execution results in a user-friendly way
func (f *TableFormatter) formatWorkflowExecutionResult(data map[string]interface{}) error {
	// Main execution info
	fmt.Printf("%s %s\n",
		text.Colors{text.FgHiGreen, text.Bold}.Sprint("✅"),
		text.FgHiGreen.Sprint("Workflow Execution Completed"))
	fmt.Println()

	// Create execution summary table
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("PROPERTY"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("VALUE"),
	})

	// Add execution details
	if executionID, exists := data["execution_id"]; exists {
		t.AppendRow(table.Row{
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("execution_id"),
			text.Bold.Sprint(fmt.Sprintf("%v", executionID)),
		})
	}

	if workflow, exists := data["workflow"]; exists {
		t.AppendRow(table.Row{
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("workflow"),
			text.Bold.Sprint(fmt.Sprintf("%v", workflow)),
		})
	}

	if status, exists := data["status"]; exists {
		statusStr := fmt.Sprintf("%v", status)
		var statusDisplay string
		switch strings.ToLower(statusStr) {
		case "completed":
			statusDisplay = text.Colors{text.FgHiGreen, text.Bold}.Sprint("✅ " + statusStr)
		case "failed":
			statusDisplay = text.Colors{text.FgHiRed, text.Bold}.Sprint("❌ " + statusStr)
		case "running":
			statusDisplay = text.Colors{text.FgHiYellow, text.Bold}.Sprint("⏳ " + statusStr)
		default:
			statusDisplay = text.Bold.Sprint(statusStr)
		}
		t.AppendRow(table.Row{
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("status"),
			statusDisplay,
		})
	}

	t.Render()

	// Show input parameters if they exist
	if input, exists := data["input"]; exists {
		if inputMap, ok := input.(map[string]interface{}); ok && len(inputMap) > 0 {
			fmt.Printf("\n%s\n", text.Colors{text.FgHiBlue, text.Bold}.Sprint("📝 Input Parameters:"))

			inputTable := table.NewWriter()
			inputTable.SetOutputMirror(os.Stdout)
			inputTable.SetStyle(table.StyleRounded)
			inputTable.AppendHeader(table.Row{
				text.Colors{text.FgHiBlue, text.Bold}.Sprint("PARAMETER"),
				text.Colors{text.FgHiBlue, text.Bold}.Sprint("VALUE"),
			})

			// Sort parameters for consistent display
			var paramNames []string
			for paramName := range inputMap {
				paramNames = append(paramNames, paramName)
			}
			sort.Strings(paramNames)

			for _, paramName := range paramNames {
				value := inputMap[paramName]
				inputTable.AppendRow(table.Row{
					text.Colors{text.FgHiYellow, text.Bold}.Sprint(paramName),
					text.Bold.Sprint(fmt.Sprintf("%v", value)),
				})
			}

			inputTable.Render()
		}
	}

	// Show step results if they exist
	if results, exists := data["results"]; exists {
		if resultsMap, ok := results.(map[string]interface{}); ok && len(resultsMap) > 0 {
			fmt.Printf("\n%s\n", text.Colors{text.FgHiBlue, text.Bold}.Sprint("🔄 Step Results:"))

			stepTable := table.NewWriter()
			stepTable.SetOutputMirror(os.Stdout)
			stepTable.SetStyle(table.StyleRounded)
			stepTable.AppendHeader(table.Row{
				text.Colors{text.FgHiBlue, text.Bold}.Sprint("STEP"),
				text.Colors{text.FgHiBlue, text.Bold}.Sprint("STATUS"),
				text.Colors{text.FgHiBlue, text.Bold}.Sprint("DETAILS"),
			})

			// Sort step names for consistent display
			var stepNames []string
			for stepName := range resultsMap {
				stepNames = append(stepNames, stepName)
			}
			sort.Strings(stepNames)

			for _, stepName := range stepNames {
				stepResult := resultsMap[stepName]
				if stepMap, ok := stepResult.(map[string]interface{}); ok {
					status := "unknown"
					details := "-"

					if stepStatus, exists := stepMap["status"]; exists {
						status = fmt.Sprintf("%v", stepStatus)
					}

					// Try to extract meaningful details from the result
					if result, exists := stepMap["result"]; exists {
						if resultMap, ok := result.(map[string]interface{}); ok {
							if name, exists := resultMap["name"]; exists {
								details = fmt.Sprintf("Created: %v", name)
							} else if len(resultMap) > 0 {
								details = fmt.Sprintf("%d properties", len(resultMap))
							}
						} else {
							details = fmt.Sprintf("%v", result)
							if len(details) > 50 {
								details = details[:47] + "..."
							}
						}
					}

					// Format status with color
					var statusDisplay string
					switch strings.ToLower(status) {
					case "completed":
						statusDisplay = text.Colors{text.FgHiGreen, text.Bold}.Sprint("✅ " + status)
					case "failed":
						statusDisplay = text.Colors{text.FgHiRed, text.Bold}.Sprint("❌ " + status)
					case "skipped":
						statusDisplay = text.Colors{text.FgHiYellow, text.Bold}.Sprint("⏭️ " + status)
					default:
						statusDisplay = text.Bold.Sprint(status)
					}

					stepTable.AppendRow(table.Row{
						text.Bold.Sprint(stepName),
						statusDisplay,
						text.Colors{text.FgHiCyan, text.Bold}.Sprint(details),
					})
				}
			}

			stepTable.Render()
		}
	}

	return nil
}

// displayWorkflowInputs shows the input args in a readable format.
// It extracts input schema information and displays arg details
// including types, descriptions, and requirement status in a structured table.
//
// Args:
//   - workflowData: Workflow data containing input schema information
func (f *TableFormatter) displayWorkflowInputs(workflowData map[string]interface{}) {
	// First try to get args in the muster workflow format
	var argsData map[string]interface{}
	if args, exists := workflowData["args"]; exists && args != nil {
		if argsMap, ok := args.(map[string]interface{}); ok {
			argsData = argsMap
		}
	}

	// Fallback to legacy input schema formats if args not found
	if argsData == nil {
		inputSchemaFields := []string{"InputSchema", "inputSchema", "inputs"}
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

		// Look for properties in the schema (legacy format)
		if properties, exists := inputSchema["properties"]; exists {
			if propsMap, ok := properties.(map[string]interface{}); ok {
				argsData = propsMap
			}
		}
	}

	if argsData == nil || len(argsData) == 0 {
		return
	}

	fmt.Printf("\n%s\n", text.Colors{text.FgHiBlue, text.Bold}.Sprint("📝 Input Args:"))

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("ARGUMENT"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("TYPE"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("DESCRIPTION"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("REQUIRED"),
	})

	// Sort arg names
	var paramNames []string
	for paramName := range argsData {
		paramNames = append(paramNames, paramName)
	}
	sort.Strings(paramNames)

	for _, paramName := range paramNames {
		paramDef, ok := argsData[paramName].(map[string]interface{})
		if !ok {
			continue
		}

		// Handle muster workflow ArgDefinition format
		paramType := "string"
		if typ, exists := paramDef["type"]; exists {
			paramType = fmt.Sprintf("%v", typ)
		}

		description := "-"
		if desc, exists := paramDef["description"]; exists && desc != nil {
			description = fmt.Sprintf("%v", desc)
			if len(description) > 40 {
				description = description[:37] + "..."
			}
		}

		isRequired := "No"
		if req, exists := paramDef["required"]; exists {
			if reqBool, ok := req.(bool); ok && reqBool {
				isRequired = text.Colors{text.FgHiYellow, text.Bold}.Sprint("Yes")
			}
		}

		// Show default value if available
		defaultValue := ""
		if def, exists := paramDef["default"]; exists && def != nil {
			defaultValue = fmt.Sprintf(" (default: %v)", def)
		}

		t.AppendRow(table.Row{
			text.Bold.Sprint(paramName),
			text.Colors{text.FgHiCyan, text.Bold}.Sprint(paramType),
			description + defaultValue,
			isRequired,
		})
	}

	t.Render()
}

// displayWorkflowSteps shows the workflow steps in a readable format.
// It extracts workflow steps and displays them in a sequential table
// showing step numbers, tools used, and descriptions for easy understanding.
//
// Args:
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

	fmt.Printf("\n%s\n", text.Colors{text.FgHiBlue, text.Bold}.Sprint("🔄 Workflow Steps:"))

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("STEP"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("TOOL"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("DESCRIPTION"),
	})

	for i, step := range steps {
		if stepMap, ok := step.(map[string]interface{}); ok {
			stepNum := fmt.Sprintf("%d", i+1)

			tool := "-"
			// Check for common tool field names
			toolFields := []string{"Tool", "tool", "action", "Action", "command", "Command"}
			for _, field := range toolFields {
				if toolName, exists := stepMap[field]; exists && toolName != nil {
					tool = fmt.Sprintf("%v", toolName)
					tool = f.builder.SimplifyToolName(tool)
					break
				}
			}

			description := "-"
			// Check for common description field names
			descFields := []string{"Description", "description", "name", "Name", "title", "Title"}
			for _, field := range descFields {
				if desc, exists := stepMap[field]; exists && desc != nil {
					description = fmt.Sprintf("%v", desc)
					break
				}
			}
			// If no description found, try ID fields as fallback
			if description == "-" {
				idFields := []string{"ID", "id", "step_id", "stepId"}
				for _, field := range idFields {
					if id, exists := stepMap[field]; exists && id != nil {
						description = fmt.Sprintf("Execute %v", id)
						break
					}
				}
			}

			if len(description) > 50 {
				description = description[:47] + "..."
			}

			t.AppendRow(table.Row{
				text.Colors{text.FgHiYellow, text.Bold}.Sprint(stepNum),
				text.Colors{text.FgHiCyan, text.Bold}.Sprint(tool),
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
// Args:
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
// Args:
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
// Args:
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
// Args:
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
// Args:
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
