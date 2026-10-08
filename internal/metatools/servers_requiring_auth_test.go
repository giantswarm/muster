package metatools

import (
	"encoding/json"
	"testing"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signedOutCatalogue is a session that has signed in to nothing yet: a core
// tool, one listed server and two servers awaiting sign-in, in no particular
// order.
func signedOutCatalogue() *mockMetaToolsHandler {
	return &mockMetaToolsHandler{
		tools: []mcp.Tool{
			tagged("core_auth_login", toolset.ToolOrigin{Kind: toolset.OriginKindCore}),
			tagged("x_k8s_get", toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: "k8s"}),
		},
		serversRequiringAuth: []api.ServerAuthInfo{
			{Name: "slack", Status: "auth_required", AuthTool: "core_auth_login", ToolPrefix: "x_slack_"},
			{Name: "github", Status: "auth_required", AuthTool: "core_auth_login", ToolPrefix: "x_github_"},
		},
	}
}

func filterTools(t *testing.T, p *Provider, args map[string]any) FilterToolsResponse {
	t.Helper()
	result, err := p.ExecuteTool(withHeader("", false), "filter_tools", args)
	require.NoError(t, err)
	return decodeFilterTools(t, result)
}

func decodeFilterTools(t *testing.T, result *api.CallToolResult) FilterToolsResponse {
	t.Helper()
	require.NotNil(t, result)
	require.False(t, result.IsError, "expected success, got %v", result.Content)
	var resp FilterToolsResponse
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(string)), &resp))
	return resp
}

// serverNames lists the servers by name; nil for none, so a table can expect
// the absence of the field.
func serverNames(servers []api.ServerAuthInfo) []string {
	if len(servers) == 0 {
		return nil
	}
	out := make([]string, len(servers))
	for i, s := range servers {
		out[i] = s.Name
	}
	return out
}

// An agent steered from list_tools to filter_tools never learned that a
// Connector exists until the person had signed in to it: the filter found no
// tool and said nothing (#1351).
func TestFilterTools_NamesTheServersASignInWouldUnlock(t *testing.T) {
	defer registerMockHandler(signedOutCatalogue())()
	p := NewProvider()

	resp := filterTools(t, p, map[string]any{"pattern": "*slack*"})
	assert.Equal(t, 0, resp.Total, "nothing listed matches")
	require.Equal(t, []string{"github", "slack"}, serverNames(resp.ServersRequiringAuth),
		"an unanchored pattern could match a tool of either server; sorted by name")
	slack := resp.ServersRequiringAuth[1]
	assert.Equal(t, "auth_required", slack.Status)
	assert.Equal(t, "core_auth_login", slack.AuthTool)
	assert.Equal(t, "x_slack_", slack.ToolPrefix)
}

func TestFilterTools_PatternNarrowsTheServersToThoseItCouldMatch(t *testing.T) {
	defer registerMockHandler(signedOutCatalogue())()
	p := NewProvider()

	for _, tc := range []struct {
		name string
		args map[string]any
		want []string
	}{
		{"no filter", nil, []string{"github", "slack"}},
		{"query only", map[string]any{"query": "post a message to a channel"}, []string{"github", "slack"}},
		{"description filter only", map[string]any{"description_filter": "channel"}, []string{"github", "slack"}},
		{"prefix pattern", map[string]any{"pattern": "x_slack_*"}, []string{"slack"}},
		{"prefix pattern, other case", map[string]any{"pattern": "X_SLACK_*"}, []string{"slack"}},
		{"prefix pattern, case-sensitive", map[string]any{"pattern": "X_SLACK_*", "case_sensitive": true}, nil},
		{"shared head", map[string]any{"pattern": "x_*"}, []string{"github", "slack"}},
		{"one name under a prefix", map[string]any{"pattern": "x_slack_search"}, []string{"slack"}},
		{"one name under no prefix", map[string]any{"pattern": "x_sl"}, nil},
		{"core tools", map[string]any{"pattern": "core_*"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := filterTools(t, p, tc.args)
			assert.Equal(t, tc.want, serverNames(resp.ServersRequiringAuth))
		})
	}
}

