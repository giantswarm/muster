package metatools

import (
	"maps"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

// analyzedDoc is a rankDoc tokenised and folded once: per field, the count of
// each term and the number of tokens. It depends only on the tool, so it is
// computed once per tool and shared by every query (see rankTermCache); it is
// never modified after analyzeDoc returns it.
type analyzedDoc struct {
	tf     [numRankFields]map[string]int
	length [numRankFields]int
}

// analyzeDoc tokenises and folds the fields of doc (see rankTerms).
func analyzeDoc(doc rankDoc) *analyzedDoc {
	var a analyzedDoc
	for f, text := range doc.fields() {
		tokens := rankTerms(text)
		tf := make(map[string]int, len(tokens))
		for _, t := range tokens {
			tf[t]++
		}
		if len(tf) < len(tokens) {
			// The analysis is kept for as long as the tool is used, so size
			// the map for its distinct terms rather than its tokens.
			// (maps.Clone would keep the original's capacity.)
			sized := make(map[string]int, len(tf))
			maps.Copy(sized, tf)
			tf = sized
		}
		a.tf[f] = tf
		a.length[f] = len(tokens)
	}
	return &a
}

// analyzeTool analyses a tool's name, summary and details (see
// splitDescription).
func analyzeTool(t mcp.Tool) *analyzedDoc {
	summary, details := splitDescription(t.Description)
	return analyzeDoc(rankDoc{name: t.Name, summary: summary, details: details})
}

// rankCacheSweepAt is the number of tools above which the rank term cache drops
// the tools no query has used since its previous sweep. A tool that queries
// keep using is never dropped, so a catalogue of any size stays cached; tools
// that are gone (a removed server) or renamed are dropped at the next sweep.
const rankCacheSweepAt = 4096

// rankCacheEntry is a tool's analysis and the description it was made from.
type rankCacheEntry struct {
	description string
	doc         *analyzedDoc
	// used is the sweep epoch in which a query last used the entry.
	used uint64
}

// rankTermCache keeps each tool's analyzedDoc by tool name, so that a
// filter_tools query tokenises and folds only the tools it has not seen, or
// whose description changed, instead of every word of every candidate tool.
// It is safe for concurrent use.
type rankTermCache struct {
	mu      sync.Mutex
	entries map[string]*rankCacheEntry
	// epoch counts the sweeps; sweepAt is the size that triggers the next.
	epoch   uint64
	sweepAt int
}

func newRankTermCache() *rankTermCache {
	return &rankTermCache{entries: make(map[string]*rankCacheEntry), sweepAt: rankCacheSweepAt}
}

// analyze returns the analyzedDoc of each tool, in order, analysing the tools
// the cache does not hold, or holds for another description, outside the lock.
// A nil cache analyses every tool.
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
		// A description that did not change is usually the same string the
		// entry holds, which compares without reading it.
		if e, ok := c.entries[t.Name]; ok && e.description == t.Description {
			e.used = c.epoch
			docs[i] = e.doc
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
		c.entries[tools[i].Name] = &rankCacheEntry{description: tools[i].Description, doc: docs[i], used: c.epoch}
	}
	if len(c.entries) > c.sweepAt {
		c.sweep()
	}
	c.mu.Unlock()
	return docs
}

// sweep drops the entries no query has used since the previous sweep and
// starts a new epoch. When the tools in use alone exceed rankCacheSweepAt, the
// next sweep waits until the cache has doubled, so that a large catalogue is
// not swept on every query. The caller holds c.mu.
func (c *rankTermCache) sweep() {
	for name, e := range c.entries {
		if e.used < c.epoch {
			delete(c.entries, name)
		}
	}
	c.epoch++
	c.sweepAt = max(rankCacheSweepAt, 2*len(c.entries))
}
