package metatools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"muster/internal/api"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// promptMessageResponse represents a serialized prompt message for JSON output.
// This structure preserves message role and content for proper serialization.
type promptMessageResponse struct {
	Role    mcp.Role        `json:"role"`
	Content json.RawMessage `json:"content"`
}

// getHandler retrieves the MetaToolsHandler from the API layer.
// This helper eliminates repetitive handler retrieval across all handler methods.
//
// Returns:
//   - api.MetaToolsHandler: The handler if available
//   - *api.CallToolResult: Error result if handler is not available, nil otherwise
func (p *Provider) getHandler() (api.MetaToolsHandler, *api.CallToolResult) {
	handler := api.GetMetaTools()
	if handler == nil {
		return nil, errorResult("Meta-tools handler not available")
	}
	return handler, nil
}

// ExecuteTool executes a specific meta-tool by name with the provided arguments.
// This implements the api.ToolProvider interface for tool execution.
//
// Args:
//   - ctx: Context for the operation, including session ID for visibility
//   - toolName: The name of the meta-tool to execute
//   - args: Arguments for the tool execution
//
// Returns:
//   - *api.CallToolResult: The result of the tool execution
//   - error: Error if the tool doesn't exist or execution fails
func (p *Provider) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	logging.Debug("metatools", "Executing tool %s with args: %v", toolName, args)

	// Dispatch to the appropriate handler
	switch toolName {
	case "list_tools":
		return p.handleListTools(ctx, args)
	case "describe_tool":
		return p.handleDescribeTool(ctx, args)
	case "list_core_tools":
		return p.handleListCoreTools(ctx, args)
	case "filter_tools":
		return p.handleFilterTools(ctx, args)
	case "call_tool":
		return p.handleCallTool(ctx, args)
	case "list_resources":
		return p.handleListResources(ctx, args)
	case "describe_resource":
		return p.handleDescribeResource(ctx, args)
	case "get_resource":
		return p.handleGetResource(ctx, args)
	case "list_prompts":
		return p.handleListPrompts(ctx, args)
	case "describe_prompt":
		return p.handleDescribePrompt(ctx, args)
	case "get_prompt":
		return p.handleGetPrompt(ctx, args)
	default:
		return nil, fmt.Errorf("unknown meta-tool: %s", toolName)
	}
}

// handleListTools handles the list_tools meta-tool.
// This handler returns a list of all available tools from the aggregator,
// along with information about servers that require authentication.
func (p *Provider) handleListTools(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	tools, err := handler.ListTools(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to list tools: %v", err)), nil
	}

	// Get servers requiring authentication for the current session
	serversRequiringAuth := handler.ListServersRequiringAuth(ctx)

	jsonData, err := p.formatters.FormatToolsListWithAuthJSON(tools, serversRequiringAuth)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format tools: %v", err)), nil
	}

	return textResult(jsonData), nil
}

// handleDescribeTool handles the describe_tool meta-tool.
// This handler returns detailed information about a specific tool.
func (p *Provider) handleDescribeTool(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return errorResult("name argument is required"), nil
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	tools, err := handler.ListTools(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to list tools: %v", err)), nil
	}

	tool := p.formatters.FindTool(tools, name)
	if tool == nil {
		return errorResult(fmt.Sprintf("Tool not found: %s", name)), nil
	}

	jsonData, err := p.formatters.FormatToolDetailJSON(*tool)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format tool info: %v", err)), nil
	}

	return textResult(jsonData), nil
}

// handleListCoreTools handles the list_core_tools meta-tool.
// This handler returns a filtered list of core muster tools (prefixed with "core").
// It delegates to handleFilterTools with a pre-configured "core*" pattern.
func (p *Provider) handleListCoreTools(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	// Build args for filter_tools with core* pattern
	filterArgs := map[string]interface{}{
		"pattern":        "core*",
		"case_sensitive": false,
	}

	// Pass through include_schema if provided
	if schemaVal, ok := args["include_schema"].(bool); ok {
		filterArgs["include_schema"] = schemaVal
	}

	return p.handleFilterTools(ctx, filterArgs)
}

