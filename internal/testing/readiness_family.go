package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/metatools"
	"github.com/giantswarm/muster/v5/internal/toolset"
)

// findMissingFamilyMembers reports, for every family-grouped tool in
// expected (exposed name -> the members the pre-configuration contributes it
// from), the members its describe_tool trailer does not list yet. A family
// tool is exposed as soon as its first member registers, so its name alone
// cannot gate readiness: the trailer grows as the later members register.
// The result is one "<tool> on [<members>]" entry per incomplete tool, in
// tool order; an error means describe_tool could not be called.
func findMissingFamilyMembers(ctx context.Context, client MCPTestClient, expected map[string][]string) ([]string, error) {
	tools := make([]string, 0, len(expected))
	for tool := range expected {
		tools = append(tools, tool)
	}
	sort.Strings(tools)

	var missing []string
	for _, tool := range tools {
		registered, err := describeFamilyMembers(ctx, client, tool)
		if err != nil {
			return nil, err
		}
		var absent []string
		for _, member := range expected[tool] {
			if !slices.Contains(registered, member) && !slices.Contains(absent, member) {
				absent = append(absent, member)
			}
		}
		if len(absent) > 0 {
			missing = append(missing, fmt.Sprintf("%s on %v", tool, absent))
		}
	}
	return missing, nil
}

// describeFamilyMembers returns the members the describe_tool trailer of
// tool names; none when the tool is not listed (yet).
func describeFamilyMembers(ctx context.Context, client MCPTestClient, tool string) ([]string, error) {
	result, err := client.CallToolDirect(ctx, metatools.ToolDescribeTool, map[string]interface{}{"name": tool})
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", metatools.ToolDescribeTool, tool, err)
	}
	if result == nil || result.IsError || len(result.Content) == 0 {
		return nil, nil
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		return nil, nil
	}
	var detail struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(text.Text), &detail); err != nil {
		return nil, fmt.Errorf("%s %s: decode: %w", metatools.ToolDescribeTool, tool, err)
	}
	return toolset.FamilyMembers(detail.Description), nil
}
