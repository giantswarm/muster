package servericon

import (
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// maxRasterSize is the largest edge, in pixels, a raster icon is preferred at.
const maxRasterSize = 256

// Select picks the icon to show: one for a light background (no theme or
// "light") over a dark one, an SVG over a raster, and among rasters the
// largest at or under 256px, else the smallest above. Icons whose declared
// MIME type is not allowed are skipped.
func Select(icons []mcp.Icon) (mcp.Icon, bool) {
	var light, other []mcp.Icon
	for _, icon := range icons {
		if icon.Src == "" {
			continue
		}
		if icon.MIMEType != "" && !allowedMIME(normalizeMIME(icon.MIMEType)) {
			continue
		}
		if icon.Theme == "" || icon.Theme == mcp.IconThemeLight {
			light = append(light, icon)
		} else {
			other = append(other, icon)
		}
	}
	if best, ok := pick(light); ok {
		return best, true
	}
	return pick(other)
}

func pick(icons []mcp.Icon) (mcp.Icon, bool) {
	var best mcp.Icon
	bestRank := -1
	for _, icon := range icons {
		if rank := rankIcon(icon); rank > bestRank {
			best, bestRank = icon, rank
		}
	}
	return best, bestRank >= 0
}

// rankIcon orders icons: SVG highest, then rasters at or under 256px by size,
// then rasters above 256px, smaller first.
func rankIcon(icon mcp.Icon) int {
	if isSVG(icon) {
		return 1 << 30
	}
	size := iconSize(icon.Sizes)
	if size <= maxRasterSize {
		return 1<<20 + size
	}
	return max(1<<20-size, 0)
}

func isSVG(icon mcp.Icon) bool {
	if normalizeMIME(icon.MIMEType) == mimeSVG {
		return true
	}
	src := strings.ToLower(icon.Src)
	if strings.HasPrefix(src, "data:") {
		return strings.HasPrefix(src, "data:"+mimeSVG)
	}
	path, _, _ := strings.Cut(src, "?")
	return strings.HasSuffix(path, ".svg")
}

// iconSize returns the largest edge among sizes at or under 256px, or the
// smallest edge above when none is; 0 when no size parses.
func iconSize(sizes []string) int {
	under, over := 0, 0
	for _, entry := range sizes {
		for _, s := range strings.Fields(entry) {
			w, h, ok := strings.Cut(strings.ToLower(s), "x")
			if !ok {
				continue
			}
			wi, err1 := strconv.Atoi(w)
			hi, err2 := strconv.Atoi(h)
			if err1 != nil || err2 != nil || wi <= 0 || hi <= 0 {
				continue
			}
			edge := max(wi, hi)
			if edge <= maxRasterSize {
				under = max(under, edge)
			} else if over == 0 || edge < over {
				over = edge
			}
		}
	}
	if under > 0 {
		return under
	}
	return over
}
