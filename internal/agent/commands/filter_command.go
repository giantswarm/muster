package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/metatools"

	"github.com/mark3labs/mcp-go/mcp"
)

// FilterCommand filters tools by pattern and description
type FilterCommand struct {
	*BaseCommand
}

// NewFilterCommand creates a new filter command
func NewFilterCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *FilterCommand {
	return &FilterCommand{
		BaseCommand: NewBaseCommand(client, output, transport),
	}
}

// Execute filters tools based on the discovery-tier options.
//
// After the "tools" target, options are given as key=value pairs (pattern,
// description, query, labels, case_sensitive, detailed, limit, offset). A bare
// argument with no "=" is treated as the name pattern, so the common
// "filter tools core_*" shorthand keeps working.
func (f *FilterCommand) Execute(ctx context.Context, args []string) error {
	parsed, err := f.parseArgs(args, 1, f.Usage())
	if err != nil {
		return err
	}

	target := strings.ToLower(parsed[0])
	if target != api.FieldTools {
		return f.validateTarget(target, []string{api.FieldTools})
	}

	toolArgs, detailed, err := f.buildFilterArgs(parsed[1:])
	if err != nil {
		return err
	}

	return f.filterTools(ctx, toolArgs, detailed)
}

// buildFilterArgs translates REPL key=value options into filter_tools arguments.
// It returns the argument map, whether full detail (schemas) was requested, and
// any parse error. A bare token (no "=") is treated as the name pattern.
func (f *FilterCommand) buildFilterArgs(rest []string) (map[string]interface{}, bool, error) {
	toolArgs := make(map[string]interface{})
	detailed := false

	for _, arg := range rest {
		key, value, hasEq := strings.Cut(arg, "=")
		if !hasEq {
			if _, ok := toolArgs["pattern"]; ok {
				return nil, false, fmt.Errorf("unexpected argument %q: options are now key=value pairs (%s); only the name pattern may be given without a key", arg, strings.Join(filterOptionKeys, ", "))
			}
			toolArgs["pattern"] = arg
			continue
		}
		value = stripQuotes(value)

		switch strings.ToLower(key) {
		case "pattern":
			toolArgs["pattern"] = value
		case "description", "description_filter":
			toolArgs["description_filter"] = value
		case "query":
			toolArgs["query"] = value
		case "labels":
			labels, err := parseLabelSelector(value)
			if err != nil {
				return nil, false, err
			}
			toolArgs["labels"] = labels
		case "case_sensitive", "case":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return nil, false, fmt.Errorf("case_sensitive must be true or false: %w", err)
			}
			toolArgs["case_sensitive"] = b
		case "detailed", "include_schema":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return nil, false, fmt.Errorf("%s must be true or false: %w", key, err)
			}
			toolArgs["include_schema"] = b
			detailed = b
		case "limit":
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, false, fmt.Errorf("limit must be a number: %w", err)
			}
			toolArgs["limit"] = n
		case "offset":
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, false, fmt.Errorf("offset must be a number: %w", err)
			}
			toolArgs["offset"] = n
		default:
			return nil, false, fmt.Errorf("unknown filter option %q: valid options are %s", key, strings.Join(filterOptionKeys, ", "))
		}
	}

	return toolArgs, detailed, nil
}

// parseLabelSelector parses a "key=value,key2=value2" label selector into a map.
func parseLabelSelector(s string) (map[string]string, error) {
	labels := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("label %q must be key=value", pair)
		}
		labels[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if len(labels) == 0 {
		return nil, fmt.Errorf("labels must contain at least one key=value pair")
	}
	return labels, nil
}

// filterTools calls the filter_tools meta-tool and renders the response.
// This returns actual tools (core_*, x_*, workflow_*) rather than meta-tools.
func (f *FilterCommand) filterTools(ctx context.Context, toolArgs map[string]interface{}, detailed bool) error {
	result, err := f.client.CallTool(ctx, metatools.ToolFilterTools, toolArgs)
	if err != nil {
		return fmt.Errorf("failed to filter tools: %w", err)
	}

	if result.IsError {
		f.output.Error("Error filtering tools:")
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				f.output.OutputLine("  %s", textContent.Text)
			}
		}
		return nil
	}

	for _, content := range result.Content {
		textContent, ok := mcp.AsTextContent(content)
		if !ok {
			continue
		}

		var response metatools.FilterToolsResponse
		if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
			// Not JSON, just output the raw text
			f.output.OutputLine("%s", textContent.Text)
			return nil
		}

		f.renderResponse(response, detailed)
		return nil
	}

	f.output.OutputLine("No tools available to filter")
	return nil
}

