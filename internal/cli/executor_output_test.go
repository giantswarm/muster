package cli

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFirstText(t *testing.T) {
	t.Run("skips leading non-text items", func(t *testing.T) {
		text, err := firstText([]mcp.Content{
			mcp.NewImageContent("aW1hZ2U=", "image/png"),
			mcp.NewTextContent(`{"caption":"graph"}`),
		})
		require.NoError(t, err)
		assert.Equal(t, `{"caption":"graph"}`, text)
	})

	t.Run("renders content items without text as JSON", func(t *testing.T) {
		text, err := firstText([]mcp.Content{mcp.NewImageContent("aW1hZ2U=", "image/png")})
		require.NoError(t, err)
		assert.JSONEq(t, `[{"type":"image","data":"aW1hZ2U=","mimeType":"image/png"}]`, text)
	})
}
