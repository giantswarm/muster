package brand

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/servericon"
)

func TestIcons(t *testing.T) {
	icons := Icons()
	require.Len(t, icons, 2)
	require.Equal(t, mcp.IconThemeLight, icons[0].Theme)
	require.Equal(t, mcp.IconThemeDark, icons[1].Theme)

	selected, ok := servericon.Select(icons)
	require.True(t, ok)
	require.Equal(t, icons[0], selected, "muster shows the light variant")

	for _, icon := range icons {
		require.Equal(t, "image/svg+xml", icon.MIMEType)
		require.Equal(t, []string{"any"}, icon.Sizes)

		// The logo survives muster's SVG sanitiser with both colours.
		uri, err := servericon.FromDataURI(icon.Src)
		require.NoError(t, err)
		payload, ok := strings.CutPrefix(uri, "data:image/svg+xml;base64,")
		require.True(t, ok)
		svg, err := base64.StdEncoding.DecodeString(payload)
		require.NoError(t, err)
		require.Contains(t, string(svg), `fill="#0e2151"`, icon.Theme)
		require.Contains(t, string(svg), `fill="#fff"`, icon.Theme)
		require.Equal(t, 2, strings.Count(string(svg), "<path"), icon.Theme)
	}
}
