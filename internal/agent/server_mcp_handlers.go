package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleListTools handles the list_tools MCP tool for AI assistants.
// This handler provides access to the complete list of available tools
// from the connected MCP servers, formatted as JSON for programmatic consumption.
//
// The handler:
//   - Retrieves tools from the client cache (populated by aggregator)
//   - Formats the tool list as structured JSON
//   - Returns tool names and descriptions for AI assistant discovery
//
// Returns:
//   - JSON array of tool objects with name and description fields
//   - Error message if formatting fails
func (m *MCPServer) handleListTools(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	m.client.mu.RLock()
	tools := m.client.toolCache
	m.client.mu.RUnlock()

	jsonData, err := m.client.formatters.FormatToolsListJSON(tools)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format tools: %v", err)), nil
	}

	return mcp.NewToolResultText(jsonData), nil
}

// handleListResources handles the list_resources MCP tool for AI assistants.
// This handler provides access to the complete list of available resources
// from the connected MCP servers, formatted as JSON for programmatic consumption.
//
// The handler:
//   - Retrieves resources from the client cache (populated by aggregator)
//   - Formats the resource list as structured JSON
//   - Returns resource URIs, names, descriptions, and MIME types
//
// Returns:
//   - JSON array of resource objects with complete metadata
//   - Error message if formatting fails
func (m *MCPServer) handleListResources(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	m.client.mu.RLock()
	resources := m.client.resourceCache
	m.client.mu.RUnlock()

	jsonData, err := m.client.formatters.FormatResourcesListJSON(resources)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format resources: %v", err)), nil
	}

	return mcp.NewToolResultText(jsonData), nil
}

// handleListPrompts handles the list_prompts MCP tool for AI assistants.
// This handler provides access to the complete list of available prompts
// from the connected MCP servers, formatted as JSON for programmatic consumption.
//
// The handler:
//   - Retrieves prompts from the client cache (populated by aggregator)
//   - Formats the prompt list as structured JSON
//   - Returns prompt names and descriptions for AI assistant discovery
//
// Returns:
//   - JSON array of prompt objects with name and description fields
//   - Error message if formatting fails
func (m *MCPServer) handleListPrompts(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	m.client.mu.RLock()
	prompts := m.client.promptCache
	m.client.mu.RUnlock()

	jsonData, err := m.client.formatters.FormatPromptsListJSON(prompts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format prompts: %v", err)), nil
	}

	return mcp.NewToolResultText(jsonData), nil
}

// handleDescribeTool handles the describe_tool MCP tool for AI assistants.
// This handler provides detailed information about a specific tool, including
// its complete input schema for arg validation and documentation.
//
// Args:
//   - name (required): The exact name of the tool to describe
//
// The handler:
//   - Validates the tool name arg
//   - Searches the cached tool list for the specified tool
//   - Returns detailed tool information including input schema
//
// Returns:
//   - JSON object with tool name, description, and complete input schema
//   - Error message if tool not found or formatting fails
func (m *MCPServer) handleDescribeTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name argument is required"), nil
	}

	m.client.mu.RLock()
	tools := m.client.toolCache
	m.client.mu.RUnlock()

	tool := m.client.formatters.FindTool(tools, name)
	if tool == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Tool not found: %s", name)), nil
	}

	jsonData, err := m.client.formatters.FormatToolDetailJSON(*tool)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format tool info: %v", err)), nil
	}

	return mcp.NewToolResultText(jsonData), nil
}

// handleDescribeResource handles the describe_resource MCP tool for AI assistants.
// This handler provides detailed metadata about a specific resource, including
// URI, name, description, and MIME type information.
//
// Args:
//   - uri (required): The exact URI of the resource to describe
//
// The handler:
//   - Validates the resource URI arg
//   - Searches the cached resource list for the specified resource
//   - Returns comprehensive resource metadata
//
// Returns:
//   - JSON object with resource URI, name, description, and MIME type
//   - Error message if resource not found or formatting fails
func (m *MCPServer) handleDescribeResource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uri, err := request.RequireString("uri")
	if err != nil {
		return mcp.NewToolResultError("uri argument is required"), nil
	}

	m.client.mu.RLock()
	resources := m.client.resourceCache
	m.client.mu.RUnlock()

	resource := m.client.formatters.FindResource(resources, uri)
	if resource == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Resource not found: %s", uri)), nil
	}

	jsonData, err := m.client.formatters.FormatResourceDetailJSON(*resource)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format resource info: %v", err)), nil
	}

	return mcp.NewToolResultText(jsonData), nil
}

