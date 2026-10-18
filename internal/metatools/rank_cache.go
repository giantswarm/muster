package metatools

import (
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

// rankCacheGeneration bounds the rank term cache: once a generation holds this
// many tools, it becomes the previous one and a new generation starts, so the
// cache holds at most twice as many tools (an entry for a tool with a few
// paragraphs of description takes about 8 KB). A catalogue has hundreds of
// tools, and every session shares the entries of the tools it sees, so the
// bound is only reached as tool descriptions change over time.
const rankCacheGeneration = 2048

// rankCacheKey identifies the text a tool is ranked by. A tool whose
// description changes (a server reconnects with new tool definitions) gets a
// new entry; the old one ages out with its generation.
type rankCacheKey struct {
	name        string
	description string
}

// rankTermCache keeps each tool's analyzedDoc, so that a filter_tools query
// tokenises and folds only the tools it has not seen before instead of every
// word of every candidate tool. It is safe for concurrent use.
type rankTermCache struct {
	mu       sync.Mutex
	current  map[rankCacheKey]*analyzedDoc
	previous map[rankCacheKey]*analyzedDoc
}

func newRankTermCache() *rankTermCache {
	return &rankTermCache{current: make(map[rankCacheKey]*analyzedDoc)}
}

// analyze returns the analyzedDoc of each tool, in order, analysing the tools
// the cache does not hold yet outside the lock. A nil cache analyses every tool.
func (c *rankTermCache) analyze(tools []mcp.Tool) []*analyzedDoc {
	docs := make([]*analyzedDoc, len(tools))
	if c == nil {
		for i, t := range tools {
			docs[i] = analyzeTool(t)
		}
		return docs
	}
	var missing []int

	c.mu.Lock()
	for i, t := range tools {
		key := rankCacheKey{name: t.Name, description: t.Description}
		if doc, ok := c.current[key]; ok {
			docs[i] = doc
		} else if doc, ok := c.previous[key]; ok {
			docs[i] = doc
			c.store(key, doc)
		} else {
			missing = append(missing, i)
		}
	}
	c.mu.Unlock()

	if len(missing) == 0 {
		return docs
	}
	for _, i := range missing {
		docs[i] = analyzeTool(tools[i])
	}

	c.mu.Lock()
	for _, i := range missing {
		c.store(rankCacheKey{name: tools[i].Name, description: tools[i].Description}, docs[i])
	}
	c.mu.Unlock()
	return docs
}

// store adds doc to the current generation, starting a new one when it is
// full. The caller holds c.mu.
func (c *rankTermCache) store(key rankCacheKey, doc *analyzedDoc) {
	if len(c.current) >= rankCacheGeneration {
		c.previous = c.current
		c.current = make(map[rankCacheKey]*analyzedDoc, rankCacheGeneration)
	}
	c.current[key] = doc
}

// analyzeTool analyses a tool's name, summary and details (see
// splitDescription).
func analyzeTool(t mcp.Tool) *analyzedDoc {
	summary, details := splitDescription(t.Description)
	return analyzeDoc(rankDoc{name: t.Name, summary: summary, details: details})
}
