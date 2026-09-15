package store

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentCache_EvictsLeastRecentlyUsedOverTheBound(t *testing.T) {
	c := newDocumentCache(100)
	doc := func(name string) *Capabilities { return &Capabilities{Tools: []mcp.Tool{{Name: name}}} }

	c.put("a", doc("a"), 40)
	c.put("b", doc("b"), 40)
	_, ok := c.get("a") // a is now the most recently used
	require.True(t, ok)

	c.put("c", doc("c"), 40) // 120 > 100: the least recently used, b, leaves
	_, ok = c.get("b")
	assert.False(t, ok, "b was the least recently used")
	got, ok := c.get("a")
	require.True(t, ok)
	assert.Equal(t, "a", got.Tools[0].Name)
	_, ok = c.get("c")
	assert.True(t, ok)
	assert.Equal(t, 2, c.len())
}

func TestDocumentCache_DocumentLargerThanTheBoundIsNotKept(t *testing.T) {
	c := newDocumentCache(10)
	c.put("huge", &Capabilities{}, 11)
	_, ok := c.get("huge")
	assert.False(t, ok)
	assert.Equal(t, 0, c.len())
}

func TestDocumentCache_NilIsInert(t *testing.T) {
	var c *documentCache
	c.put("a", &Capabilities{}, 1)
	_, ok := c.get("a")
	assert.False(t, ok)
	assert.Equal(t, 0, c.len())
}