// renderResponse prints the applied filters, the match/page counts, and the
// matching tools (brief by default, full specifications when detailed).
func (f *FilterCommand) renderResponse(response metatools.FilterToolsResponse, detailed bool) {
	f.printFilterSummary(response)

	if len(response.Tools) == 0 {
		f.output.OutputLine("No tools match the specified filters.")
		return
	}

	if detailed {
		f.output.OutputLine("\nFiltered Tools with Full Specifications:")
		f.output.OutputLine("%s", strings.Repeat("=", 60))

		for i, tool := range response.Tools {
			f.output.OutputLine("\n%d. %s", i+1, tool.Name)
			f.output.OutputLine("   Description: %s", toolText(tool))
			if len(tool.Labels) > 0 {
				f.output.OutputLine("   Labels: %s", formatLabels(tool.Labels))
			}
			if tool.InputSchema != nil {
				if schemaJSON, err := json.MarshalIndent(tool.InputSchema, "  ", "  "); err == nil {
					f.output.OutputLine("   Schema: %s", string(schemaJSON))
				}
			}
			if i < len(response.Tools)-1 {
				f.output.OutputLine("%s", strings.Repeat("-", 40))
			}
		}
		return
	}

	// Tools are already returned best-first when ranked, so the order conveys
	// relevance; we omit the raw BM25 score here as it is unbounded and not
	// meaningful to a human (it stays in the JSON response for programmatic use).
	width := nameColumnWidth(response.Tools)
	f.output.OutputLine("\nMatching tools:")
	for i, tool := range response.Tools {
		line := fmt.Sprintf("  %d. %-*s - %s", i+1, width, tool.Name, toolText(tool))
		if len(tool.Labels) > 0 {
			line += fmt.Sprintf("  {%s}", formatLabels(tool.Labels))
		}
		f.output.OutputLine("%s", line)
	}
}

// nameColumnWidth returns the column width for tool names in brief mode: the
// longest name on the page, capped so a single very long name cannot push the
// description column off the screen.
func nameColumnWidth(tools []metatools.ToolInfo) int {
	const maxWidth = 50
	width := 0
	for _, tool := range tools {
		if n := len(tool.Name); n > width {
			width = n
		}
	}
	if width > maxWidth {
		return maxWidth
	}
	return width
}

// printFilterSummary reports the applied filters and an accurate match/page
// count. The page (len(Tools)) is shown against Total (matches across the whole
// catalogue) and TotalTools (catalogue size); when more matches exist beyond the
// page, it prints how to fetch them.
func (f *FilterCommand) printFilterSummary(response metatools.FilterToolsResponse) {
	filters := response.Filters
	if filters.Pattern != "" || filters.DescriptionFilter != "" || filters.Query != "" || len(filters.Labels) > 0 {
		f.output.Info("Filtering tools with:")
		if filters.Pattern != "" {
			f.output.Info("  Pattern: %s", filters.Pattern)
		}
		if filters.DescriptionFilter != "" {
			f.output.Info("  Description filter: %s", filters.DescriptionFilter)
		}
		if filters.Query != "" {
			f.output.Info("  Query: %s", filters.Query)
		}
		if len(filters.Labels) > 0 {
			f.output.Info("  Labels: %s", formatLabels(filters.Labels))
		}
		f.output.Info("  Case sensitive: %t", filters.CaseSensitive)
	}

	f.output.Info("Showing %d of %d matching tool(s) (catalogue: %d)", len(response.Tools), response.Total, response.TotalTools)
	if response.Truncated {
		f.output.Info("More matches available — narrow the filters or page with offset=%d", filters.Offset+len(response.Tools))
	}
}

// toolText returns the human-readable line for a filtered tool, preferring the
// full description and falling back to the discovery-tier one-line summary.
func toolText(tool metatools.ToolInfo) string {
	if tool.Description != "" {
		return tool.Description
	}
	return tool.Summary
}

// formatLabels renders a label map as a stable, comma-separated key=value list.
func formatLabels(labels map[string]string) string {
	pairs := make([]string, 0, len(labels))
	for k, v := range labels {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ", ")
}

// Usage returns the usage string
func (f *FilterCommand) Usage() string {
	return "filter tools [pattern] [key=value ...] - keys: pattern, description, query, labels (k=v,k2=v2), case_sensitive, detailed, limit, offset"
}

// Description returns the command description
func (f *FilterCommand) Description() string {
	return "Discover tools by name pattern, description, labels, or a ranked query"
}

// filterOptionKeys are the canonical key=value option names. They are the single
// source of truth for error messages and REPL completions.
var filterOptionKeys = []string{
	"pattern", "description", "query", "labels",
	"case_sensitive", "detailed", "limit", "offset",
}

// Completions returns possible completions
func (f *FilterCommand) Completions(input string) []string {
	parts := strings.Fields(input)

	if len(parts) <= 1 {
		return []string{api.FieldTools}
	}

	// Offer only option keys that have not been supplied yet.
	used := make(map[string]struct{})
	for _, part := range parts[1:] {
		if key, _, ok := strings.Cut(part, "="); ok {
			used[strings.ToLower(key)] = struct{}{}
		}
	}

	completions := make([]string, 0, len(filterOptionKeys))
	for _, key := range filterOptionKeys {
		if _, seen := used[key]; !seen {
			completions = append(completions, key+"=")
		}
	}
	return completions
}

// Aliases returns command aliases
func (f *FilterCommand) Aliases() []string {
	return []string{"find", "search"}
}
