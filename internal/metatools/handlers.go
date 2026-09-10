package metatools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/toolset"
	"github.com/giantswarm/muster/pkg/logging"

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
func (p *Provider) ExecuteTool(ctx context.Context, toolName string, args map[string]any) (*api.CallToolResult, error) {
	// Argument values are caller data and can carry credentials: log names only.
	logging.Debug("metatools", "Executing tool %s with args: %s", toolName, logging.KeyNames(args))

	// A request that declares a toolset must declare a valid one: an empty
	// header, a malformed, reserved or preset-only selector, more than the
	// inline cap, or an unknown preset is an error on every meta-tool call
	// (D12) — never a silent fall-back to the unscoped catalogue and never
	// silent degradation to the selectors that did parse.
	if ts, present, err := toolset.FromContext(ctx); err != nil {
		return errorResult(err.Error()), nil
	} else if present {
		if err := p.presets.Check(ts); err != nil {
			return errorResult(err.Error()), nil
		}
	}

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
	case "filter_resources":
		return p.handleFilterResources(ctx, args)
	case "describe_resource":
		return p.handleDescribeResource(ctx, args)
	case "get_resource":
		return p.handleGetResource(ctx, args)
	case "list_prompts":
		return p.handleListPrompts(ctx, args)
	case "filter_prompts":
		return p.handleFilterPrompts(ctx, args)
	case "describe_prompt":
		return p.handleDescribePrompt(ctx, args)
	case "get_prompt":
		return p.handleGetPrompt(ctx, args)
	default:
		return nil, fmt.Errorf("unknown meta-tool: %s", toolName)
	}
}

// handleCallTool handles the call_tool meta-tool.
// This handler executes any tool by name with the provided arguments.
// It preserves the full CallToolResult structure for proper unwrapping.
func (p *Provider) handleCallTool(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return errorResult("name argument is required"), nil
	}

	// Get arguments if provided
	var toolArgs map[string]any
	if argsRaw := args["arguments"]; argsRaw != nil {
		var ok bool
		toolArgs, ok = argsRaw.(map[string]any)
		if !ok {
			return errorResult("arguments must be a JSON object"), nil
		}
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	// A declared toolset bounds what the model may call: the refusal names
	// the tool and the toolset, and is logged with the session so an operator
	// can see which agent asked for what. Workflow execution (workflow_<name>)
	// goes through the same gate; the tools a workflow's steps call
	// internally are the workflow author's composition, not the model's, and
	// are not re-checked here.
	if cat, errResult := p.scope(ctx, handler); errResult != nil {
		return errResult, nil
	} else if errResult := cat.refuse(ctx, name); errResult != nil {
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
		IsError           bool  `json:"isError"`
		Content           []any `json:"content"`
		StructuredContent any   `json:"structuredContent,omitempty"`
	}{
		IsError:           result.IsError,
		Content:           SerializeContent(result.Content),
		StructuredContent: result.StructuredContent,
	})
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to serialize result: %v", err)), nil
	}

	// Propagate the underlying tool's error status to the outer wrapper so that
	// MCP clients inspecting only the top-level isError field get an accurate signal.
	// StructuredContent is propagated both natively (for clients that read the MCP
	// structuredContent field directly) and inside the JSON envelope (for clients
	// that unwrap the text content). The native Content is the envelope JSON while
	// the native StructuredContent mirrors the wrapped tool: the two fields describe
	// different layers by design and are not meant to agree.
	return &api.CallToolResult{
		Content:           []any{string(resultJSON)},
		IsError:           result.IsError,
		StructuredContent: result.StructuredContent,
	}, nil
}

// handleListResources handles the list_resources meta-tool.
// This handler returns a list of all available resources.
func (p *Provider) handleListResources(ctx context.Context, _ map[string]any) (*api.CallToolResult, error) {
	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	resources, _, errResult := p.scopedResources(ctx, handler)
	if errResult != nil {
		return errResult, nil
	}

	jsonData, err := p.formatters.FormatResourcesListJSON(resources)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format resources: %v", err)), nil
	}

	return textResult(jsonData), nil
}

