package toolset

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaySelect_UnlistedTool(t *testing.T) {
	r, err := NewRegistry(PresetsConfig{
		"github":         {Include: []Rule{{Server: "gh"}}},
		"github-no-del":  {Include: []Rule{{Server: "gh"}}, Exclude: []Rule{{Pattern: "x_gh_delete_*"}}},
		"github-ro":      {Include: []Rule{{Server: "gh"}}, Exclude: []Rule{{ReadOnly: boolPtr(true)}}},
		"by-pattern":     {Include: []Rule{{Pattern: "x_gh_*"}}},
		"by-tool":        {Include: []Rule{{Tool: "x_gh_issues"}}},
		"labelled":       {Include: []Rule{{Label: "team=bumblebee"}}},
		"composed":       {Include: []Rule{{Preset: "github"}}},
		"composed-minus": {Include: []Rule{{Preset: "github"}}, Exclude: []Rule{{Server: "gh"}}},
		"kube":           {Include: []Rule{{Server: "kubernetes"}}},
	})
	require.NoError(t, err)
	labels := func(server string) map[string]string {
		if server == "gh" {
			return map[string]string{"team": "bumblebee"}
		}
		return nil
	}
	issues := Unlisted{Server: "gh", Prefix: "x_gh_", Name: "x_gh_issues"}
	deleteRepo := Unlisted{Server: "gh", Prefix: "x_gh_", Name: "x_gh_delete_repo"}
	member := Unlisted{Server: "kube-b", Family: "kubernetes", Prefix: "x_kubernetes_", Name: "x_kubernetes_get"}

	for _, tc := range []struct {
		toolset string
		tool    Unlisted
		want    bool
	}{
		{"tool:x_gh_issues", issues, true},
		{"tool:x_gh_pulls", issues, false},
		{"server:gh", issues, true},
		{"server:k8s", issues, false},
		{"workflow:gh", issues, false},
		{"preset:github", issues, true},
		{"preset:github-no-del", issues, true},
		{"preset:github-no-del", deleteRepo, false},
		{"preset:github-ro", issues, true},
		{"preset:read-only", issues, true},
		{"preset:full", issues, true},
		{"preset:none", issues, false},
		{"preset:by-pattern", issues, true},
		{"preset:by-tool", issues, true},
		{"preset:by-tool", deleteRepo, false},
		{"preset:labelled", issues, true},
		{"preset:composed", issues, true},
		{"preset:composed-minus", issues, false},
		{"server:kube-b", member, true},
		{"server:kubernetes", member, true},
		{"preset:kube", member, true},
		{"server:kube-a", member, false},
		{"server:k8s,tool:x_gh_issues", issues, true},
	} {
		ts, err := ParseHeader(tc.toolset)
		require.NoError(t, err)
		assert.Equal(t, tc.want, r.MaySelect(ts, tc.tool, labels), "%s selects %s", tc.toolset, tc.tool.Name)
	}
}

func TestMaySelect_AnyToolOfTheServer(t *testing.T) {
	r, err := NewRegistry(PresetsConfig{
		"by-pattern":   {Include: []Rule{{Pattern: "x_gh_*"}}},
		"other":        {Include: []Rule{{Pattern: "x_k8s_*"}}},
		"wide-minus":   {Include: []Rule{{Pattern: "x_*"}}, Exclude: []Rule{{Pattern: "x_g*"}}},
		"narrow-minus": {Include: []Rule{{Pattern: "x_*"}}, Exclude: []Rule{{Pattern: "x_gh_delete_*"}}},
		"exact":        {Include: []Rule{{Pattern: "x_gh_issues"}}},
		"shorter":      {Include: []Rule{{Pattern: "x_g"}}},
	})
	require.NoError(t, err)
	gh := Unlisted{Server: "gh", Prefix: "x_gh_", Owns: func(name string) bool { return strings.HasPrefix(name, "x_gh_") }}

	for _, tc := range []struct {
		toolset string
		want    bool
	}{
		{"tool:x_gh_issues", true},
		{"tool:x_k8s_get", false},
		{"server:gh", true},
		{"preset:read-only", true},
		{"preset:by-pattern", true},
		{"preset:other", false},
		{"preset:wide-minus", false},
		{"preset:narrow-minus", true},
		{"preset:exact", true},
		{"preset:shorter", false},
	} {
		ts, err := ParseHeader(tc.toolset)
		require.NoError(t, err)
		assert.Equal(t, tc.want, r.MaySelect(ts, gh, nil), tc.toolset)
	}
}
