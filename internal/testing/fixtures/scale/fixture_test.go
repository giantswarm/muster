package scale

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The committed YAML is the generator's rendering: regenerate with
// `go generate ./internal/testing/fixtures/scale/` after changing the shape.
func TestFixtureFilesAreCurrent(t *testing.T) {
	f := Get()
	pre, err := RenderPreConfiguration(f)
	require.NoError(t, err)
	if !bytes.Equal(pre, PreConfigurationYAML) {
		t.Fatalf("pre_configuration.yaml is stale: run `go generate ./internal/testing/fixtures/scale/` (rendered %d bytes, committed %d bytes)", len(pre), len(PreConfigurationYAML))
	}
	sessions, err := RenderSessions(f)
	require.NoError(t, err)
	if !bytes.Equal(sessions, SessionsYAML) {
		t.Fatalf("sessions.yaml is stale: run `go generate ./internal/testing/fixtures/scale/`")
	}
}

// Two generations are the same fixture: the generator is seeded and sorts
// everything it iterates.
func TestGeneratorIsDeterministic(t *testing.T) {
	a, err := RenderPreConfiguration(generate())
	require.NoError(t, err)
	b, err := RenderPreConfiguration(generate())
	require.NoError(t, err)
	assert.True(t, bytes.Equal(a, b), "two renderings of the fixture differ")
}

// The fixture has the installation's shape (giantswarm/muster#1240).
func TestFixtureShape(t *testing.T) {
	shape := Get().Shape()
	assert.Equal(t, 87, shape.Servers)
	assert.Equal(t, 84, shape.SessionAuthServers)
	assert.Equal(t, 5, shape.Families)
	assert.Equal(t, 28, shape.Installations)
	assert.Equal(t, 18, shape.SessionDocuments, "distinct capability documents the sessions reference")
	assert.Equal(t, 21, shape.Documents, "session documents plus the three in-house servers' tool lists")
	assert.Equal(t, 282, shape.Workflows)
	assert.Equal(t, 450, shape.Sessions)

	f := Get()
	names := map[string]struct{}{}
	for _, s := range f.Servers {
		_, dup := names[s.Name]
		assert.False(t, dup, "server %s twice", s.Name)
		names[s.Name] = struct{}{}
		_, ok := f.DocumentByName(s.Document)
		assert.True(t, ok, "server %s offers unknown document %s", s.Name, s.Document)
	}
	workflowNames := map[string]struct{}{}
	for _, w := range f.Workflows {
		_, dup := workflowNames[w.Name]
		assert.False(t, dup, "workflow %s twice", w.Name)
		workflowNames[w.Name] = struct{}{}
		assert.NotEmpty(t, w.Steps, "workflow %s has no steps", w.Name)
	}
}

// A family exposes one name per tool for all its members, so the members'
// documents must agree on every shared tool's description, and the instance
// argument must not be a tool's own property (either makes the harness fall
// back to per-server names).
func TestFamilyToolsAreConsistent(t *testing.T) {
	f := Get()
	descriptions := map[string]string{}
	for _, s := range f.Servers {
		if s.Family == "" {
			continue
		}
		doc, _ := f.DocumentByName(s.Document)
		for _, tool := range doc.Tools {
			key := s.Family + "/" + tool.Name
			if prev, seen := descriptions[key]; seen {
				assert.Equal(t, prev, tool.Description, "family tool %s described differently", key)
			}
			descriptions[key] = tool.Description
			for _, p := range tool.Properties {
				assert.NotEqual(t, s.InstanceArg, p.Name, "tool %s declares the instance argument as a property", key)
			}
		}
	}
}

// Every workflow step names a tool some server of the fixture offers.
func TestWorkflowStepsReferenceFixtureTools(t *testing.T) {
	f := Get()
	exposed := map[string]struct{}{}
	for _, s := range f.Servers {
		doc, _ := f.DocumentByName(s.Document)
		for _, tool := range doc.Tools {
			exposed[ExposedToolName(s, tool.Name)] = struct{}{}
		}
	}
	for _, w := range f.Workflows {
		for _, st := range w.Steps {
			_, ok := exposed[st.Tool]
			assert.True(t, ok, "workflow %s step %s references %s, which no server offers", w.Name, st.ID, st.Tool)
		}
	}
}

// Nothing in the fixture comes from an installation: no real domain, no
// organisation name, no person. The review the acceptance criteria ask for,
// as a test.
func TestFixtureNamesAreInvented(t *testing.T) {
	text := string(PreConfigurationYAML) + string(SessionsYAML)
	for _, forbidden := range []string{"giantswarm", ".io/", ".io\"", ".com", ".net", ".dev", "gazelle", "golem", "glean", "graveler", "@giantswarm"} {
		assert.NotContains(t, text, forbidden, "the fixture must not carry %q", forbidden)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "@") {
			assert.Contains(t, line, "@people.fixture.invalid", "an address that is not a numbered fixture person: %s", line)
		}
	}
	for _, s := range Get().Servers {
		assert.True(t, strings.HasSuffix(s.URL(), ".mcp.fixture.invalid/mcp"), "hostname %s", s.URL())
	}
}

// The rendering decodes into the harness's pre_configuration shape with the
// aliases resolved: every server carries its document's tools.
func TestRenderingDecodesAsPreConfiguration(t *testing.T) {
	var pre struct {
		MockOAuthServers []map[string]any `yaml:"mock_oauth_servers"`
		MusterBroker     map[string]any   `yaml:"muster_broker"`
		MCPServers       []struct {
			Name   string         `yaml:"name"`
			Config map[string]any `yaml:"config"`
		} `yaml:"mcp_servers"`
		Workflows []struct {
			Name   string         `yaml:"name"`
			Config map[string]any `yaml:"config"`
		} `yaml:"workflows"`
	}
	require.NoError(t, yaml.Unmarshal(PreConfigurationYAML, &pre))
	assert.Len(t, pre.MockOAuthServers, 2)
	assert.NotNil(t, pre.MusterBroker["trusted_issuers"])
	require.Len(t, pre.MCPServers, 87)
	require.Len(t, pre.Workflows, 282)

	f := Get()
	for i, s := range pre.MCPServers {
		tools, ok := s.Config["tools"].([]any)
		require.True(t, ok, "server %s: tools did not decode as a list", s.Name)
		doc, _ := f.DocumentByName(f.Servers[i].Document)
		assert.Len(t, tools, len(doc.Tools), "server %s", s.Name)
		if f.Servers[i].SessionAuth {
			oauth, _ := s.Config["oauth"].(map[string]any)
			assert.Equal(t, true, oauth["forward_token"], "server %s", s.Name)
			assert.Equal(t, f.FleetIssuer, oauth["trust_issuer_ref"], "server %s", s.Name)
		} else {
			assert.Nil(t, s.Config["oauth"], "in-house server %s must not require authentication", s.Name)
		}
	}
}