// handleDescribeResource handles the describe_resource meta-tool.
// This handler returns detailed information about a specific resource.
func (p *Provider) handleDescribeResource(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	uri, ok := args["uri"].(string)
	if !ok || uri == "" {
		return errorResult("uri argument is required"), nil
	}

	// Optional: disambiguates a URI exposed by more than one server.
	serverName, _ := args[ArgServer].(string)

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	resources, cat, errResult := p.scopedResources(ctx, handler)
	if errResult != nil {
		return errResult, nil
	}

	matches := p.formatters.FindResource(resources, uri)
	if len(matches) == 0 {
		if cat.isScoped() {
			return cat.outsideError("resource", uri), nil
		}
		return errorResult(fmt.Sprintf("Resource not found: %s", uri)), nil
	}

	// A URI carrying a scheme is exposed unprefixed, so several servers can
	// advertise the same one. Report the choice instead of describing whichever
	// happened to be listed first.
	resource := matches[0]
	if len(matches) > 1 {
		if serverName == "" {
			servers := make([]string, len(matches))
			for i, m := range matches {
				servers[i] = m.Server
			}
			sort.Strings(servers)
			return errorResult(fmt.Sprintf("Resource %s is exposed by servers %s; the \"server\" argument is required",
				uri, strings.Join(servers, ", "))), nil
		}
		found := false
		for _, m := range matches {
			if m.Server == serverName {
				resource, found = m, true
				break
			}
		}
		if !found {
			return errorResult(fmt.Sprintf("Resource %s is not available on server %q", uri, serverName)), nil
		}
	}

	jsonData, err := p.formatters.FormatResourceDetailJSON(resource)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format resource info: %v", err)), nil
	}

	return textResult(jsonData), nil
}

// handleGetResource handles the get_resource meta-tool.
// This handler retrieves the contents of a resource.
func (p *Provider) handleGetResource(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	uri, ok := args["uri"].(string)
	if !ok || uri == "" {
		return errorResult("uri argument is required"), nil
	}

	// Optional: disambiguates a URI exposed by more than one server.
	serverName, _ := args[ArgServer].(string)

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	// With a toolset declared, a resource is readable only when its server is
	// inside the toolset. Without one the read is unchanged.
	if _, present, _ := toolset.FromContext(ctx); present {
		resources, cat, errResult := p.scopedResources(ctx, handler)
		if errResult != nil {
			return errResult, nil
		}
		inside := false
		for _, m := range p.formatters.FindResource(resources, uri) {
			if serverName == "" || m.Server == serverName {
				inside = true
				break
			}
		}
		if !inside {
			return cat.outsideError("resource", uri), nil
		}
	}

	result, err := handler.GetResource(ctx, uri, serverName)
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

// capabilityFilterArgs parses the arguments shared by filter_resources and
// filter_prompts. Unlike filter_tools these take no relevance query or label
// facets -- resources and prompts carry neither.
func parseCapabilityFilterArgs(args map[string]any) (CapabilityFilterCriteria, *api.CallToolResult) {
	opts := CapabilityFilterCriteria{Limit: defaultFilterLimit}

	if v, ok := args["pattern"].(string); ok {
		opts.Pattern = v
	}
	if v, ok := args[ArgServer].(string); ok {
		opts.Server = v
	}
	if v, ok := args["case_sensitive"].(bool); ok {
		opts.CaseSensitive = v
	}
	if v, ok := args["limit"]; ok {
		limit, err := toInt(v)
		if err != nil {
			return opts, errorResult("limit must be a number")
		}
		if limit < 1 {
			return opts, errorResult("limit must be at least 1")
		}
		opts.Limit = limit
	}
	if v, ok := args["offset"]; ok {
		offset, err := toInt(v)
		if err != nil {
			return opts, errorResult("offset must be a number")
		}
		if offset < 0 {
			return opts, errorResult("offset must be at least 0")
		}
		opts.Offset = offset
	}

	if opts.Pattern != "" {
		if _, err := filepath.Match(opts.Pattern, ""); err != nil {
			return opts, errorResult(fmt.Sprintf("Invalid pattern %q: %v", opts.Pattern, err))
		}
	}
	return opts, nil
}

// paginate returns the [start:end) window of total items for the given
// limit/offset, plus whether matches remain beyond the returned page.
func paginate(total, limit, offset int) (start, end int, truncated bool) {
	start = min(offset, total)
	end = total
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	return start, end, end < total
}

// handleFilterResources handles the filter_resources meta-tool.
//
// Resources are scoped by source server rather than by name prefix: a URI
// carrying a scheme is exposed unprefixed, so unlike filter_tools a pattern
// alone cannot select one server's resources. The "server" argument is the
// reliable way to do that; "pattern" additionally globs the URI.
func (p *Provider) handleFilterResources(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	opts, errResult := parseCapabilityFilterArgs(args)
	if errResult != nil {
		return errResult, nil
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	resources, _, errResult := p.scopedResources(ctx, handler)
	if errResult != nil {
		return errResult, nil
	}

	matched := make([]api.ResourceOrigin, 0, len(resources))
	for _, origin := range resources {
		if opts.Server != "" && origin.Server != opts.Server {
			continue
		}
		if !matchesPattern(origin.Resource.URI, opts.Pattern, opts.CaseSensitive) {
			continue
		}
		matched = append(matched, origin)
	}

	start, end, truncated := paginate(len(matched), opts.Limit, opts.Offset)
	page := matched[start:end]

	infos := make([]ResourceInfo, 0, len(page))
	for _, origin := range page {
		infos = append(infos, ResourceInfo{
			URI:         origin.Resource.URI,
			Name:        origin.Resource.Name,
			Description: origin.Resource.Description,
			MIMEType:    origin.Resource.MIMEType,
			Server:      origin.Server,
		})
	}

	resp := FilterResourcesResponse{
		Filters:        opts,
		TotalResources: len(resources),
		FilteredCount:  len(infos),
		Total:          len(matched),
		Truncated:      truncated,
		Resources:      infos,
	}

	jsonData, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format filtered resources: %v", err)), nil
	}
	return textResult(string(jsonData)), nil
}

// handleFilterPrompts handles the filter_prompts meta-tool.
//
// The "server" argument matches the source server recorded on each prompt, not
// the exposed name. A prompt name is prefixed with the server's *configured*
// tool prefix (spec.toolPrefix) rather than its name -- a server named
// "gazelle-mcp-pro" with prefix "pro" exposes "x_pro_<name>" -- so matching the
// server name against the exposed name would find nothing for any server whose
// prefix differs from its name. "pattern" still globs the exposed name.
func (p *Provider) handleFilterPrompts(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	opts, errResult := parseCapabilityFilterArgs(args)
	if errResult != nil {
		return errResult, nil
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	prompts, _, errResult := p.scopedPrompts(ctx, handler)
	if errResult != nil {
		return errResult, nil
	}

	matched := make([]api.PromptOrigin, 0, len(prompts))
	for _, origin := range prompts {
		if opts.Server != "" && origin.Server != opts.Server {
			continue
		}
		if !matchesPattern(origin.Prompt.Name, opts.Pattern, opts.CaseSensitive) {
			continue
		}
		matched = append(matched, origin)
	}

	start, end, truncated := paginate(len(matched), opts.Limit, opts.Offset)
	page := matched[start:end]

	infos := make([]PromptInfo, 0, len(page))
	for _, origin := range page {
		infos = append(infos, PromptInfo{
			Name:        origin.Prompt.Name,
			Description: origin.Prompt.Description,
			Server:      origin.Server,
		})
	}

	resp := FilterPromptsResponse{
		Filters:       opts,
		TotalPrompts:  len(prompts),
		FilteredCount: len(infos),
		Total:         len(matched),
		Truncated:     truncated,
		Prompts:       infos,
	}

	jsonData, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format filtered prompts: %v", err)), nil
	}
	return textResult(string(jsonData)), nil
}

// handleListPrompts handles the list_prompts meta-tool.
// This handler returns a list of all available prompts.
func (p *Provider) handleListPrompts(ctx context.Context, _ map[string]any) (*api.CallToolResult, error) {
	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	prompts, _, errResult := p.scopedPrompts(ctx, handler)
	if errResult != nil {
		return errResult, nil
	}

	jsonData, err := p.formatters.FormatPromptsListJSON(prompts)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format prompts: %v", err)), nil
	}

	return textResult(jsonData), nil
}