func TestFilterTools_ServersRequiringAuthIsNotNarrowedByTheToolset(t *testing.T) {
	defer registerMockHandler(signedOutCatalogue())()
	p := NewProvider()

	result, err := p.ExecuteTool(withHeader("server:k8s", true), "filter_tools", map[string]any{"pattern": "*slack*"})
	require.NoError(t, err)
	_, present := decode(t, result)["toolset_requiring_auth"]
	assert.False(t, present, "the toolset selects no tool of a pending server")
	assert.Equal(t, []string{"github", "slack"}, serverNames(decodeFilterTools(t, result).ServersRequiringAuth),
		"servers_requiring_auth tells which sign-in would make more of the toolset resolve")
}

func TestFilterTools_EmptyCatalogueWithAPendingServerIsStructured(t *testing.T) {
	m := signedOutCatalogue()
	m.tools = nil
	defer registerMockHandler(m)()
	p := NewProvider()

	resp := filterTools(t, p, map[string]any{"pattern": "x_slack_*"})
	assert.Empty(t, resp.Tools)
	assert.Equal(t, 0, resp.TotalTools)
	assert.Equal(t, []string{"slack"}, serverNames(resp.ServersRequiringAuth))

	// A pattern no sign-in could serve keeps the legacy text.
	result, err := p.ExecuteTool(withHeader("", false), "filter_tools", map[string]any{"pattern": "core_*"})
	require.NoError(t, err)
	assert.Equal(t, "No tools available to filter", result.Content[0].(string))
}

func TestListCoreTools_NamesNoServerRequiringAuth(t *testing.T) {
	defer registerMockHandler(signedOutCatalogue())()
	p := NewProvider()

	result, err := p.ExecuteTool(withHeader("", false), "list_core_tools", nil)
	require.NoError(t, err)
	_, present := decode(t, result)["servers_requiring_auth"]
	assert.False(t, present, "a core tool is never a pending server's")
}

func TestListTools_ServersRequiringAuthIsSortedByName(t *testing.T) {
	defer registerMockHandler(signedOutCatalogue())()
	p := NewProvider()

	resp := listTools(t, p, nil)
	assert.Equal(t, []string{"github", "slack"}, serverNames(resp.ServersRequiringAuth))
}

func TestPatternMayMatchPrefix(t *testing.T) {
	for _, tc := range []struct {
		pattern, prefix string
		caseSensitive   bool
		want            bool
	}{
		{"", "x_slack_", false, true},
		{"*", "x_slack_", false, true},
		{"*slack*", "x_slack_", false, true},
		{"*slack*", "x_github_", false, true}, // the unknown tail may hold "slack"
		{"x_*", "x_slack_", false, true},
		{"x_slack_*", "x_slack_", false, true},
		{"x_slack_sea*", "x_slack_", false, true},
		{"x_slack_search", "x_slack_", false, true},
		{"x_slack_?", "x_slack_", false, true},
		{"x_sl?ck_*", "x_slack_", false, true},  // ? counts as a star,
		{"x_[sg]*", "x_slack_", false, true},    // so does a class,
		{`x\_slack_*`, "x_slack_", false, true}, // and an escape
		{"X_SLACK_*", "x_slack_", false, true},
		{"X_SLACK_*", "x_slack_", true, false},
		{"x_github_*", "x_slack_", false, false},
		{"x_gith*", "x_slack_", false, false},
		{"x_sl", "x_slack_", false, false},
		{"core_*", "x_slack_", false, false},
		{"core_*", "", false, true}, // a server without a prefix could own any name
	} {
		assert.Equal(t, tc.want, patternMayMatchPrefix(tc.pattern, tc.prefix, tc.caseSensitive),
			"pattern %q, prefix %q, case-sensitive %t", tc.pattern, tc.prefix, tc.caseSensitive)
	}
}
