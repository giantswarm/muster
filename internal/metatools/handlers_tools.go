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

// defaultListLimit caps how many tools list_tools returns per page when the
// caller does not specify a limit. A summarised entry costs a few hundred
// bytes, so 50 keeps the default answer in the tens of kilobytes — a page a
// model can read — where the unpaged listing of a 450-tool toolset measured
// 400 KB (≈135k tokens, #1193). Callers page explicitly with limit/offset;
// total and truncated tell them there is more.
const defaultListLimit = 50

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

// filterToolsOptions configures a single query against the caller's catalogue.
// It is built explicitly by each caller: handleFilterTools applies cheap
// discovery defaults (summaries, no schema, capped page), handleListTools
// lists the catalogue in summarised pages, and handleListCoreTools reproduces
// the legacy full-detail listing (full descriptions, schema, no cap).
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

// handleListTools handles the list_tools meta-tool.
//
// It answers with one bounded page of the caller's catalogue — the session's
// tools narrowed by the request's toolset — every entry summarised the way the
// discovery tier does, plus the servers a sign-in would unlock. Paging (limit,
// offset, total, truncated) means a caller can never receive hundreds of full
// descriptions without asking for them: the unpaged listing measured 400 KB
// for a 450-tool toolset (#1193). The full description and the input schema
// of a tool remain available through describe_tool.
func (p *Provider) handleListTools(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	opts := filterToolsOptions{summarize: true, limit: defaultListLimit}
	if errResult := parsePageArgs(args, &opts.limit, &opts.offset); errResult != nil {
		return errResult, nil
	}

	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	page, errResult := p.pageTools(ctx, handler, opts)
	if errResult != nil {
		return errResult, nil
	}

	// The servers requiring authentication are informational (which sign-in
	// would unlock more tools) and are not narrowed by the toolset: a toolset
	// selector can only match a server's tools once the caller has signed in
	// to it. Nor are they paged — the list is a handful of names.
	return jsonResult(ListToolsResponse{
		FilterToolsResponse:  *page,
		ServersRequiringAuth: handler.ListServersRequiringAuth(ctx),
	}, "tools")
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

	if errResult := parsePageArgs(args, &opts.limit, &opts.offset); errResult != nil {
		return errResult, nil
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

// parsePageArgs reads the optional limit and offset arguments every paging
// meta-tool shares, replacing the given defaults. A limit below 1 or an
// offset below 0 is an error result; a nil args map leaves the defaults.
func parsePageArgs(args map[string]any, limit, offset *int) *api.CallToolResult {
	if v, ok := args[ArgLimit]; ok {
		n, err := toInt(v)
		if err != nil {
			return errorResult("limit must be a number")
		}
		if n < 1 {
			return errorResult("limit must be at least 1")
		}
		*limit = n
	}
	if v, ok := args[ArgOffset]; ok {
		n, err := toInt(v)
		if err != nil {
			return errorResult("offset must be a number")
		}
		if n < 0 {
			return errorResult("offset must be at least 0")
		}
		*offset = n
	}
	return nil
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

// filterToolsWithOptions runs the engine for filter_tools and list_core_tools
// and serialises the page. An unscoped request whose catalogue is empty keeps
// the legacy text answer; a toolset that resolves to nothing (preset:none, a
// selector matching nothing) is a structured answer the caller can read.
func (p *Provider) filterToolsWithOptions(ctx context.Context, opts filterToolsOptions) (*api.CallToolResult, error) {
	handler, errResult := p.getHandler()
	if errResult != nil {
		return errResult, nil
	}

	page, errResult := p.pageTools(ctx, handler, opts)
	if errResult != nil {
		return errResult, nil
	}

	if _, scoped, _ := toolset.FromContext(ctx); page.TotalTools == 0 && !scoped && opts.toolsetArg == nil {
		return textResult("No tools available to filter"), nil
	}

	return jsonResult(page, "filtered tools")
}

// pageTools is the shared filter/rank/paginate engine behind list_tools,
// filter_tools and list_core_tools. It reads the caller's catalogue — the
// session's tools narrowed by the request's toolset — applies the options and
// returns one structured page.
func (p *Provider) pageTools(ctx context.Context, handler api.MetaToolsHandler, opts filterToolsOptions) (*FilterToolsResponse, *api.CallToolResult) {
	cat, errResult := p.catalogue(ctx, handler)
	if errResult != nil {
		return nil, errResult
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
			return nil, errorResult(err.Error())
		}
		res, err := p.presets.ResolveWith(ts, toolset.EntriesFromTools(tools), p.labelsFor(ctx))
		if err != nil {
			return nil, errorResult(err.Error())
		}
		argToolset, argResolution = &ts, res
		tools = toolset.Filter(tools, res)
	}

	if opts.pattern != "" {
		if _, err := filepath.Match(opts.pattern, ""); err != nil {
			return nil, errorResult(fmt.Sprintf("Invalid pattern %q: %v", opts.pattern, err))
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
	start, end, truncated := paginate(len(ordered), opts.limit, opts.offset)
	page := ordered[start:end]

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

	resp := &FilterToolsResponse{
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
		Total:         len(ordered),
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
	return resp, nil
}

// jsonResult serialises v as the indented JSON text of a successful result;
// what names the payload in the error result a marshalling failure produces.
func jsonResult(v any, what string) (*api.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format %s: %v", what, err)), nil
	}
	return textResult(string(data)), nil
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
