package brand

import (
	_ "embed"

	toolkitbrand "github.com/giantswarm/mcp-toolkit/brand"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// The muster logo from giantswarm/web-assets
// assets/app-icons/muster/1/{light,dark}.svg, with the stylesheet classes
// written as fill attributes: muster's SVG sanitiser drops <style>.
var (
	//go:embed muster-light.svg
	lightLogo []byte
	//go:embed muster-dark.svg
	darkLogo []byte
)

// Icons returns the muster logo as MCP icons, the light-background variant
// first.
func Icons() []mcp.Icon {
	light := toolkitbrand.Icon(lightLogo)
	light.Theme = mcp.IconThemeLight
	dark := toolkitbrand.Icon(darkLogo)
	dark.Theme = mcp.IconThemeDark
	return []mcp.Icon{light, dark}
}

// WithIcons returns a server option that sets [Icons] as the server's icons.
func WithIcons() server.ServerOption {
	return server.WithIcons(Icons()...)
}
