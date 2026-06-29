package metatools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/giantswarm/muster/internal/api"
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

// defaultFilterLimit caps how many tools the discovery tier returns per
// filter_tools call when the caller does not specify a limit. Discovery is a
// "find the right tool" step, not a listing: a small ranked top-K is enough for
// a caller (typically an LLM) to pick from, and a small default keeps the page
// — which then rides in the model's context for every following step — cheap.
// Callers that want more can page explicitly with limit/offset.
const defaultFilterLimit = 5

// summaryMaxLen caps the length (in runes) of the one-line summary the
// discovery tier emits in place of a tool's full description.
const summaryMaxLen = 120

// filterToolsOptions configures a single tool-discovery query. It is built
// explicitly by each caller: handleFilterTools applies cheap discovery defaults
// (summaries, no schema, capped page), while handleListCoreTools reproduces the
// legacy full-detail listing (full descriptions, schema, no cap).
type filterToolsOptions struct {
	pattern           string
	descriptionFilter string
	query             string
	labels            map[string]string
	caseSensitive     bool
	includeSchema     bool
	summarize         bool
	limit             int // 0 means no limit
	offset            int
}

// handleListCoreTools handles the list_core_tools meta-tool.
// This handler returns a filtered list of core muster tools (prefixed with "core").
// It reuses the filter_tools engine but keeps the legacy full-detail listing
// behaviour (full descriptions, schema by default, no result cap) so existing
// callers are unaffected by the discovery-tier defaults.
func (p *Provider) handleListCoreTools(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	opts := filterToolsOptions{
		pattern:       "core*",
		caseSensitive: false,
		includeSchema: true,
		summarize:     false,
		limit:         0, // no cap: the core tool set is small and bounded
	}
	if schemaVal, ok := args["include_schema"].(bool); ok {
		opts.includeSchema = schemaVal
	}
	return p.filterToolsWithOptions(ctx, opts)
}

// handleFilterTools handles the filter_tools meta-tool.
//
// This is the discovery tier: it filters by name pattern, description substring,
// and label facets, optionally ranks by relevance to a natural-language query,
// and returns a bounded, summarised page. Full descriptions and input schemas
// are omitted by default (opt in via include_schema); the authoritative detail
// remains available through describe_tool.
func (p *Provider) handleFilterTools(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	opts := filterToolsOptions{
		includeSchema: false,
		summarize:     true,
		limit:         defaultFilterLimit,
	}

	if patternVal, ok := args["pattern"].(string); ok {
		opts.pattern = patternVal
	}
	if descFilterVal, ok := args["description_filter"].(string); ok {
		opts.descriptionFilter = descFilterVal
	}
	if queryVal, ok := args["query"].(string); ok {
		opts.query = queryVal
	}
	if caseVal, ok := args["case_sensitive"].(bool); ok {
		opts.caseSensitive = caseVal
	}
	if schemaVal, ok := args["include_schema"].(bool); ok {
		opts.includeSchema = schemaVal
	}
	// Opting into schemas signals a request for full detail, so the full
	// description is returned instead of the one-line summary.
	opts.summarize = !opts.includeSchema
	if labelsVal, ok := args["labels"].(map[string]interface{}); ok {
		labels := make(map[string]string, len(labelsVal))
		for k, v := range labelsVal {
			labels[k] = fmt.Sprintf("%v", v)
		}
		opts.labels = labels
	}

	if limitVal, ok := args["limit"]; ok {
		limit, err := toInt(limitVal)
		if err != nil {
			return errorResult("limit must be a number"), nil
		}
		if limit < 1 {
			return errorResult("limit must be at least 1"), nil
		}
		opts.limit = limit
	}
	if offsetVal, ok := args["offset"]; ok {
		offset, err := toInt(offsetVal)
		if err != nil {
			return errorResult("offset must be a number"), nil
		}
		if offset < 0 {
			return errorResult("offset must be at least 0"), nil
		}
		opts.offset = offset
	}

	return p.filterToolsWithOptions(ctx, opts)
}

