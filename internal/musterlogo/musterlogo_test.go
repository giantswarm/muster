package musterlogo

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/servericon"
)

var pathFill = regexp.MustCompile(`<path fill="([^"]+)"`)

func decode(t *testing.T, src string) string {
	t.Helper()
	payload, ok := strings.CutPrefix(src, "data:image/svg+xml;base64,")
	require.True(t, ok, src)
	svg, err := base64.StdEncoding.DecodeString(payload)
	require.NoError(t, err)
	return string(svg)
}

func TestIcons(t *testing.T) {
	icons := Icons()
	require.Len(t, icons, 2)

	want := []struct {
		theme mcp.IconTheme
		fills []string
	}{
		{mcp.IconThemeLight, []string{"#0e2151", "#fff"}},
		{mcp.IconThemeDark, []string{"#fff", "#0e2151"}},
	}
	for i, w := range want {
		got := icons[i]
		require.Equal(t, w.theme, got.Theme)
		require.Equal(t, "image/svg+xml", got.MIMEType)
		require.Equal(t, []string{"any"}, got.Sizes)

		svg := decode(t, got.Src)
		require.True(t, strings.HasPrefix(svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">`), svg)
		var fills []string
		for _, m := range pathFill.FindAllStringSubmatch(svg, -1) {
			fills = append(fills, m[1])
		}
		require.Equal(t, w.fills, fills, w.theme)
	}
	require.Equal(t, string(light), decode(t, icons[0].Src))
}

func TestIconsSurviveSanitiser(t *testing.T) {
	for _, icon := range Icons() {
		got, err := servericon.FromDataURI(icon.Src)
		require.NoError(t, err)
		require.Equal(t, decode(t, icon.Src), decode(t, got), "%s logo changed in the sanitiser", icon.Theme)
	}
}

func TestSelectPicksLight(t *testing.T) {
	icons := Icons()
	selected, ok := servericon.Select(icons)
	require.True(t, ok)
	require.Equal(t, icons[0], selected)
}

func TestIconsReturnsFreshValues(t *testing.T) {
	first := Icons()
	first[0].Sizes[0] = "changed"
	first[0].Theme = mcp.IconThemeDark

	again := Icons()
	require.Equal(t, []string{"any"}, again[0].Sizes)
	require.Equal(t, mcp.IconThemeLight, again[0].Theme)
}
