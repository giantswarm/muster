package testing

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/giantswarm/muster/v5/internal/testing/fixtures/scale"
)

// fixtureCatalogue maps the names pre_configuration.fixture may carry to the
// pre_configuration fragment each fixture renders. A fixture is a committed,
// generated, installation-shaped set of definitions a scenario boots its
// instance from; the scenario adds or overrides on top of it.
var fixtureCatalogue = map[string][]byte{
	scale.Name: scale.PreConfigurationYAML,
}

// fixtureNames lists the fixtures a scenario can name, for error messages.
func fixtureNames() []string {
	names := make([]string, 0, len(fixtureCatalogue))
	for name := range fixtureCatalogue {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// applyFixture merges the named fixture's fragment into the scenario's
// pre-configuration, at load time, so everything after the loader sees one
// pre-configuration. The fixture's lists come first and the scenario's are
// appended; a scenario entry that names a fixture server, workflow or mock
// authorization server overlays it instead (config keys merged shallowly for
// servers, the entry replaced for the others), so a scenario can give one
// fixture backend a connect_delay without repeating it. Scalars and blocks
// the fixture does not set -- storage, mode, intervals, main_config -- are
// the scenario's alone; the broker is the scenario's when it declares one.
func applyFixture(pre *MusterPreConfiguration) error {
	if pre == nil || pre.Fixture == "" {
		return nil
	}
	data, ok := fixtureCatalogue[strings.ToLower(pre.Fixture)]
	if !ok {
		return fmt.Errorf("pre_configuration.fixture %q is unknown; known fixtures: %s", pre.Fixture, strings.Join(fixtureNames(), ", "))
	}
	var base MusterPreConfiguration
	if err := yaml.Unmarshal(data, &base); err != nil {
		return fmt.Errorf("fixture %s does not decode: %w", pre.Fixture, err)
	}

	pre.MCPServers = mergeMCPServers(base.MCPServers, pre.MCPServers)
	pre.Workflows = mergeWorkflows(base.Workflows, pre.Workflows)
	pre.MockOAuthServers = mergeMockOAuthServers(base.MockOAuthServers, pre.MockOAuthServers)
	pre.Services = append(base.Services, pre.Services...)
	if pre.MusterBroker == nil {
		pre.MusterBroker = base.MusterBroker
	}
	return nil
}

// mergeMCPServers appends the scenario's servers to the fixture's; a scenario
// server named like a fixture server overlays that server's config keys.
func mergeMCPServers(base, overlay []MCPServerConfig) []MCPServerConfig {
	index := make(map[string]int, len(base))
	merged := make([]MCPServerConfig, 0, len(base)+len(overlay))
	for _, s := range base {
		index[s.Name] = len(merged)
		merged = append(merged, s)
	}
	for _, s := range overlay {
		i, exists := index[s.Name]
		if !exists {
			merged = append(merged, s)
			continue
		}
		target := &merged[i]
		if target.Config == nil {
			target.Config = map[string]interface{}{}
		} else {
			copied := make(map[string]interface{}, len(target.Config)+len(s.Config))
			for k, v := range target.Config {
				copied[k] = v
			}
			target.Config = copied
		}
		for k, v := range s.Config {
			target.Config[k] = v
		}
		if len(s.Labels) > 0 {
			target.Labels = s.Labels
		}
	}
	return merged
}

// mergeWorkflows appends the scenario's workflows; a scenario workflow named
// like a fixture workflow replaces it.
func mergeWorkflows(base, overlay []WorkflowConfig) []WorkflowConfig {
	index := make(map[string]int, len(base))
	merged := make([]WorkflowConfig, 0, len(base)+len(overlay))
	for _, w := range base {
		index[w.Name] = len(merged)
		merged = append(merged, w)
	}
	for _, w := range overlay {
		if i, exists := index[w.Name]; exists {
			merged[i] = w
			continue
		}
		merged = append(merged, w)
	}
	return merged
}

// mergeMockOAuthServers appends the scenario's mock authorization servers; a
// scenario server named like a fixture server replaces it.
func mergeMockOAuthServers(base, overlay []MockOAuthServerConfig) []MockOAuthServerConfig {
	index := make(map[string]int, len(base))
	merged := make([]MockOAuthServerConfig, 0, len(base)+len(overlay))
	for _, s := range base {
		index[s.Name] = len(merged)
		merged = append(merged, s)
	}
	for _, s := range overlay {
		if i, exists := index[s.Name]; exists {
			merged[i] = s
			continue
		}
		merged = append(merged, s)
	}
	return merged
}
