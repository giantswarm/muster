package store

import (
	"container/list"
	"sync"
)

// DefaultCapabilityDocumentCacheBytes bounds the decoded capability documents
// the Valkey store keeps in memory, measured in bytes of the JSON they were
// decoded from. Documents are content-addressed and shared by every session
// that connected the same server the same way, so an installation holds few
// distinct documents however many sessions it serves (18 documents for 463
// sessions and 87 servers on one installation); the bound only matters for a
// pathological catalogue and keeps the decoded copies -- a few times the JSON
// size -- well inside the pod's memory.
const DefaultCapabilityDocumentCacheBytes = 32 << 20

// documentCache keeps decoded capability documents by reference, so a session
// listing resolves the references its hash carries without a GET and a JSON
// decode per server. A reference names its document's bytes (SHA-256), so a
// cached decode never goes stale: a server that lists different tools writes a
// different reference into the session's hash, and the next listing fetches
// that document once. Least recently used documents leave when the byte bound
// is exceeded.
type documentCache struct {
	mu       sync.Mutex
	maxBytes int
	bytes    int
	entries  map[string]*list.Element
	order    *list.List // front = most recently used
}

type documentEntry struct {
	ref  string
	caps *Capabilities
	size int
}

func newDocumentCache(maxBytes int) *documentCache {
	return &documentCache{
		maxBytes: maxBytes,
		entries:  make(map[string]*list.Element),
		order:    list.New(),
	}
}

// get returns the cached document for ref. The result is shared: callers copy
// before they modify (Capabilities.DeepCopy).
func (c *documentCache) get(ref string) (*Capabilities, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[ref]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*documentEntry).caps, true
}

// put caches caps as the document ref names, size being the length of the
// JSON it was decoded from. A document larger than the whole bound is not
// kept.
func (c *documentCache) put(ref string, caps *Capabilities, size int) {
	if c == nil || caps == nil || size > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[ref]; ok {
		c.order.MoveToFront(el)
		return
	}
	c.entries[ref] = c.order.PushFront(&documentEntry{ref: ref, caps: caps, size: size})
	c.bytes += size
	for c.bytes > c.maxBytes {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(*documentEntry)
		c.order.Remove(oldest)
		delete(c.entries, entry.ref)
		c.bytes -= entry.size
	}
}

// len reports how many documents are cached.
func (c *documentCache) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