// handleDescribePrompt handles the describe_prompt MCP tool for AI assistants.
// This handler provides detailed information about a specific prompt, including
// its argument specifications and requirements for proper usage.
//
// Args:
//   - name (required): The exact name of the prompt to describe
//
// The handler:
//   - Validates the prompt name arg
//   - Searches the cached prompt list for the specified prompt
//   - Returns detailed prompt information including argument specifications
//
// Returns:
//   - JSON object with prompt name, description, and argument details
//   - Error message if prompt not found or formatting fails
func (m *MCPServer) handleDescribePrompt(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name argument is required"), nil
	}

	m.client.mu.RLock()
	prompts := m.client.promptCache
	m.client.mu.RUnlock()

	prompt := m.client.formatters.FindPrompt(prompts, name)
	if prompt == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Prompt not found: %s", name)), nil
	}

	jsonData, err := m.client.formatters.FormatPromptDetailJSON(*prompt)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format prompt info: %v", err)), nil
	}

	return mcp.NewToolResultText(jsonData), nil
}

// handleCallTool handles the call_tool MCP tool for AI assistants.
// This is the primary tool execution handler that allows AI assistants to
// invoke any tool available through the muster aggregator with proper
// argument validation and error handling.
//
// Args:
//   - name (required): The exact name of the tool to execute
//   - arguments (optional): JSON object with tool-specific args
//
// The handler:
//   - Validates tool name and argument args
//   - Forwards the tool call to the aggregator via the client
//   - Handles both successful results and tool-reported errors
//   - Formats content for different media types (text, images, audio)
//
// Returns:
//   - Tool execution results formatted as text
//   - Error message if tool execution fails or args are invalid
func (m *MCPServer) handleCallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name argument is required"), nil
	}

	// Get arguments if provided and validate they're a JSON object
	var args map[string]interface{}
	if argsRaw := request.GetArguments()["arguments"]; argsRaw != nil {
		var ok bool
		args, ok = argsRaw.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("arguments must be a JSON object"), nil
		}
	}

	// Execute the tool via the client
	result, err := m.client.CallTool(ctx, name, args)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Tool execution failed: %v", err)), nil
	}

	// Handle tool-reported errors
	if result.IsError {
		var errorMessages []string
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				errorMessages = append(errorMessages, textContent.Text)
			}
		}
		return mcp.NewToolResultError(strings.Join(errorMessages, "\n")), nil
	}

	// Format successful result content for different media types
	var resultTexts []string
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			resultTexts = append(resultTexts, textContent.Text)
		} else if imageContent, ok := mcp.AsImageContent(content); ok {
			resultTexts = append(resultTexts, fmt.Sprintf("[Image: MIME type %s, %d bytes]", imageContent.MIMEType, len(imageContent.Data)))
		} else if audioContent, ok := mcp.AsAudioContent(content); ok {
			resultTexts = append(resultTexts, fmt.Sprintf("[Audio: MIME type %s, %d bytes]", audioContent.MIMEType, len(audioContent.Data)))
		}
	}

	return mcp.NewToolResultText(strings.Join(resultTexts, "\n")), nil
}

// handleGetResource handles the get_resource MCP tool for AI assistants.
// This handler allows AI assistants to retrieve resource content from
// any resource available through the muster aggregator.
//
// Args:
//   - uri (required): The URI of the resource to retrieve
//
// The handler:
//   - Validates the resource URI arg
//   - Retrieves resource content via the client
//   - Handles different content types (text and binary)
//   - Formats content appropriately for AI assistant consumption
//
// Returns:
//   - Resource content formatted as text
//   - Binary data description for non-text content
//   - Error message if resource retrieval fails
func (m *MCPServer) handleGetResource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uri, err := request.RequireString("uri")
	if err != nil {
		return mcp.NewToolResultError("uri argument is required"), nil
	}

	// Retrieve the resource via the client
	result, err := m.client.GetResource(ctx, uri)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Resource retrieval failed: %v", err)), nil
	}

	// Format contents for different content types
	var contentTexts []string
	for _, content := range result.Contents {
		if textContent, ok := mcp.AsTextResourceContents(content); ok {
			contentTexts = append(contentTexts, textContent.Text)
		} else if blobContent, ok := mcp.AsBlobResourceContents(content); ok {
			contentTexts = append(contentTexts, fmt.Sprintf("[Binary data: %d bytes]", len(blobContent.Blob)))
		}
	}

	return mcp.NewToolResultText(strings.Join(contentTexts, "\n")), nil
}