// handleDescribePrompt handles the describe_prompt meta-tool.
// This handler returns detailed information about a specific prompt.
func (p *Provider) handleDescribePrompt(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return errorResult("name argument is required"), nil
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	prompts, cat, errResult := p.scopedPrompts(ctx, handler)
	if errResult != nil {
		return errResult, nil
	}

	prompt := p.formatters.FindPrompt(prompts, name)
	if prompt == nil {
		if cat.isScoped() {
			return cat.outsideError("prompt", name), nil
		}
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
func (p *Provider) handleGetPrompt(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return errorResult("name argument is required"), nil
	}

	// Get arguments if provided and convert to string map
	promptArgs := make(map[string]string)
	if argsRaw := args["arguments"]; argsRaw != nil {
		argsMap, ok := argsRaw.(map[string]any)
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

	// With a toolset declared, a prompt is available only when its server is
	// inside the toolset. Without one the call is unchanged.
	if _, present, _ := toolset.FromContext(ctx); present {
		prompts, cat, errResult := p.scopedPrompts(ctx, handler)
		if errResult != nil {
			return errResult, nil
		}
		if p.formatters.FindPrompt(prompts, name) == nil {
			return cat.outsideError("prompt", name), nil
		}
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
		return json.Marshal(map[string]any{
			"type": "text",
			"text": textContent.Text,
		})
	}
	if imageContent, ok := mcp.AsImageContent(content); ok {
		return json.Marshal(map[string]any{
			"type":            "image",
			api.FieldMimeType: imageContent.MIMEType,
			"dataSize":        len(imageContent.Data),
		})
	}
	if audioContent, ok := mcp.AsAudioContent(content); ok {
		return json.Marshal(map[string]any{
			"type":            "audio",
			api.FieldMimeType: audioContent.MIMEType,
			"dataSize":        len(audioContent.Data),
		})
	}
	if resource, ok := mcp.AsEmbeddedResource(content); ok {
		return json.Marshal(map[string]any{
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
		Content: []any{text},
		IsError: false,
	}
}

// errorResult creates an error result.
func errorResult(message string) *api.CallToolResult {
	return &api.CallToolResult{
		Content: []any{message},
		IsError: true,
	}
}
