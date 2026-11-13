package metatools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/toolset"
)

func TestToolsetLimitsAcrossSources(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	provider := NewProvider()
	for _, count := range []int{33, 128, 129} {
		selectors := []string{"tool:x_k8s_get"}
		for index := 1; index < count; index++ {
			selectors = append(selectors, fmt.Sprintf("tool:x_missing_%d", index))
		}
		for _, source := range []string{"header", "argument", "binding"} {
			t.Run(fmt.Sprintf("%s/%d", source, count), func(t *testing.T) {
				ctx := t.Context()
				arguments := map[string]any{}
				switch source {
				case "header":
					ctx = toolset.ContextWithSource(ctx, toolset.Source{Present: true, Raw: strings.Join(selectors, ",")})
				case "argument":
					arguments["toolset"] = selectors
				case "binding":
					ctx = toolset.ContextWithBound(ctx, selectors)
				}
				result, err := provider.ExecuteTool(ctx, "filter_tools", arguments)
				require.NoError(t, err)
				if count > 128 {
					require.Contains(t, errorText(t, result), "129 selectors")
				} else {
					require.Equal(t, []string{"x_k8s_get"}, toolNames(t, decode(t, result)))
				}
			})
		}
	}
	for _, source := range []string{"header", "argument", "binding"} {
		t.Run(source+"/byte-limit", func(t *testing.T) {
			ctx := t.Context()
			selectors := []string{"tool:" + strings.Repeat("x", 8188)}
			arguments := map[string]any{}
			switch source {
			case "header":
				ctx = toolset.ContextWithSource(ctx, toolset.Source{Present: true, Raw: selectors[0]})
			case "argument":
				arguments["toolset"] = selectors
			case "binding":
				ctx = toolset.ContextWithBound(ctx, selectors)
			}
			result, err := provider.ExecuteTool(ctx, "filter_tools", arguments)
			require.NoError(t, err)
			require.Contains(t, errorText(t, result), "encodes to 8193 bytes")
		})
	}
}
