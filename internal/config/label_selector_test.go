package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/labels"
)

func TestLabelSelectorFromYAML(t *testing.T) {
	var cfg MusterConfig
	require.NoError(t, yaml.Unmarshal([]byte(`
mcpServerNamespaceSelector:
  matchLabels:
    agent-platform.giantswarm.io/organization: acme
mcpServerSelector:
  matchExpressions:
    - key: tier
      operator: In
      values: [gold, silver]
`), &cfg))

	ns, err := cfg.MCPServerNamespaceSelector.Selector()
	require.NoError(t, err)
	assert.True(t, ns.Matches(labels.Set{"agent-platform.giantswarm.io/organization": "acme"}))
	assert.False(t, ns.Matches(labels.Set{"agent-platform.giantswarm.io/organization": "other"}))

	servers, err := cfg.MCPServerSelector.Selector()
	require.NoError(t, err)
	assert.True(t, servers.Matches(labels.Set{"tier": "gold"}))
	assert.False(t, servers.Matches(labels.Set{}))
}

// TestLabelSelectorUnsetAndEmpty: an unset selector is nil, `{}` matches
// every label set.
func TestLabelSelectorUnsetAndEmpty(t *testing.T) {
	var unset MusterConfig
	require.NoError(t, yaml.Unmarshal([]byte(`namespace: muster`), &unset))
	selector, err := unset.MCPServerNamespaceSelector.Selector()
	require.NoError(t, err)
	assert.Nil(t, selector)

	var empty MusterConfig
	require.NoError(t, yaml.Unmarshal([]byte(`mcpServerNamespaceSelector: {}`), &empty))
	selector, err = empty.MCPServerNamespaceSelector.Selector()
	require.NoError(t, err)
	require.NotNil(t, selector)
	assert.True(t, selector.Matches(labels.Set{"anything": "x"}))
	assert.True(t, selector.Matches(labels.Set{}))
}

func TestLabelSelectorRefusesABadOperator(t *testing.T) {
	_, err := (&LabelSelector{MatchExpressions: []LabelSelectorRequirement{{Key: "tier", Operator: "Near"}}}).Selector()
	require.ErrorContains(t, err, "invalid label selector")
}
