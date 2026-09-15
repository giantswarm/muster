package aggregator

import (
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

// The core catalogue is built once and served until it is invalidated or
// older than coreCatalogueMaxAge; every meta-tool call used to rebuild it,
// which on an installation is a list of every workflow definition (#1225).
func TestCoreCatalogue_BuildsOnceUntilInvalidated(t *testing.T) {
	now := time.Now()
	c := newCoreCatalogue()
	c.now = func() time.Time { return now }

	builds := 0
	build := func() ([]mcp.Tool, map[string][]string) {
		builds++
		return []mcp.Tool{{Name: "workflow_a"}}, map[string][]string{"workflow_a": {"x_k8s_get_pods"}}
	}

	tools, steps := c.get(build)
	_, _ = c.get(build)
	assert.Equal(t, 1, builds, "the second read is served from the catalogue")
	assert.Equal(t, "workflow_a", tools[0].Name)
	assert.Equal(t, []string{"x_k8s_get_pods"}, steps["workflow_a"])

	// Callers get their own slice: appending to it does not grow the catalogue.
	tools = append(tools, mcp.Tool{Name: "extra"})
	again, _ := c.get(build)
	assert.Len(t, again, 1)
	assert.Len(t, tools, 2)

	c.invalidate()
	_, _ = c.get(build)
	assert.Equal(t, 2, builds, "an invalidated catalogue is rebuilt on the next read")

	now = now.Add(coreCatalogueMaxAge + time.Second)
	_, _ = c.get(build)
	assert.Equal(t, 3, builds, "a catalogue past its age is rebuilt")
	_, _ = c.get(build)
	assert.Equal(t, 3, builds)
}

func TestCoreCatalogue_NilBuildsEveryTime(t *testing.T) {
	var c *coreCatalogue
	builds := 0
	build := func() ([]mcp.Tool, map[string][]string) { builds++; return nil, nil }
	_, _ = c.get(build)
	_, _ = c.get(build)
	c.invalidate()
	assert.Equal(t, 2, builds)
}
