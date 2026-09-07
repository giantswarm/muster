package toolset

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHeader_TrimsAndSplits(t *testing.T) {
	ts, err := ParseHeader(" preset:read-only , workflow:incident-triage ,tool:x_k8s_get")
	require.NoError(t, err)
	assert.Equal(t, []string{"preset:read-only", "workflow:incident-triage", "tool:x_k8s_get"}, ts.Raw)
	require.Len(t, ts.Selectors, 3)
	assert.Equal(t, Selector{Kind: KindPreset, Name: "read-only"}, ts.Selectors[0])
	assert.Equal(t, Selector{Kind: KindWorkflow, Name: "incident-triage"}, ts.Selectors[1])
	assert.Equal(t, Selector{Kind: KindTool, Name: "x_k8s_get"}, ts.Selectors[2])
	assert.Equal(t, "[preset:read-only,workflow:incident-triage,tool:x_k8s_get]", ts.String())
	assert.Equal(t, []string{"read-only"}, ts.Presets())
}

func TestParseHeader_Errors(t *testing.T) {
	tooMany := make([]string, MaxInlineSelectors+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("tool:t%d", i)
	}

	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"empty", "", `toolset [] is empty; use "preset:none"`},
		{"whitespace only", "   ", `toolset [] is empty`},
		{"reserved", "preset:read-only,toolset:shared", `toolset [preset:read-only,toolset:shared] selector "toolset:shared" is reserved for shared toolsets`},
		{"inline label", "label:a=b", `selector "label:a=b" is allowed inside presets only`},
		{"unknown kind", "pattern:core_*", `selector "pattern:core_*" is malformed`},
		{"no colon", "read-only", `selector "read-only" is malformed`},
		{"empty name", "preset:", `selector "preset:" is malformed`},
		{"trailing comma", "preset:none,", `selector "" is malformed`},
		{"whitespace in name", "tool:x k8s", `selector "tool:x k8s" is malformed`},
		{"too many", strings.Join(tooMany, ","), "has 33 selectors, more than the 32 allowed inline; define a preset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseHeader(tc.header)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestParseInline_CapIsInclusive(t *testing.T) {
	list := make([]string, MaxInlineSelectors)
	for i := range list {
		list[i] = fmt.Sprintf("tool:t%d", i)
	}
	_, err := ParseInline(list)
	require.NoError(t, err)
}

func TestParseInline_NamesAreCaseSensitiveAndMayContainColons(t *testing.T) {
	ts, err := ParseInline([]string{"tool:X_K8s_Get", "server:ns:name"})
	require.NoError(t, err)
	assert.Equal(t, "X_K8s_Get", ts.Selectors[0].Name)
	assert.Equal(t, "ns:name", ts.Selectors[1].Name)
}