// handleGetPrompt handles the get_prompt MCP tool for AI assistants.
// This handler allows AI assistants to execute prompt templates with
// specific arguments and retrieve the generated content.
//
// Args:
//   - name (required): The exact name of the prompt to execute
//   - arguments (optional): JSON object with prompt-specific args
//
// The handler:
//   - Validates prompt name and converts arguments to string map
//   - Executes the prompt template via the client
//   - Formats the resulting messages as structured JSON
//   - Handles different message content types (text, images, audio, embedded resources)
//
// Returns:
//   - JSON array of message objects with role and content information
//   - Error message if prompt execution fails or args are invalid
func (m *MCPServer) handleGetPrompt(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name argument is required"), nil
	}

	// Get arguments if provided and convert to string map
	args := make(map[string]string)
	if argsRaw := request.GetArguments()["arguments"]; argsRaw != nil {
		argsMap, ok := argsRaw.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("arguments must be a JSON object"), nil
		}

		// Convert all values to strings as required by prompt interface
		for k, v := range argsMap {
			args[k] = fmt.Sprintf("%v", v)
		}
	}

	// Get the prompt via the client
	result, err := m.client.GetPrompt(ctx, name, args)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Prompt retrieval failed: %v", err)), nil
	}

	// Format messages as structured JSON with proper content handling
	type Message struct {
		Role    mcp.Role        `json:"role"`
		Content json.RawMessage `json:"content"`
	}

	messages := make([]Message, len(result.Messages))
	for i, msg := range result.Messages {
		var content json.RawMessage

		// Handle different content types with appropriate formatting
		if textContent, ok := mcp.AsTextContent(msg.Content); ok {
			contentMap := map[string]interface{}{
				"type": "text",
				"text": textContent.Text,
			}
			content, _ = json.Marshal(contentMap)
		} else if imageContent, ok := mcp.AsImageContent(msg.Content); ok {
			contentMap := map[string]interface{}{
				"type":     "image",
				"mimeType": imageContent.MIMEType,
				"dataSize": len(imageContent.Data),
			}
			content, _ = json.Marshal(contentMap)
		} else if audioContent, ok := mcp.AsAudioContent(msg.Content); ok {
			contentMap := map[string]interface{}{
				"type":     "audio",
				"mimeType": audioContent.MIMEType,
				"dataSize": len(audioContent.Data),
			}
			content, _ = json.Marshal(contentMap)
		} else if resource, ok := mcp.AsEmbeddedResource(msg.Content); ok {
			contentMap := map[string]interface{}{
				"type":     "embeddedResource",
				"resource": resource.Resource,
			}
			content, _ = json.Marshal(contentMap)
		} else {
			// Fallback for unknown content types
			content, _ = json.Marshal(msg.Content)
		}

		messages[i] = Message{
			Role:    msg.Role,
			Content: content,
		}
	}

	jsonData, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format messages: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}

// handleListCoreTools handles the list_core_tools MCP tool for AI assistants.
// This handler provides a filtered view of tools that are considered "core"
// muster functionality, helping AI assistants distinguish between built-in
// capabilities and external MCP server tools.
//
// Args:
//   - include_schema (optional): Whether to include full tool specifications (default: true)
//
// The handler:
//   - Filters tools that start with "core" prefix (case-insensitive)
//   - Provides summary statistics about filtering results
//   - Returns structured data compatible with filter_tools format
//   - Includes full tool specifications with input schemas by default
//
// Returns:
//   - JSON object with filter criteria, counts, and filtered tool list with full specifications
//   - Error message if formatting fails
func (m *MCPServer) handleListCoreTools(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Get include_schema arg (defaults to true for full specifications)
	args := request.GetArguments()
	includeSchema := true

	if schemaVal, ok := args["include_schema"]; ok {
		if b, ok := schemaVal.(bool); ok {
			includeSchema = b
		}
	}

	// Get all tools from cache
	m.client.mu.RLock()
	tools := m.client.toolCache
	m.client.mu.RUnlock()

	if len(tools) == 0 {
		return mcp.NewToolResultText("No tools available"), nil
	}

	// Filter tools that start with "core" (case-insensitive)
	var coreTools []map[string]interface{}
	pattern := "core"

	for _, tool := range tools {
		toolName := strings.ToLower(tool.Name)
		if strings.HasPrefix(toolName, pattern) {
			toolInfo := map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
			}

			// Include full input schema if requested (default behavior)
			if includeSchema {
				toolInfo["inputSchema"] = tool.InputSchema
			}

			coreTools = append(coreTools, toolInfo)
		}
	}

	// Prepare result in the same format as filter_tools for consistency
	result := map[string]interface{}{
		"filters": map[string]interface{}{
			"pattern":            "core*",
			"description_filter": "",
			"case_sensitive":     false,
			"include_schema":     includeSchema,
		},
		"total_tools":    len(tools),
		"filtered_count": len(coreTools),
		"tools":          coreTools,
	}

	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format core tools: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}