// filterToolsWithOptions is the shared filter/rank/paginate engine behind both
// filter_tools (discovery) and list_core_tools (legacy listing).
func (p *Provider) filterToolsWithOptions(ctx context.Context, opts filterToolsOptions) (*api.CallToolResult, error) {
	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	tools, err := handler.ListTools(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to list tools: %v", err)), nil
	}
	if len(tools) == 0 {
		return textResult("No tools available to filter"), nil
	}

	if opts.pattern != "" {
		if _, err := filepath.Match(opts.pattern, ""); err != nil {
			return errorResult(fmt.Sprintf("Invalid pattern %q: %v", opts.pattern, err)), nil
		}
	}

	// 1. Filter by pattern, description substring, and label facets.
	matched := make([]mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		if !matchesPattern(tool.Name, opts.pattern, opts.caseSensitive) {
			continue
		}
		if !matchesDescription(tool.Description, opts.descriptionFilter, opts.caseSensitive) {
			continue
		}
		if !matchesLabels(tool, opts.labels) {
			continue
		}
		matched = append(matched, tool)
	}

	// 2. Rank by relevance when a query is given; otherwise keep input order.
	type scoredTool struct {
		tool   mcp.Tool
		score  float64
		scored bool
	}
	var ordered []scoredTool
	if opts.query != "" {
		docs := make([]rankDoc, len(matched))
		for i, t := range matched {
			docs[i] = rankDoc{name: t.Name, description: summarizeText(t.Description, 0)}
		}
		for _, rd := range rankBM25(opts.query, docs) {
			ordered = append(ordered, scoredTool{tool: matched[rd.index], score: rd.score, scored: true})
		}
	} else {
		for _, t := range matched {
			ordered = append(ordered, scoredTool{tool: t})
		}
	}

	// 3. Paginate.
	total := len(ordered)
	start := opts.offset
	if start > total {
		start = total
	}
	end := total
	if opts.limit > 0 && start+opts.limit < end {
		end = start + opts.limit
	}
	page := ordered[start:end]
	truncated := end < total // more matches exist beyond this page

	// 4. Project each tool to a discovery- or detail-shaped entry.
	toolInfos := make([]ToolInfo, 0, len(page))
	for _, st := range page {
		info := ToolInfo{Name: st.tool.Name}
		if opts.summarize {
			info.Summary = summarizeText(st.tool.Description, summaryMaxLen)
		} else {
			info.Description = st.tool.Description
		}
		if st.scored {
			info.Score = roundScore(st.score)
		}
		if labels := toolLabels(st.tool); len(labels) > 0 {
			info.Labels = labels
		}
		if opts.includeSchema {
			info.InputSchema = st.tool.InputSchema
		}
		toolInfos = append(toolInfos, info)
	}

	resp := FilterToolsResponse{
		Filters: FilterCriteria{
			Pattern:           opts.pattern,
			DescriptionFilter: opts.descriptionFilter,
			Query:             opts.query,
			Labels:            opts.labels,
			CaseSensitive:     opts.caseSensitive,
			IncludeSchema:     opts.includeSchema,
			Limit:             opts.limit,
			Offset:            opts.offset,
		},
		TotalTools:    len(tools),
		FilteredCount: len(toolInfos),
		Total:         total,
		Truncated:     truncated,
		Tools:         toolInfos,
	}

	jsonData, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format filtered tools: %v", err)), nil
	}

	return textResult(string(jsonData)), nil
}

// matchesPattern reports whether name matches the glob pattern (empty pattern
// matches everything). The pattern is assumed already validated by the caller.
func matchesPattern(name, pattern string, caseSensitive bool) bool {
	if pattern == "" {
		return true
	}
	if !caseSensitive {
		name = strings.ToLower(name)
		pattern = strings.ToLower(pattern)
	}
	ok, _ := filepath.Match(pattern, name)
	return ok
}

// matchesDescription reports whether desc contains the filter substring (empty
// filter matches everything).
func matchesDescription(desc, filter string, caseSensitive bool) bool {
	if filter == "" {
		return true
	}
	if !caseSensitive {
		desc = strings.ToLower(desc)
		filter = strings.ToLower(filter)
	}
	return strings.Contains(desc, filter)
}

// matchesLabels reports whether the tool carries every requested label
// (key=value). An empty want set matches everything.
func matchesLabels(tool mcp.Tool, want map[string]string) bool {
	if len(want) == 0 {
		return true
	}
	have := toolLabels(tool)
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

// toolLabels extracts the discovery labels stashed in the tool's _meta by the
// aggregator. Returns nil when the tool carries none.
func toolLabels(tool mcp.Tool) map[string]string {
	if tool.Meta == nil || tool.Meta.AdditionalFields == nil {
		return nil
	}
	raw, ok := tool.Meta.AdditionalFields[api.MetaKeyLabels]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case map[string]string:
		return v
	case map[string]interface{}:
		m := make(map[string]string, len(v))
		for k, val := range v {
			m[k] = fmt.Sprintf("%v", val)
		}
		return m
	default:
		return nil
	}
}

// summarizeText returns the first line of desc, trimmed, and capped to maxRunes
// runes (maxRunes <= 0 means no cap). It is the cheap one-line excerpt the
// discovery tier returns in place of a full description.
func summarizeText(desc string, maxRunes int) string {
	s := desc
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if maxRunes > 0 {
		if r := []rune(s); len(r) > maxRunes {
			s = strings.TrimSpace(string(r[:maxRunes])) + "..."
		}
	}
	return s
}

// roundScore rounds a relevance score to 4 decimal places for stable, compact
// output. A positive score never collapses to exactly 0, so a ranked result
// always serialises a non-zero score despite the Score field's omitempty tag.
func roundScore(s float64) float64 {
	r := math.Round(s*10000) / 10000
	if r == 0 && s > 0 {
		r = 0.0001 // smallest value at this precision; keeps the score present
	}
	return r
}

// toInt coerces a JSON-decoded numeric value (float64) or a native int to int.
func toInt(v interface{}) (int, error) {
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case int64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("not a number")
	}
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

	// Propagate the underlying tool's error status to the outer wrapper so that
	// MCP clients inspecting only the top-level isError field get an accurate signal.
	return &api.CallToolResult{
		Content: []interface{}{string(resultJSON)},
		IsError: result.IsError,
	}, nil
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
			"type":            "image",
			api.FieldMimeType: imageContent.MIMEType,
			"dataSize":        len(imageContent.Data),
		})
	}
	if audioContent, ok := mcp.AsAudioContent(content); ok {
		return json.Marshal(map[string]interface{}{
			"type":            "audio",
			api.FieldMimeType: audioContent.MIMEType,
			"dataSize":        len(audioContent.Data),
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
