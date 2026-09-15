package aggregator

import (
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// coreCatalogueMaxAge bounds how long the core catalogue is served without
// being rebuilt from the providers. Every path that changes a workflow
// definition invalidates it explicitly (the workflow tools through
// UpdateCapabilities, a Workflow resource applied or deleted outside muster
// through the workflow reconciler); the age is the backstop for a change
// none of them saw.
const coreCatalogueMaxAge = 5 * time.Minute

// coreCatalogue is the aggregator's own contribution to every session's
// catalogue -- the core_* tools and one workflow_<name> tool per workflow --
// as exposed tools, built from the tool providers once and kept until a
// provider's inventory changes.
//
// The workflow provider builds its tools from the definition source, which
// on an installation is the API server: a list of every Workflow resource.
// Rebuilding that for every meta-tool call made each call read all
// definitions, twice, per call (#1225). Nothing in the core catalogue depends
// on the calling session; the one session-dependent property, a workflow's
// derived read-only hint, is computed per request from the step tools kept
// alongside (deriveWorkflowReadOnlyHints).
type coreCatalogue struct {
	mu      sync.Mutex
	tools   []mcp.Tool
	steps   map[string][]string
	builtAt time.Time
	valid   bool
	now     func() time.Time
}

func newCoreCatalogue() *coreCatalogue {
	return &coreCatalogue{now: time.Now}
}

// get returns the core tools (a fresh slice; the elements share their schema
// and _meta with the catalogue, which nothing mutates in place) and the step
// tools of every workflow execution tool, building both through build when
// the catalogue is invalid or older than coreCatalogueMaxAge. build runs
// under the catalogue's lock, so concurrent callers wait for one build
// instead of each running their own.
func (c *coreCatalogue) get(build func() ([]mcp.Tool, map[string][]string)) ([]mcp.Tool, map[string][]string) {
	if c == nil { // an aggregator assembled without one (tests) builds every time
		return build()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid || c.now().Sub(c.builtAt) > coreCatalogueMaxAge {
		c.tools, c.steps = build()
		c.builtAt = c.now()
		c.valid = true
	}
	return append([]mcp.Tool(nil), c.tools...), c.steps
}

// invalidate makes the next get rebuild the catalogue.
func (c *coreCatalogue) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.valid = false
}