// handleFilterTools handles the filter_tools MCP tool for AI assistants.
// This handler provides advanced filtering capabilities for discovering
// tools based on name patterns and description content, enabling AI
// assistants to find relevant tools more efficiently.
//
// Args:
//   - pattern (optional): Wildcard pattern for tool name matching (* supported)
//   - description_filter (optional): Substring to match in descriptions
//   - case_sensitive (optional): Whether to use case-sensitive matching
//   - include_schema (optional): Whether to include full tool specifications (default: true)
//
// The handler:
//   - Supports wildcard pattern matching with * for flexible name searches
//   - Provides case-sensitive and case-insensitive matching options
//   - Combines pattern and description filters with AND logic
//   - Returns comprehensive filtering statistics and results
//   - Includes full tool specifications with input schemas by default
//
// Wildcard patterns supported:
//   - prefix* (starts with prefix)
//   - *suffix (ends with suffix)
//   - *substring* (contains substring)
//   - Complex patterns with multiple * wildcards
//
// Returns:
//   - JSON object with filter criteria, statistics, and matching tools with full specifications
//   - Error message if formatting fails
func (m *MCPServer) handleFilterTools(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Get filter args from arguments with defaults
	args := request.GetArguments()

	var pattern, descriptionFilter string
	var caseSensitive bool
	includeSchema := true // Default to true for full specifications

	if patternVal, ok := args["pattern"]; ok {
		if str, ok := patternVal.(string); ok {
			pattern = str
		}
	}

	if descFilterVal, ok := args["description_filter"]; ok {
		if str, ok := descFilterVal.(string); ok {
			descriptionFilter = str
		}
	}

	if caseVal, ok := args["case_sensitive"]; ok {
		if b, ok := caseVal.(bool); ok {
			caseSensitive = b
		}
	}

	if schemaVal, ok := args["include_schema"]; ok {
		if b, ok := schemaVal.(bool); ok {
			includeSchema = b
		}
	}

	// Get tools from cache
	m.client.mu.RLock()
	tools := m.client.toolCache
	m.client.mu.RUnlock()

	if len(tools) == 0 {
		return mcp.NewToolResultText("No tools available to filter"), nil
	}

	// Filter tools based on criteria with comprehensive pattern matching
	var filteredTools []map[string]interface{}

	for _, tool := range tools {
		// Check if tool matches all specified filters
		matches := true

		// Check pattern filter with wildcard support
		if pattern != "" {
			toolName := tool.Name
			searchPattern := pattern

			if !caseSensitive {
				toolName = strings.ToLower(toolName)
				searchPattern = strings.ToLower(searchPattern)
			}

			// Use proper wildcard pattern matching
			matches = matches && matchWildcard(toolName, searchPattern)
		}

		// Check description filter with case sensitivity option
		if descriptionFilter != "" && matches {
			toolDesc := tool.Description
			searchDesc := descriptionFilter

			if !caseSensitive {
				toolDesc = strings.ToLower(toolDesc)
				searchDesc = strings.ToLower(searchDesc)
			}

			matches = matches && strings.Contains(toolDesc, searchDesc)
		}

		// Add to filtered results if all criteria match
		if matches {
			toolInfo := map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
			}

			// Include full input schema if requested (default behavior)
			if includeSchema {
				toolInfo["inputSchema"] = tool.InputSchema
			}

			filteredTools = append(filteredTools, toolInfo)
		}
	}

	// Prepare comprehensive result with filtering metadata
	result := map[string]interface{}{
		"filters": map[string]interface{}{
			"pattern":            pattern,
			"description_filter": descriptionFilter,
			"case_sensitive":     caseSensitive,
			"include_schema":     includeSchema,
		},
		"total_tools":    len(tools),
		"filtered_count": len(filteredTools),
		"tools":          filteredTools,
	}

	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format filtered tools: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}

// matchWildcard implements proper sequential wildcard pattern matching
func matchWildcard(text, pattern string) bool {
	// Handle edge cases
	if pattern == "*" {
		return true
	}
	if pattern == "" {
		return text == ""
	}
	if text == "" {
		return pattern == ""
	}

	// If no wildcards, do substring matching (like the original behavior)
	if !strings.Contains(pattern, "*") {
		return strings.Contains(text, pattern)
	}

	// Split pattern by wildcards
	parts := strings.Split(pattern, "*")
	textPos := 0

	for i, part := range parts {
		// Skip empty parts between consecutive wildcards
		if part == "" {
			continue
		}

		if i == 0 && !strings.HasPrefix(pattern, "*") {
			// First part must match from the beginning (no leading wildcard)
			if !strings.HasPrefix(text[textPos:], part) {
				return false
			}
			textPos += len(part)
		} else {
			// All other parts must exist in sequence
			// For patterns with wildcards, all parts after the first are treated as "find anywhere"
			idx := strings.Index(text[textPos:], part)
			if idx == -1 {
				return false
			}
			textPos += idx + len(part)
		}
	}

	return true
}
