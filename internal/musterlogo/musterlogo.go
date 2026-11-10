package musterlogo

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	mimeType = "image/svg+xml"
	navy     = `fill="#0e2151"`
	white    = `fill="#fff"`
)

// logoSVG is the light-background variant of the muster logo from
// giantswarm/web-assets assets/app-icons/muster/1/light.svg, with the
// stylesheet classes written as fill attributes (muster's SVG sanitiser drops
// <style>) and zero-length path segments removed. The dark variant swaps its
// two fills.
//
//go:embed muster.svg
var logoSVG []byte

var (
	light = bytes.TrimSpace(logoSVG)
	dark  = []byte(strings.NewReplacer(navy, white, white, navy).Replace(string(light)))

	lightSrc = dataURI(light)
	darkSrc  = dataURI(dark)
)

func dataURI(svg []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(svg)
}

func icon(src string, theme mcp.IconTheme) mcp.Icon {
	return mcp.Icon{Src: src, MIMEType: mimeType, Sizes: []string{"any"}, Theme: theme}
}

// Icons returns the muster logo as MCP icons, the light-background variant
// first.
func Icons() []mcp.Icon {
	return []mcp.Icon{icon(lightSrc, mcp.IconThemeLight), icon(darkSrc, mcp.IconThemeDark)}
}

// WithIcons returns a server option that sets [Icons] as the server's icons.
func WithIcons() server.ServerOption {
	return server.WithIcons(Icons()...)
}
