package aggregator

import (
	"log/slog"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// coreCatalogueMaxAge is the age past which a read of the core catalogue
// refreshes it in the background. Every path that changes a workflow
// definition invalidates the catalogue explicitly (the workflow tools through
// UpdateCapabilities, a Workflow resource applied or deleted outside muster
// through the workflow reconciler); the age is the backstop for a change none
// of them saw, so the read that notices it is served the catalogue it has and
// the rebuilt one is swapped in when it lands (#1231). The integration test
// harness lowers it to seconds.
var coreCatalogueMaxAge = config.DurationFromEnv("MUSTER_CORE_CATALOGUE_MAX_AGE", 5*time.Minute)

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
//
// A rebuild runs in its own goroutine, one at a time: readers that have a
// catalogue keep reading it while the rebuild runs and see the new one once
// it lands; readers that have none (the first read of the process, or a
// definition changed) wait for the rebuild in flight instead of starting
// their own.
type coreCatalogue struct {
	mu      sync.Mutex
	tools   []mcp.Tool
	steps   map[string][]string
	builtAt time.Time
	valid   bool
	// gen counts invalidations. A rebuild that started before the latest
	// one carries a stale result and builds again before it lands.
	gen uint64
	// done is non-nil while a rebuild is in flight and closed when its
	// result has landed.
	done   chan struct{}
	now    func() time.Time
	maxAge time.Duration
}

// coreCatalogueBuild produces the core tools and the step tools of every
// workflow execution tool, keyed by exposed name.
type coreCatalogueBuild func() ([]mcp.Tool, map[string][]string)

func newCoreCatalogue() *coreCatalogue {
	return &coreCatalogue{now: clock.Now, maxAge: coreCatalogueMaxAge}
}

// get returns the core tools (a fresh slice; the elements share their schema
// and _meta with the catalogue, which nothing mutates in place) and the step
// tools of every workflow execution tool.
//
// Without a valid catalogue -- before the first build, or after a definition
// change invalidated it -- get waits for one rebuild through build, shared by
// every concurrent caller. With a valid catalogue older than maxAge, get
// starts that rebuild in the background (unless one is in flight) and returns
// the catalogue it has.
func (c *coreCatalogue) get(build coreCatalogueBuild) ([]mcp.Tool, map[string][]string) {
	if c == nil { // an aggregator assembled without one (tests) builds every time
		return build()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for !c.valid {
		trigger := "invalidated"
		if c.builtAt.IsZero() {
			trigger = "initial"
		}
		done := c.startRebuild(build, trigger)
		c.mu.Unlock()
		<-done
		c.mu.Lock()
	}
	if c.done == nil && c.now().Sub(c.builtAt) > c.maxAge {
		c.startRebuild(build, "age")
	}
	return append([]mcp.Tool(nil), c.tools...), c.steps
}

// invalidate makes the next get wait for a rebuild; a rebuild in flight
// builds again before it lands, so no read is served a catalogue built before
// the change.
func (c *coreCatalogue) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.valid = false
	c.gen++
}

// startRebuild returns the channel closed when the rebuild in flight lands,
// starting one when there is none. The caller holds c.mu.
func (c *coreCatalogue) startRebuild(build coreCatalogueBuild, trigger string) <-chan struct{} {
	if c.done == nil {
		c.done = make(chan struct{})
		go c.rebuild(build, trigger, c.done)
	}
	return c.done
}

// rebuild runs build outside the lock and swaps the result in. A result
// built before the latest invalidation is stale -- a definition changed while
// build ran -- and is built again; done is closed once a current result has
// landed.
func (c *coreCatalogue) rebuild(build coreCatalogueBuild, trigger string, done chan struct{}) {
	for {
		c.mu.Lock()
		gen := c.gen
		c.mu.Unlock()

		start := c.now()
		tools, steps := build()
		duration := c.now().Sub(start)

		c.mu.Lock()
		if gen != c.gen {
			c.mu.Unlock()
			logging.DebugWithAttrs("Aggregator", "Core catalogue rebuilt while a definition changed, rebuilding",
				slog.String("trigger", trigger),
				slog.Float64("duration_s", duration.Seconds()))
			trigger = "invalidated"
			continue
		}
		c.tools, c.steps = tools, steps
		c.builtAt = c.now()
		c.valid = true
		c.done = nil
		close(done)
		c.mu.Unlock()

		logging.DebugWithAttrs("Aggregator", "Core catalogue rebuilt",
			slog.String("trigger", trigger),
			slog.Int("tools", len(tools)),
			slog.Float64("duration_s", duration.Seconds()))
		return
	}
}
