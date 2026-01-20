package cli

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
)

// unwantedColumnsByResourceType defines columns that should be excluded from table
// display in non-wide mode for each resource type. This keeps list views clean
// and focused on the most useful information.
//
// Exclusion rationale for mcpServers:
//   - args, command, url, env, headers: Configuration details, use `muster server get <name>` for full info
//   - timeout, toolPrefix: Minor config, not useful in list view
//   - error: Raw error messages; statusMessage provides user-friendly version
//   - description: Can be long, use detail view for this
//   - auth: Nested config; state already shows auth status
//   - health: Cleared for non-connected servers, not useful in list
//   - statusMessage: Shown in footer notes instead of column
//   - consecutiveFailures, lastAttempt, nextRetryAfter: Diagnostic fields for verbose/debug use
var unwantedColumnsByResourceType = map[string][]string{
	"mcpServers": {
		"args", "command", "url", "env", "headers", "timeout", "toolPrefix",
		"error", "description", "auth", "health", "statusMessage",
		"consecutiveFailures", "lastAttempt", "nextRetryAfter",
	},
	"mcpServer": {
		"args", "command", "url", "env", "headers", "timeout", "toolPrefix",
		"error", "description", "auth", "health", "statusMessage",
		"consecutiveFailures", "lastAttempt", "nextRetryAfter",
	},
	"service": {
		"metadata", // Nested data doesn't display well in list view
	},
	"services": {
		"metadata", // Nested data doesn't display well in list view
	},
}

// filterUnwantedColumns filters out columns that should not be displayed in table view.
// The comparison is case-insensitive to handle JSON field name variations.
func filterUnwantedColumns(columns []string, unwanted []string) []string {
	filtered := make([]string, 0, len(columns))
	for _, col := range columns {
		isUnwanted := false
		for _, u := range unwanted {
			if strings.EqualFold(col, u) {
				isUnwanted = true
				break
			}
		}
		if !isUnwanted {
			filtered = append(filtered, col)
		}
	}
	return filtered
}

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
		value := data[arrayKey]
		// Handle nil as empty array
		if value == nil {
			f.printFooterMessages(data, arrayKey)
			return f.formatEmptyList(arrayKey)
		}
		if arr, ok := value.([]interface{}); ok {
			// Format the array first
			if err := f.formatTableFromArrayWithMeta(arr, data, arrayKey); err != nil {
				return err
			}
			// Print footer messages (hints about hidden servers, status messages)
			f.printFooterMessages(data, arrayKey)
			return nil
		}
	}

	// No array found, format as key-value pairs
	return f.formatKeyValueTable(data)
}

// formatTableFromArrayWithMeta creates a kubectl-style table and collects status messages.
func (f *TableFormatter) formatTableFromArrayWithMeta(data []interface{}, meta map[string]interface{}, resourceType string) error {
	if len(data) == 0 {
		fmt.Println("No items found")
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
	detectedType := f.detectResourceType(firstObj)

	// Create kubectl-style plain table
	tw := NewPlainTableWriter(os.Stdout)
	tw.SetHeaders(columns)
	tw.SetNoHeaders(f.options.NoHeaders)

	// Add rows with formatting - sort by name field if present
	sortedData := f.builder.SortDataByName(data, columns)
	for _, item := range sortedData {
		if itemMap, ok := item.(map[string]interface{}); ok {
			row := make([]string, len(columns))
			for i, col := range columns {
				// Use context-aware formatting for MCP servers (plain text version)
				if detectedType == "mcpServers" || detectedType == "mcpServer" {
					row[i] = f.builder.FormatCellValuePlain(col, itemMap[col], itemMap)
				} else {
					row[i] = f.builder.FormatCellValuePlain(col, itemMap[col], nil)
				}
			}
			tw.AppendRow(row)
		}
	}

	tw.Render()
	return nil
}

// printFooterMessages prints helpful footer messages based on response metadata.
// This includes hints about hidden servers and actionable status messages.
func (f *TableFormatter) printFooterMessages(data map[string]interface{}, resourceType string) {
	// Skip footer messages if we're in JSON/YAML mode or --no-headers mode
	if f.options.Format == OutputFormatJSON || f.options.Format == OutputFormatYAML {
		return
	}

	// Check for hint about hidden unreachable servers
	if hint, exists := data["hint"]; exists {
		if hintStr, ok := hint.(string); ok && hintStr != "" {
			fmt.Printf("\n%s\n", hintStr)
		}
	}

	// Check for status messages from servers in error states (for mcpServers)
	if resourceType == "mcpServers" {
		f.printServerStatusNotes(data)
	}
}

// printServerStatusNotes prints actionable notes for servers requiring attention.
func (f *TableFormatter) printServerStatusNotes(data map[string]interface{}) {
	servers, ok := data["mcpServers"].([]interface{})
	if !ok || len(servers) == 0 {
		return
	}

	// Collect servers with actionable status messages
	var notes []string
	for _, server := range servers {
		serverMap, ok := server.(map[string]interface{})
		if !ok {
			continue
		}

		name, _ := serverMap["name"].(string)
		statusMessage, _ := serverMap["statusMessage"].(string)
		state, _ := serverMap["state"].(string)

		// Only show notes for servers that need attention
		if statusMessage != "" && name != "" {
			switch state {
			case "auth_required", "unreachable", "failed":
				notes = append(notes, fmt.Sprintf("  %s: %s", name, statusMessage))
			}
		}
	}

	// Print collected notes
	if len(notes) > 0 {
		fmt.Println("\nServers requiring attention:")
		for _, note := range notes {
			fmt.Println(note)
		}
	}
}

// findArrayKey looks for common array keys in wrapped objects.
// Many muster API responses wrap arrays in objects with predictable key names.
// This function identifies those patterns to extract the relevant data.
// It also handles nil/null values which represent empty arrays.
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
			// Handle both actual arrays and nil values (which represent empty arrays)
			if _, isArray := value.([]interface{}); isArray {
				return key
			}
			// Treat nil as an empty array
			if value == nil {
				return key
			}
		}
	}
	return ""
}

