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
	presetReadOnlySVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16" fill="#0F6E56"/><g fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M8 16c2.5-4 5-6 8-6s5.5 2 8 6c-2.5 4-5 6-8 6s-5.5-2-8-6z"/><circle cx="16" cy="16" r="2.5"/></g></svg>`
	presetNoneSVG     = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16" fill="#5F5E5A"/><g fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="16" cy="16" r="6"/><path d="M11.8 11.8l8.4 8.4"/></g></svg>`
	presetFullSVG     = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16" fill="#185FA5"/><g fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="10" y="10" width="5" height="5" rx="1"/><rect x="17" y="10" width="5" height="5" rx="1"/><rect x="10" y="17" width="5" height="5" rx="1"/><rect x="17" y="17" width="5" height="5" rx="1"/></g></svg>`
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
