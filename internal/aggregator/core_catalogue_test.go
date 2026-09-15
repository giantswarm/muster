package aggregator

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awaitRebuild blocks until no rebuild is in flight. The result of a rebuild
// lands before its done channel is closed, so a get after awaitRebuild reads
// it.
func (c *coreCatalogue) awaitRebuild() {
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

// countingBuild is a build function whose calls are counted and whose
// completion the test controls: every call announces itself on entered, then
// blocks on release until the test lets it through, and returns the tool
// named for its ordinal.
type countingBuild struct {
	calls   atomic.Int32
	entered chan struct{}
	proceed chan struct{}
}

func newCountingBuild() *countingBuild {
	return &countingBuild{entered: make(chan struct{}, 64), proceed: make(chan struct{})}
}

func (b *countingBuild) build() ([]mcp.Tool, map[string][]string) {
	n := b.calls.Add(1)
	b.entered <- struct{}{}
	<-b.proceed
	name := "workflow_" + string(rune('a'+n-1))
	return []mcp.Tool{{Name: name}}, map[string][]string{name: {"x_k8s_get_pods"}}
}

// awaitBuild returns once a build has started and is blocked in build.
func (b *countingBuild) awaitBuild() { <-b.entered }

// release lets the blocked build through.
func (b *countingBuild) release() { b.proceed <- struct{}{} }

// releaseOne waits for a build to start and lets it through.
func (b *countingBuild) releaseOne() { b.awaitBuild(); b.release() }

func newTestCatalogue(now *time.Time) *coreCatalogue {
	c := newCoreCatalogue()
	c.now = func() time.Time { return *now }
	c.maxAge = 5 * time.Minute
	return c
}

// The core catalogue is built once and served until it is invalidated or
// older than its maximum age; every meta-tool call used to rebuild it, which
// on an installation is a list of every workflow definition (#1225).
func TestCoreCatalogue_BuildsOnceUntilInvalidated(t *testing.T) {
	now := time.Now()
	c := newTestCatalogue(&now)
	b := newCountingBuild()
	go b.releaseOne()

	tools, steps := c.get(b.build)
	_, _ = c.get(b.build)
	assert.EqualValues(t, 1, b.calls.Load(), "the second read is served from the catalogue")
	assert.Equal(t, "workflow_a", tools[0].Name)
	assert.Equal(t, []string{"x_k8s_get_pods"}, steps["workflow_a"])

	// Callers get their own slice: appending to it does not grow the catalogue.
	tools = append(tools, mcp.Tool{Name: "extra"})
	again, _ := c.get(b.build)
	assert.Len(t, again, 1)
	assert.Len(t, tools, 2)

	c.invalidate()
	go b.releaseOne()
	fresh, _ := c.get(b.build)
	assert.EqualValues(t, 2, b.calls.Load(), "an invalidated catalogue is rebuilt before the next read is served")
	assert.Equal(t, "workflow_b", fresh[0].Name, "the read after an invalidation sees the rebuilt catalogue")
}

// A read that finds the catalogue older than its maximum age is served the
// catalogue as it is and starts one rebuild in the background; the rebuilt
// catalogue is what the reads after it see (#1231).
func TestCoreCatalogue_RefreshesInTheBackgroundPastMaxAge(t *testing.T) {
	now := time.Now()
	c := newTestCatalogue(&now)
	b := newCountingBuild()
	go b.releaseOne()
	_, _ = c.get(b.build)
	require.EqualValues(t, 1, b.calls.Load())

	now = now.Add(c.maxAge + time.Second)
	// The build is not released: were the read to wait for it, the test
	// would hang here.
	old, _ := c.get(b.build)
	assert.Equal(t, "workflow_a", old[0].Name, "the read past the age is served the current catalogue")
	b.awaitBuild()
	stillOld, _ := c.get(b.build)
	assert.Equal(t, "workflow_a", stillOld[0].Name)
	assert.EqualValues(t, 2, b.calls.Load(), "one rebuild is in flight; further reads past the age do not start their own")

	b.release()
	c.awaitRebuild()
	fresh, steps := c.get(b.build)
	assert.Equal(t, "workflow_b", fresh[0].Name, "the rebuilt catalogue is served once it has landed")
	assert.Equal(t, []string{"x_k8s_get_pods"}, steps["workflow_b"])
	assert.EqualValues(t, 2, b.calls.Load(), "a fresh catalogue is not rebuilt again")

	now = now.Add(c.maxAge - time.Second)
	_, _ = c.get(b.build)
	assert.EqualValues(t, 2, b.calls.Load(), "the age is counted from the rebuild, not from the read that started it")
}

// A definition change while a rebuild runs makes that rebuild's result stale:
// it is built again before anything is served, so a reader arriving after the
// change never sees a catalogue built before it.
func TestCoreCatalogue_InvalidationDuringRebuildBuildsAgain(t *testing.T) {
	now := time.Now()
	c := newTestCatalogue(&now)
	b := newCountingBuild()
	go b.releaseOne()
	_, _ = c.get(b.build)

	now = now.Add(c.maxAge + time.Second)
	_, _ = c.get(b.build) // starts the background rebuild, blocked in build
	b.awaitBuild()
	require.EqualValues(t, 2, b.calls.Load())

	c.invalidate()
	got := make(chan string)
	go func() {
		tools, _ := c.get(b.build)
		got <- tools[0].Name
	}()

	b.release()    // the stale result: discarded, built again
	b.releaseOne() // the current one
	assert.Equal(t, "workflow_c", <-got, "the reader after the change is served the catalogue built after it")
	assert.EqualValues(t, 3, b.calls.Load(), "the stale rebuild is repeated once, not once per reader")
	c.awaitRebuild()
}

// Concurrent readers of an invalid catalogue share a single rebuild.
func TestCoreCatalogue_ConcurrentReadersShareOneRebuild(t *testing.T) {
	now := time.Now()
	c := newTestCatalogue(&now)
	b := newCountingBuild()

	const readers = 16
	var wg sync.WaitGroup
	names := make(chan string, readers)
	for range readers {
		wg.Go(func() {
			tools, _ := c.get(b.build)
			names <- tools[0].Name
		})
	}
	b.releaseOne()
	wg.Wait()
	close(names)
	for name := range names {
		assert.Equal(t, "workflow_a", name)
	}
	assert.EqualValues(t, 1, b.calls.Load(), "one build serves every concurrent reader")
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