// formatTableFromArray creates a kubectl-style plain table from an array of objects.
// It automatically optimizes column selection, sorts data for better readability,
// and uses clean columnar output without box-drawing characters.
//
// Args:
//   - data: Array of objects to display as a table
//
// Returns:
//   - error: Formatting error, if any
func (f *TableFormatter) formatTableFromArray(data []interface{}) error {
	if len(data) == 0 {
		fmt.Println("No items found")
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

	// Create kubectl-style plain table
	tw := NewPlainTableWriter(os.Stdout)
	tw.SetHeaders(columns)
	tw.SetNoHeaders(f.options.NoHeaders)

	// Add rows with formatting - sort by name field if present
	sortedData := f.builder.SortDataByName(data, columns)
	for _, item := range sortedData {
		if itemMap, ok := item.(map[string]interface{}); ok {
			row := make([]string, len(columns))
			for i, col := range columns {
				// Use context-aware formatting for MCP servers (plain text version)
				if resourceType == "mcpServers" || resourceType == "mcpServer" {
					row[i] = f.builder.FormatCellValuePlain(col, itemMap[col], itemMap)
				} else {
					row[i] = f.builder.FormatCellValuePlain(col, itemMap[col], nil)
				}
			}
			tw.AppendRow(row)
		}
	}

	tw.Render()
	return nil
}

// isWideMode returns true if the formatter is configured for wide output.
func (f *TableFormatter) isWideMode() bool {
	return f.options.Format == OutputFormatWide
}

// optimizeColumns determines the best columns to show based on the data type.
// It analyzes the data structure and selects the most relevant columns for
// display, prioritizing key fields and limiting the total number of columns
// to prevent layout issues. Different resource types get specialized column
// selection logic. When wide mode is enabled (-o wide), additional columns
// are included.
//
// Args:
//   - objects: Objects used to determine available columns
//
// Returns:
//   - []string: Optimized list of column names for table display
func (f *TableFormatter) optimizeColumns(objects []interface{}) []string {
	// Extract all available keys (deduplicated)
	keySet := make(map[string]bool)
	for _, obj := range objects {
		castObj, ok := obj.(map[string]interface{})
		if !ok {
			continue
		}
		for key := range castObj {
			keySet[key] = true
		}
	}
	var allKeys []string
	for key := range keySet {
		allKeys = append(allKeys, key)
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
		"service":        {"health", "state", "service_type"},
		"services":       {"health", "state", "service_type"},
		"serviceClasses": {"available", "serviceType", "description", "requiredTools"},
		"serviceClass":   {"available", "serviceType", "description", "requiredTools"},
		"mcpServers":     {"state", "type"},
		"mcpServer":      {"state", "type"},
		"workflows":      {"status", "description", "steps"},
		"workflow":       {"status", "description", "steps"},
		"executions":     {"workflow_name", "status", "started_at", "duration_ms"},
		"execution":      {"workflow_name", "status", "started_at", "duration_ms"},
		"event":          {"timestamp", "type", "resource_type", "resource_name", "reason", "message"},
		"mcpTool":        {"description"},
		"mcpResource":    {"uri", "description", "mimeType"},
		"mcpPrompt":      {"description"},
		"generic":        {"status", "type", "description", "available"},
	}

	// Extended columns for wide mode (-o wide)
	wideColumns := map[string][]string{
		"service":        {"endpoint", "tools"},
		"services":       {"endpoint", "tools"},
		"serviceClasses": {"requiredTools"},
		"serviceClass":   {"requiredTools"},
		"mcpServers":     {"url", "command", "timeout"},
		"mcpServer":      {"url", "command", "timeout"},
		"workflows":      {"args"},
		"workflow":       {"args"},
		"executions":     {"completed_at"},
		"execution":      {"completed_at"},
		"event":          {"message"},
		"mcpTool":        {"inputSchema"},
		"mcpResource":    {"mimeType"},
		"mcpPrompt":      {"arguments"},
		"generic":        {"metadata"},
	}

	// Detect resource type and use optimized columns
	resourceType := f.detectResourceType(sample)
	if priorities, exists := priorityColumns[resourceType]; exists {
		// Add priority columns that exist (and haven't been added yet)
		for _, col := range priorities {
			if f.keyExists(sample, col) && !slices.Contains(columns, col) {
				columns = append(columns, col)
			}
		}
	}

	// Add wide columns if in wide mode
	if f.isWideMode() {
		if wideCols, exists := wideColumns[resourceType]; exists {
			for _, col := range wideCols {
				if f.keyExists(sample, col) && !slices.Contains(columns, col) {
					columns = append(columns, col)
				}
			}
		}
	}

	// For complex resource types, limit columns to prevent wrapping (in non-wide mode)
	var maxColumns int
	if f.isWideMode() {
		// In wide mode, allow more columns
		maxColumns = 10
	} else {
		switch resourceType {
		case "service", "services":
			maxColumns = 4 // name, health, state, service_type
		case "serviceClasses", "serviceClass":
			maxColumns = 5 // More conservative for wider data
		case "mcpServers", "mcpServer":
			maxColumns = 4 // name, type, autoStart
		case "event":
			maxColumns = 6 // Allow more columns for events
		default:
			maxColumns = 6
		}
	}

	// Add remaining columns alphabetically if we have space
	if len(columns) < maxColumns {
		remaining := f.getRemainingKeys(allKeys, columns)

		// Filter out unwanted columns based on resource type (in non-wide mode only)
		filteredRemaining := remaining
		if !f.isWideMode() {
			if unwantedColumns, exists := unwantedColumnsByResourceType[resourceType]; exists {
				filteredRemaining = filterUnwantedColumns(remaining, unwantedColumns)
			}
		}

		spaceLeft := maxColumns - len(columns)
		if spaceLeft > 0 && len(filteredRemaining) > 0 {
			addCount := f.min(spaceLeft, len(filteredRemaining))
			columns = append(columns, filteredRemaining[:addCount]...)
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

	// Create kubectl-style plain table
	tw := NewPlainTableWriter(os.Stdout)
	tw.SetHeaders([]string{"PROPERTY", "VALUE"})
	tw.SetNoHeaders(f.options.NoHeaders)

	// Sort keys for consistent output
	var keys []string
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		// Apply contextual formatting for MCP servers
		var value string
		displayKey := key

		if isMCPServer {
			value = f.builder.FormatCellValuePlain(key, data[key], data)

			// Use contextual property names for remote servers
			if IsRemoteServerType(serverType) {
				switch strings.ToLower(key) {
				case "autostart":
					displayKey = "autoConnect"
				}
			}
		} else {
			value = f.builder.FormatCellValuePlain(key, data[key], nil)
		}

		tw.AppendRow([]string{displayKey, value})
	}

	tw.Render()
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

	// Create kubectl-style plain table for main info
	tw := NewPlainTableWriter(os.Stdout)
	tw.SetHeaders([]string{"PROPERTY", "VALUE"})
	tw.SetNoHeaders(f.options.NoHeaders)

	// Display basic workflow information
	basicFields := []string{"Name", "name", "Description", "description", "Version", "version"}
	for _, field := range basicFields {
		if value, exists := workflowData[field]; exists && value != nil {
			tw.AppendRow([]string{strings.ToLower(field), fmt.Sprintf("%v", value)})
		}
	}

	tw.Render()

	// Display Input Args if they exist
	f.displayWorkflowInputs(workflowData)

	// Display Steps if they exist
	f.displayWorkflowSteps(workflowData)

	return nil
}

// formatWorkflowExecutionResult formats workflow execution results in a user-friendly way
func (f *TableFormatter) formatWorkflowExecutionResult(data map[string]interface{}) error {
	// Main execution info
	fmt.Println("Workflow Execution Completed")
	fmt.Println()

	// Create kubectl-style plain table for execution summary
	tw := NewPlainTableWriter(os.Stdout)
	tw.SetHeaders([]string{"PROPERTY", "VALUE"})
	tw.SetNoHeaders(f.options.NoHeaders)

	// Add execution details
	if executionID, exists := data["execution_id"]; exists {
		tw.AppendRow([]string{"execution_id", fmt.Sprintf("%v", executionID)})
	}

	if workflow, exists := data["workflow"]; exists {
		tw.AppendRow([]string{"workflow", fmt.Sprintf("%v", workflow)})
	}

	if status, exists := data["status"]; exists {
		tw.AppendRow([]string{"status", fmt.Sprintf("%v", status)})
	}

	tw.Render()

	// Show input parameters if they exist
	if input, exists := data["input"]; exists {
		if inputMap, ok := input.(map[string]interface{}); ok && len(inputMap) > 0 {
			fmt.Println("\nInput Parameters:")

			inputTw := NewPlainTableWriter(os.Stdout)
			inputTw.SetHeaders([]string{"PARAMETER", "VALUE"})
			inputTw.SetNoHeaders(f.options.NoHeaders)

			// Sort parameters for consistent display
			var paramNames []string
			for paramName := range inputMap {
				paramNames = append(paramNames, paramName)
			}
			sort.Strings(paramNames)

			for _, paramName := range paramNames {
				value := inputMap[paramName]
				inputTw.AppendRow([]string{paramName, fmt.Sprintf("%v", value)})
			}

			inputTw.Render()
		}
	}

	// Show step results if they exist
	if results, exists := data["results"]; exists {
		if resultsMap, ok := results.(map[string]interface{}); ok && len(resultsMap) > 0 {
			fmt.Println("\nStep Results:")

			stepTw := NewPlainTableWriter(os.Stdout)
			stepTw.SetHeaders([]string{"STEP", "STATUS", "DETAILS"})
			stepTw.SetNoHeaders(f.options.NoHeaders)

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

					stepTw.AppendRow([]string{stepName, status, details})
				}
			}

			stepTw.Render()
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

	if len(argsData) == 0 {
		return
	}

	fmt.Println("\nInput Args:")

	tw := NewPlainTableWriter(os.Stdout)
	tw.SetHeaders([]string{"ARGUMENT", "TYPE", "DESCRIPTION", "REQUIRED"})
	tw.SetNoHeaders(f.options.NoHeaders)

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
				isRequired = "Yes"
			}
		}

		// Show default value if available
		if def, exists := paramDef["default"]; exists && def != nil {
			description = description + fmt.Sprintf(" (default: %v)", def)
		}

		tw.AppendRow([]string{paramName, paramType, description, isRequired})
	}

	tw.Render()
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

	fmt.Println("\nWorkflow Steps:")

	tw := NewPlainTableWriter(os.Stdout)
	tw.SetHeaders([]string{"STEP", "TOOL", "DESCRIPTION"})
	tw.SetNoHeaders(f.options.NoHeaders)

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

			tw.AppendRow([]string{stepNum, tool, description})
		}
	}

	tw.Render()
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

// formatEmptyList displays a message for empty resource lists.
// This provides a kubectl-style "No X found" message instead of
// showing a property/value table with metadata.
//
// Args:
//   - resourceKey: The array key name (e.g., "workflows", "services")
//
// Returns:
//   - error: Always nil, just prints the message
func (f *TableFormatter) formatEmptyList(resourceKey string) error {
	// Convert key to readable name (e.g., "serviceClasses" -> "service classes")
	readable := f.keyToReadableName(resourceKey)
	fmt.Printf("No %s found\n", readable)
	return nil
}

// keyToReadableName converts a camelCase or singular key to a readable plural name.
func (f *TableFormatter) keyToReadableName(key string) string {
	// Common mappings for resource keys
	mappings := map[string]string{
		"services":       "services",
		"serviceClasses": "service classes",
		"mcpServers":     "MCP servers",
		"workflows":      "workflows",
		"executions":     "executions",
		"capabilities":   "capabilities",
		"items":          "items",
		"results":        "results",
		"tools":          "tools",
		"resources":      "resources",
		"prompts":        "prompts",
	}

	if readable, exists := mappings[key]; exists {
		return readable
	}
	return key
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
