package metatools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/toolset"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleListTools handles the list_tools meta-tool.
// This handler returns a list of all available tools from the aggregator,
// along with information about servers that require authentication.
func (p *Provider) handleListTools(ctx context.Context, _ map[string]any) (*api.CallToolResult, error) {
	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	cat, errResult := p.catalogue(ctx, handler)
	if errResult != nil {
		return errResult, nil
	}

	// Get servers requiring authentication for the current session. The list
	// is informational (which servers a sign-in would unlock) and is not
	// narrowed by the toolset: a toolset selector can only match a server's
	// tools once the caller has signed in to it.
	serversRequiringAuth := handler.ListServersRequiringAuth(ctx)

	jsonData, err := p.formatters.FormatToolsListWithAuthJSON(cat.tools, serversRequiringAuth)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format tools: %v", err)), nil
	}

	return textResult(jsonData), nil
}

// handleDescribeTool handles the describe_tool meta-tool.
// This handler returns detailed information about a specific tool.
func (p *Provider) handleDescribeTool(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return errorResult("name argument is required"), nil
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	cat, errResult := p.catalogue(ctx, handler)
	if errResult != nil {
		return errResult, nil
	}

	tool := p.formatters.FindTool(cat.tools, name)
	if tool == nil {
		if errResult := cat.outside(name); errResult != nil {
			return errResult, nil
		}
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
	// toolsetArg is the inline toolset to resolve against the caller's
	// catalogue (nil when the argument was not given; an empty list is an
	// error, like an empty header).
	toolsetArg []string
	// includePresets adds the known presets to the response.
	includePresets bool
}

// handleListCoreTools handles the list_core_tools meta-tool.
// This handler returns a filtered list of core muster tools (prefixed with "core").
// It reuses the filter_tools engine but keeps the legacy full-detail listing
// behaviour (full descriptions, schema by default, no result cap) so existing
// callers are unaffected by the discovery-tier defaults.
func (p *Provider) handleListCoreTools(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
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
func (p *Provider) handleFilterTools(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
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
	if labelsVal, ok := args["labels"].(map[string]any); ok {
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
	if raw, ok := args["toolset"]; ok && raw != nil {
		selectors, err := toStringList(raw)
		if err != nil {
			return errorResult("toolset must be an array of selector strings"), nil
		}
		opts.toolsetArg = selectors
	}
	if v, ok := args["include_presets"].(bool); ok {
		opts.includePresets = v
	}

	return p.filterToolsWithOptions(ctx, opts)
}

// toStringList coerces a JSON-decoded array (or a native string slice) to
// []string. A non-array, or an element that is not a string, is an error.
func toStringList(v any) ([]string, error) {
	switch list := v.(type) {
	case []string:
		return append([]string(nil), list...), nil
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("element %v is not a string", item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("not an array")
	}
}

// filterToolsWithOptions is the shared filter/rank/paginate engine behind both
// filter_tools (discovery) and list_core_tools (legacy listing).
func (p *Provider) filterToolsWithOptions(ctx context.Context, opts filterToolsOptions) (*api.CallToolResult, error) {
	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	cat, errResult := p.catalogue(ctx, handler)
	if errResult != nil {
		return errResult, nil
	}
	tools := cat.tools

	// The toolset argument resolves within the caller's catalogue — which,
	// when the request itself declares a toolset, is already that toolset, so
	// the argument can narrow it but never widen it.
	var argToolset *toolset.Toolset
	var argResolution toolset.Resolution
	if opts.toolsetArg != nil {
		ts, err := toolset.ParseInline(opts.toolsetArg)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		res, err := p.presets.ResolveWith(ts, toolset.EntriesFromTools(tools), p.labelsFor(ctx))
		if err != nil {
			return errorResult(err.Error()), nil
		}
		argToolset, argResolution = &ts, res
		tools = toolset.Filter(tools, res)
	}

	// Without any toolset in play the legacy text answer is kept; a toolset
	// that resolves to nothing (preset:none, a selector matching nothing) is a
	// real, structured answer the caller can read.
	if len(tools) == 0 && !cat.scoped && argToolset == nil {
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
	start := min(opts.offset, total)
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
		info.Server, info.Kind = originOf(st.tool)
		info.Annotations = annotationsOf(st.tool)
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
		TotalTools:    len(cat.tools),
		FilteredCount: len(toolInfos),
		Total:         total,
		Truncated:     truncated,
		Tools:         toolInfos,
	}
	// The response names the toolset the tools above were resolved within:
	// the argument when one was given (it resolves inside the request's
	// toolset, so it is the narrower of the two), else the toolset the request
	// itself declared (X-Muster-Toolset) — the one place an agent whose
	// toolset is set by its deployment can learn what bounds it. An unscoped
	// request carries no toolset.
	switch {
	case argToolset != nil:
		resp.Toolset = argToolset.Raw
		resp.ToolsetUnmatched = argResolution.Unmatched
	case cat.scoped:
		resp.Toolset = cat.ts.Raw
		resp.ToolsetUnmatched = cat.res.Unmatched
	}
	// Presets accompany an inline argument (the caller is composing a toolset)
	// or an explicit include_presets. A request-declared toolset alone does not
	// list them, so the discovery calls of an agent scoped by header stay as
	// small as unscoped ones.
	if opts.includePresets || argToolset != nil {
		resp.Presets = p.presets.List()
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
	case map[string]any:
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
func toInt(v any) (int, error) {
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