// handleFilterTools handles the filter_tools meta-tool.
// This handler filters tools based on name patterns and description content.
func (p *Provider) handleFilterTools(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	// Get filter args with defaults
	var pattern, descriptionFilter string
	var caseSensitive bool
	includeSchema := true

	if patternVal, ok := args["pattern"].(string); ok {
		pattern = patternVal
	}
	if descFilterVal, ok := args["description_filter"].(string); ok {
		descriptionFilter = descFilterVal
	}
	if caseVal, ok := args["case_sensitive"].(bool); ok {
		caseSensitive = caseVal
	}
	if schemaVal, ok := args["include_schema"].(bool); ok {
		includeSchema = schemaVal
	}

	tools, err := handler.ListTools(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to list tools: %v", err)), nil
	}

	if len(tools) == 0 {
		return textResult("No tools available to filter"), nil
	}

	// Validate pattern syntax before filtering
	if pattern != "" {
		if _, err := filepath.Match(pattern, ""); err != nil {
			return errorResult(fmt.Sprintf("Invalid pattern %q: %v", pattern, err)), nil
		}
	}

	// Filter tools based on criteria
	filteredTools := make([]map[string]interface{}, 0, len(tools))

	for _, tool := range tools {
		matches := true

		// Check pattern filter with wildcard support
		if pattern != "" {
			toolName := tool.Name
			searchPattern := pattern

			if !caseSensitive {
				toolName = strings.ToLower(toolName)
				searchPattern = strings.ToLower(searchPattern)
			}

			// Pattern already validated above, error won't occur
			matches, _ = filepath.Match(searchPattern, toolName)
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

		if matches {
			toolInfo := map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
			}

			if includeSchema {
				toolInfo["inputSchema"] = tool.InputSchema
			}

			filteredTools = append(filteredTools, toolInfo)
		}
	}

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
		return errorResult(fmt.Sprintf("Failed to format filtered tools: %v", err)), nil
	}

	return textResult(string(jsonData)), nil
}

// handleCallTool handles the call_tool meta-tool.
// This handler executes any tool by name with the provided arguments.
// It preserves the full CallToolResult structure for proper unwrapping.
func (p *Provider) handleCallTool(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return errorResult("name argument is required"), nil
	}

	// Get arguments if provided
	var toolArgs map[string]interface{}
	if argsRaw := args["arguments"]; argsRaw != nil {
		var ok bool
		toolArgs, ok = argsRaw.(map[string]interface{})
		if !ok {
			return errorResult("arguments must be a JSON object"), nil
		}
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	// Execute the tool via the handler
	result, err := handler.CallTool(ctx, name, toolArgs)
	if err != nil {
		return errorResult(fmt.Sprintf("Tool execution failed: %v", err)), nil
	}

	// CRITICAL: Return result as structured JSON to preserve CallToolResult structure.
	// This enables proper unwrapping by clients and maintains BDD test validation fidelity.
	resultJSON, err := json.Marshal(struct {
		IsError bool          `json:"isError"`
		Content []interface{} `json:"content"`
	}{
		IsError: result.IsError,
		Content: SerializeContent(result.Content),
	})
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to serialize result: %v", err)), nil
	}

	return textResult(string(resultJSON)), nil
}

// handleListResources handles the list_resources meta-tool.
// This handler returns a list of all available resources.
func (p *Provider) handleListResources(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	resources, err := handler.ListResources(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to list resources: %v", err)), nil
	}

	jsonData, err := p.formatters.FormatResourcesListJSON(resources)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format resources: %v", err)), nil
	}

	return textResult(jsonData), nil
}

// handleDescribeResource handles the describe_resource meta-tool.
// This handler returns detailed information about a specific resource.
func (p *Provider) handleDescribeResource(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	uri, ok := args["uri"].(string)
	if !ok || uri == "" {
		return errorResult("uri argument is required"), nil
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	resources, err := handler.ListResources(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to list resources: %v", err)), nil
	}

	resource := p.formatters.FindResource(resources, uri)
	if resource == nil {
		return errorResult(fmt.Sprintf("Resource not found: %s", uri)), nil
	}

	jsonData, err := p.formatters.FormatResourceDetailJSON(*resource)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format resource info: %v", err)), nil
	}

	return textResult(jsonData), nil
}

