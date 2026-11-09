package toolset

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/giantswarm/muster/v5/internal/servericon"
)

const testSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="14" fill="#000"/></svg>`

func svgDataURI(svg string) string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}

func TestBuiltInIcons_PassIconValidation(t *testing.T) {
	presets := builtInPresets()
	for _, name := range builtInOrder {
		icon := presets[name].Icon
		require.NotEmpty(t, icon, name)
		canonical, err := servericon.FromDataURI(icon)
		require.NoError(t, err, name)
		assert.Equal(t, canonical, icon, "%s icon is stored in its sanitised form", name)
		assert.LessOrEqual(t, len(icon), servericon.MaxEncodedLen, name)
	}
}

func TestBuiltInIcons_ArtworkSurvivesSanitiser(t *testing.T) {
	for name, raw := range map[string]string{PresetReadOnly: presetReadOnlySVG, PresetNone: presetNoneSVG, PresetFull: presetFullSVG} {
		assert.Equal(t, svgDataURI(raw), builtInIcons[name], "%s artwork is stored byte-identical", name)
	}
}

func TestNewRegistry_PresetIcon(t *testing.T) {
	valid := svgDataURI(testSVG)
	canonical, err := servericon.FromDataURI(valid)
	require.NoError(t, err)

	cases := []struct {
		name    string
		icon    string
		want    string // stored icon when valid
		wantErr string
	}{
		{name: "none", icon: "", want: ""},
		{name: "valid svg", icon: valid, want: canonical},
		{name: "percent-encoded svg stored as base64", icon: "data:image/svg+xml," + url.PathEscape(testSVG), want: canonical},
		{name: "svg sanitised", icon: svgDataURI(strings.Replace(testSVG, "<circle", `<script>alert(1)</script><circle onclick="x()"`, 1)), want: canonical},
		{name: "not a data URI", icon: "https://example.com/icon.svg", wantErr: "not a data: URI"},
		{name: "wrong mime", icon: "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(testSVG)), wantErr: `type "text/html" not allowed`},
		{name: "declared mime does not match content", icon: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(testSVG)), wantErr: "declared image/png"},
		{name: "oversized", icon: svgDataURI(strings.Replace(testSVG, "<circle", "<title>"+strings.Repeat("a", servericon.MaxEncodedLen)+"</title><circle", 1)), wantErr: "over 65536"},
		{name: "not an svg", icon: svgDataURI("<html></html>"), wantErr: "invalid icon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewRegistry(PresetsConfig{"x": {Icon: tc.icon, Include: []Rule{{Pattern: "*"}}}})
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), `toolsetPresets: preset "x" icon is invalid: `)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			p, _ := r.Get("x")
			assert.Equal(t, tc.want, p.Icon)
		})
	}
}

func TestRegistry_ListCarriesIcons(t *testing.T) {
	icon, err := servericon.FromDataURI(svgDataURI(testSVG))
	require.NoError(t, err)
	r, err := NewRegistry(PresetsConfig{
		"with-icon":    {Icon: svgDataURI(testSVG), Include: []Rule{{Pattern: "*"}}},
		"without-icon": {Include: []Rule{{Pattern: "*"}}},
	})
	require.NoError(t, err)
	infos := r.List()
	require.Len(t, infos, 5)
	for _, info := range infos[:3] {
		assert.Equal(t, builtInIcons[info.Name], info.Icon, info.Name)
		assert.NotEmpty(t, info.Icon, info.Name)
	}
	assert.Equal(t, Info{Name: "with-icon", Icon: icon}, infos[3])
	assert.Equal(t, Info{Name: "without-icon"}, infos[4])
}

func TestPresetsConfig_YAMLIcon(t *testing.T) {
	src := "infrastructure:\n  icon: \"" + svgDataURI(testSVG) + "\"\n  include:\n    - pattern: \"*\"\n"
	var cfg PresetsConfig
	require.NoError(t, yaml.Unmarshal([]byte(src), &cfg))
	assert.Equal(t, svgDataURI(testSVG), cfg["infrastructure"].Icon)
}
