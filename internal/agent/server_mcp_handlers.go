package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleListTools handles the list_tools MCP tool
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

// handleListResources handles the list_resources MCP tool
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

// handleListPrompts handles the list_prompts MCP tool
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

// handleDescribeTool handles the describe_tool MCP tool
func (m *MCPServer) handleDescribeTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name parameter is required"), nil
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

// handleDescribeResource handles the describe_resource MCP tool
func (m *MCPServer) handleDescribeResource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uri, err := request.RequireString("uri")
	if err != nil {
		return mcp.NewToolResultError("uri parameter is required"), nil
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

// handleDescribePrompt handles the describe_prompt MCP tool
func (m *MCPServer) handleDescribePrompt(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name parameter is required"), nil
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

// handleCallTool handles the call_tool MCP tool
func (m *MCPServer) handleCallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name parameter is required"), nil
	}

	// Get arguments if provided
	var args map[string]interface{}
	if argsRaw := request.GetArguments()["arguments"]; argsRaw != nil {
		var ok bool
		args, ok = argsRaw.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("arguments must be a JSON object"), nil
		}
	}

	// Execute the tool
	result, err := m.client.CallTool(ctx, name, args)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Tool execution failed: %v", err)), nil
	}

	// Format result
	if result.IsError {
		var errorMessages []string
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				errorMessages = append(errorMessages, textContent.Text)
			}
		}
		return mcp.NewToolResultError(strings.Join(errorMessages, "\n")), nil
	}

	// Format successful result
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

// handleGetResource handles the get_resource MCP tool
func (m *MCPServer) handleGetResource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uri, err := request.RequireString("uri")
	if err != nil {
		return mcp.NewToolResultError("uri parameter is required"), nil
	}

	// Retrieve the resource
	result, err := m.client.GetResource(ctx, uri)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Resource retrieval failed: %v", err)), nil
	}

	// Format contents
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

// handleGetPrompt handles the get_prompt MCP tool
func (m *MCPServer) handleGetPrompt(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name parameter is required"), nil
	}

	// Get arguments if provided
	args := make(map[string]string)
	if argsRaw := request.GetArguments()["arguments"]; argsRaw != nil {
		argsMap, ok := argsRaw.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("arguments must be a JSON object"), nil
		}

		// Convert to string map
		for k, v := range argsMap {
			args[k] = fmt.Sprintf("%v", v)
		}
	}

	// Get the prompt
	result, err := m.client.GetPrompt(ctx, name, args)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Prompt retrieval failed: %v", err)), nil
	}

	// Format messages as JSON
	type Message struct {
		Role    mcp.Role        `json:"role"`
		Content json.RawMessage `json:"content"`
	}

	messages := make([]Message, len(result.Messages))
	for i, msg := range result.Messages {
		var content json.RawMessage

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
			// Fallback
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

// handleListCoreTools handles the list_core_tools MCP tool by filtering tools that start with "core_"
func (m *MCPServer) handleListCoreTools(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
			coreTools = append(coreTools, map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
			})
		}
	}

	// Prepare result in the same format as filter_tools
	result := map[string]interface{}{
		"filters": map[string]interface{}{
			"pattern":            "core*",
			"description_filter": "",
			"case_sensitive":     false,
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

// handleFilterTools handles the filter_tools MCP tool
func (m *MCPServer) handleFilterTools(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Get filter parameters from arguments
	args := request.GetArguments()

	var pattern, descriptionFilter string
	var caseSensitive bool

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

	// Get tools from cache
	m.client.mu.RLock()
	tools := m.client.toolCache
	m.client.mu.RUnlock()

	if len(tools) == 0 {
		return mcp.NewToolResultText("No tools available to filter"), nil
	}

	// Filter tools based on criteria
	var filteredTools []map[string]interface{}

	for _, tool := range tools {
		// Check if tool matches the filters
		matches := true

		// Check pattern filter (supports basic wildcard matching)
		if pattern != "" {
			toolName := tool.Name
			searchPattern := pattern

			if !caseSensitive {
				toolName = strings.ToLower(toolName)
				searchPattern = strings.ToLower(searchPattern)
			}

			// Simple wildcard matching
			if strings.Contains(searchPattern, "*") {
				// Convert wildcard pattern to prefix/suffix matching
				if strings.HasPrefix(searchPattern, "*") && strings.HasSuffix(searchPattern, "*") {
					// *pattern* - contains
					middle := strings.Trim(searchPattern, "*")
					matches = matches && strings.Contains(toolName, middle)
				} else if strings.HasPrefix(searchPattern, "*") {
					// *pattern - ends with
					suffix := strings.TrimPrefix(searchPattern, "*")
					matches = matches && strings.HasSuffix(toolName, suffix)
				} else if strings.HasSuffix(searchPattern, "*") {
					// pattern* - starts with
					prefix := strings.TrimSuffix(searchPattern, "*")
					matches = matches && strings.HasPrefix(toolName, prefix)
				} else {
					// pattern*pattern - more complex, use simple contains for each part
					parts := strings.Split(searchPattern, "*")
					for _, part := range parts {
						if part != "" && !strings.Contains(toolName, part) {
							matches = false
							break
						}
					}
				}
			} else {
				// Exact match or contains
				matches = matches && strings.Contains(toolName, searchPattern)
			}
		}

		// Check description filter
		if descriptionFilter != "" && matches {
			toolDesc := tool.Description
			searchDesc := descriptionFilter

			if !caseSensitive {
				toolDesc = strings.ToLower(toolDesc)
				searchDesc = strings.ToLower(searchDesc)
			}

			matches = matches && strings.Contains(toolDesc, searchDesc)
		}

		// Add to filtered results if it matches
		if matches {
			filteredTools = append(filteredTools, map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
			})
		}
	}

	// Prepare result
	result := map[string]interface{}{
		"filters": map[string]interface{}{
			"pattern":            pattern,
			"description_filter": descriptionFilter,
			"case_sensitive":     caseSensitive,
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