// handleGetResource handles the get_resource meta-tool.
// This handler retrieves the contents of a resource.
func (p *Provider) handleGetResource(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	uri, ok := args["uri"].(string)
	if !ok || uri == "" {
		return errorResult("uri argument is required"), nil
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	result, err := handler.GetResource(ctx, uri)
	if err != nil {
		return errorResult(fmt.Sprintf("Resource retrieval failed: %v", err)), nil
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

	return textResult(strings.Join(contentTexts, "\n")), nil
}

// handleListPrompts handles the list_prompts meta-tool.
// This handler returns a list of all available prompts.
func (p *Provider) handleListPrompts(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	prompts, err := handler.ListPrompts(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to list prompts: %v", err)), nil
	}

	jsonData, err := p.formatters.FormatPromptsListJSON(prompts)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format prompts: %v", err)), nil
	}

	return textResult(jsonData), nil
}

// handleDescribePrompt handles the describe_prompt meta-tool.
// This handler returns detailed information about a specific prompt.
func (p *Provider) handleDescribePrompt(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return errorResult("name argument is required"), nil
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	prompts, err := handler.ListPrompts(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to list prompts: %v", err)), nil
	}

	prompt := p.formatters.FindPrompt(prompts, name)
	if prompt == nil {
		return errorResult(fmt.Sprintf("Prompt not found: %s", name)), nil
	}

	jsonData, err := p.formatters.FormatPromptDetailJSON(*prompt)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format prompt info: %v", err)), nil
	}

	return textResult(jsonData), nil
}

// handleGetPrompt handles the get_prompt meta-tool.
// This handler executes a prompt with the provided arguments.
func (p *Provider) handleGetPrompt(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return errorResult("name argument is required"), nil
	}

	// Get arguments if provided and convert to string map
	promptArgs := make(map[string]string)
	if argsRaw := args["arguments"]; argsRaw != nil {
		argsMap, ok := argsRaw.(map[string]interface{})
		if !ok {
			return errorResult("arguments must be a JSON object"), nil
		}

		for k, v := range argsMap {
			promptArgs[k] = fmt.Sprintf("%v", v)
		}
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	result, err := handler.GetPrompt(ctx, name, promptArgs)
	if err != nil {
		return errorResult(fmt.Sprintf("Prompt retrieval failed: %v", err)), nil
	}

	// Format messages as structured JSON using the package-level type
	messages := make([]promptMessageResponse, len(result.Messages))
	for i, msg := range result.Messages {
		content, marshalErr := serializePromptContent(msg.Content)
		if marshalErr != nil {
			logging.Warn("metatools", "Failed to serialize prompt content: %v", marshalErr)
			content = []byte(`{"error": "failed to serialize content"}`)
		}

		messages[i] = promptMessageResponse{
			Role:    msg.Role,
			Content: content,
		}
	}

	jsonData, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format messages: %v", err)), nil
	}

	return textResult(string(jsonData)), nil
}

// serializePromptContent serializes prompt message content to JSON.
// This helper handles different content types and returns appropriate JSON.
func serializePromptContent(content mcp.Content) (json.RawMessage, error) {
	if textContent, ok := mcp.AsTextContent(content); ok {
		return json.Marshal(map[string]interface{}{
			"type": "text",
			"text": textContent.Text,
		})
	}
	if imageContent, ok := mcp.AsImageContent(content); ok {
		return json.Marshal(map[string]interface{}{
			"type":     "image",
			"mimeType": imageContent.MIMEType,
			"dataSize": len(imageContent.Data),
		})
	}
	if audioContent, ok := mcp.AsAudioContent(content); ok {
		return json.Marshal(map[string]interface{}{
			"type":     "audio",
			"mimeType": audioContent.MIMEType,
			"dataSize": len(audioContent.Data),
		})
	}
	if resource, ok := mcp.AsEmbeddedResource(content); ok {
		return json.Marshal(map[string]interface{}{
			"type":     "embeddedResource",
			"resource": resource.Resource,
		})
	}
	// Fallback for unknown content types
	return json.Marshal(content)
}

// textResult creates a successful text result.
func textResult(text string) *api.CallToolResult {
	return &api.CallToolResult{
		Content: []interface{}{text},
		IsError: false,
	}
}

// errorResult creates an error result.
func errorResult(message string) *api.CallToolResult {
	return &api.CallToolResult{
		Content: []interface{}{message},
		IsError: true,
	}
}
