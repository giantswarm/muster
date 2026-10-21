package metatools

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// TestToolDescriptionBudgets holds the meta-tools, which every client lists in
// full on connect, to the budget of giantswarm/giantswarm#38592. Once muster
// imports mcp-toolkit's tooldesc (#1545), this becomes tooldesc.CheckServer.
func TestToolDescriptionBudgets(t *testing.T) {
	prefixes := map[string]string{}
	for _, tool := range NewProvider().GetTools() {
		d := tool.Description
		first, _, _ := strings.Cut(d, "\n")
		n := utf8.RuneCountInString(strings.TrimSpace(first))
		assert.True(t, n > 0 && n <= summaryMaxLen, "%s: first line is %d runes, want 1 to %d", tool.Name, n, summaryMaxLen)
		assert.LessOrEqual(t, utf8.RuneCountInString(d), descriptionMax, "%s: description over budget", tool.Name)
		p := strings.ToLower(d)
		if r := []rune(p); len(r) > prefixMax {
			p = string(r[:prefixMax])
		}
		if other, ok := prefixes[p]; ok {
			t.Errorf("%s starts like %s: %q", tool.Name, other, p)
		}
		prefixes[p] = tool.Name
		for _, arg := range tool.Args {
			assert.LessOrEqual(t, utf8.RuneCountInString(arg.Description), paramMax, "%s.%s: description over budget", tool.Name, arg.Name)
		}
	}
}

// TestReadOnlyMetaToolsSayItInFull: a read-only meta-tool reports every hint,
// so no client falls back to the spec's defaults (destructive, not
// idempotent); call_tool, which runs whatever it is given, reports the opposite.
func TestReadOnlyMetaToolsSayItInFull(t *testing.T) {
	for _, tool := range NewProvider().GetTools() {
		a := tool.Annotations
		require.NotNil(t, a, "%s declares no annotations", tool.Name)
		require.NotNil(t, a.ReadOnlyHint, tool.Name)
		require.NotNil(t, a.DestructiveHint, tool.Name)
		require.NotNil(t, a.IdempotentHint, tool.Name)
		if tool.Name == ToolCallTool {
			assert.False(t, *a.ReadOnlyHint, "call_tool runs any tool, writes included")
			assert.True(t, *a.DestructiveHint, "call_tool runs any tool, destructive ones included")
			continue
		}
		assert.True(t, *a.ReadOnlyHint, "%s only reads", tool.Name)
		assert.False(t, *a.DestructiveHint, tool.Name)
		assert.True(t, *a.IdempotentHint, tool.Name)
	}
}

// TestToolsDocIsCurrent keeps docs/reference/meta-tools.md, the meta-tools as
// a client lists them, equal to the provider. TOOLS_DOC_UPDATE=1 rewrites it
// (make tools-doc).
func TestToolsDocIsCurrent(t *testing.T) {
	const path = "../../docs/reference/meta-tools.md"
	got := renderToolsDoc(NewProvider().GetTools())
	if os.Getenv("TOOLS_DOC_UPDATE") != "" {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), got, "%s is stale: run make tools-doc", path)
}

func renderToolsDoc(tools []api.ToolMetadata) string {
	var b strings.Builder
	b.WriteString("# Meta-tools\n\nGenerated from the provider (`make tools-doc`): every meta-tool's description, annotations and arguments as an MCP client lists them on connect. See [MCP tools](mcp-tools.md) for what they answer.\n")
	for _, tool := range tools {
		fmt.Fprintf(&b, "\n## `%s`\n\n%s\n\n```text\n%s\n```\n", tool.Name, hints(tool.Annotations), tool.Description)
		if len(tool.Args) == 0 {
			continue
		}
		b.WriteString("\n| Argument | Type | Required | Description |\n|---|---|---|---|\n")
		for _, arg := range tool.Args {
			fmt.Fprintf(&b, "| `%s` | %s | %t | %s |\n", arg.Name, arg.Type, arg.Required, tableCell.Replace(arg.Description))
		}
	}
	return b.String()
}

// tableCell escapes what a Markdown table cell would otherwise read as markup.
var tableCell = strings.NewReplacer("|", `\|`, "<", "&lt;", ">", "&gt;")

func hints(a *api.ToolAnnotations) string {
	if a == nil {
		return "No annotations."
	}
	var parts []string
	for _, h := range []struct {
		name string
		v    *bool
	}{{"readOnly", a.ReadOnlyHint}, {"destructive", a.DestructiveHint}, {"idempotent", a.IdempotentHint}, {"openWorld", a.OpenWorldHint}} {
		if h.v != nil {
			parts = append(parts, fmt.Sprintf("%s: %t", h.name, *h.v))
		}
	}
	return "Annotations: " + strings.Join(parts, ", ") + "."
}
