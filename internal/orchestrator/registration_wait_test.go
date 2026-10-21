package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// registeredAggregator is an aggregator whose registry lists tools for the
// servers in withTools, and whose catalogue exposes them under exposedTools.
type registeredAggregator struct {
	stubAggregator
	withTools    map[string]bool
	exposedTools []string
}

func (r *registeredAggregator) GetAvailableTools() []string     { return r.exposedTools }
func (r *registeredAggregator) ServerHasTools(name string) bool { return r.withTools[name] }

// TestWaitForMCPServerRegistration_ExposedPrefixNotDerivedFromKey is the
// regression test for #1621: the wait looked for a tool named
// "x_<key>_…", which a server outside muster's namespace (key
// "<namespace>/<name>") or with its own toolPrefix never exposes. Each such
// start waited the full timeout and logged a false "registration wait failed".
func TestWaitForMCPServerRegistration_ExposedPrefixNotDerivedFromKey(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		exposedTools []string
	}{
		{"tenant server", "org-a/deepwiki", []string{"x_org-a_deepwiki_ask_question"}},
		{"tenant server with its own toolPrefix", "org-b/context7-server", []string{"x_org-b_context7_resolve"}},
		{"server with its own toolPrefix", "mcp-deepwiki", []string{"x_deepwiki_ask_question"}},
		{"server with the default prefix", "github", []string{"x_github_create_issue"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			swapAggregatorHandler(t, &registeredAggregator{
				withTools:    map[string]bool{tt.key: true},
				exposedTools: tt.exposedTools,
			})

			o := &Orchestrator{}
			require.NoError(t, o.waitForMCPServerRegistration(tt.key))
		})
	}
}
