package toolset

import (
	"encoding/base64"
	"fmt"

	"github.com/giantswarm/muster/v5/internal/servericon"
)

// The built-in presets' artwork: one raw SVG per preset. Replace a constant
// with the final artwork; it goes through the same validation and sanitising
// as a configured preset's icon (servericon.FromDataURI), so anything the
// sanitiser drops is dropped here too, and invalid artwork panics at start.
const (
	presetReadOnlySVG = `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="14" fill="#2E7D32"/></svg>`
	presetNoneSVG     = `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="14" fill="#757575"/></svg>`
	presetFullSVG     = `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="14" fill="#1565C0"/></svg>`
)

// builtInIcons maps each built-in preset to its icon as a sanitised data: URI.
var builtInIcons = map[string]string{
	PresetReadOnly: svgIcon(PresetReadOnly, presetReadOnlySVG),
	PresetNone:     svgIcon(PresetNone, presetNoneSVG),
	PresetFull:     svgIcon(PresetFull, presetFullSVG),
}

// svgIcon turns a raw SVG into the canonical data: URI a configured icon is
// stored as. Invalid artwork is a programming error.
func svgIcon(preset, svg string) string {
	uri, err := servericon.FromDataURI("data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)))
	if err != nil {
		panic(fmt.Sprintf("built-in preset %q icon: %v", preset, err))
	}
	return uri
}
