package toolset

import (
	"fmt"
	"regexp"
	"strings"
)

// HeaderName is the request header that carries an agent's declared toolset.
const HeaderName = "X-Muster-Toolset"

// MaxInlineSelectors caps the number of selectors a toolset may carry inline
// (in the header or the filter_tools argument). Larger selections are presets.
const MaxInlineSelectors = 32

// Inline selector kinds.
const (
	KindPreset   = "preset"
	KindServer   = "server"
	KindWorkflow = "workflow"
	KindTool     = "tool"

	// kindToolset is reserved for the shared-toolset follow-up and rejected.
	kindToolset = "toolset"
	// kindLabel exists inside presets only (#1168) and is rejected inline.
	kindLabel = "label"
)

// selectorPattern is the inline grammar: exact names, no whitespace or commas.
var selectorPattern = regexp.MustCompile(`^(preset|server|workflow|tool):[^\s,]+$`)

// Selector is one inline selector, e.g. preset:read-only or tool:x_k8s_get.
type Selector struct {
	Kind string
	Name string
}

// String renders the selector in its inline form.
func (s Selector) String() string { return s.Kind + ":" + s.Name }

// Toolset is a parsed inline toolset: the selectors as given (trimmed) and
// their parsed form. Raw is kept for echoing and for naming the toolset in
// errors exactly as the caller wrote it.
type Toolset struct {
	Raw       []string
	Selectors []Selector
}

// String renders the toolset as it appears in errors and log lines.
func (t Toolset) String() string { return "[" + strings.Join(t.Raw, ",") + "]" }

// Presets returns the names of the presets the toolset references, in order.
func (t Toolset) Presets() []string {
	var names []string
	for _, s := range t.Selectors {
		if s.Kind == KindPreset {
			names = append(names, s.Name)
		}
	}
	return names
}

// ParseHeader parses the value of the X-Muster-Toolset header: a comma-separated
// list of inline selectors with whitespace around each selector trimmed. The
// caller decides whether the header was present; a present-but-empty value is
// an error here, never "no toolset".
func ParseHeader(value string) (Toolset, error) {
	if strings.TrimSpace(value) == "" {
		return ParseInline(nil)
	}
	parts := strings.Split(value, ",")
	list := make([]string, 0, len(parts))
	for _, p := range parts {
		list = append(list, strings.TrimSpace(p))
	}
	return ParseInline(list)
}

// ParseInline parses a list of inline selectors (the filter_tools "toolset"
// argument, or a header already split on commas). Every error names the
// toolset and the offending selector so the caller can relay it verbatim.
func ParseInline(list []string) (Toolset, error) {
	ts := Toolset{Raw: make([]string, 0, len(list))}
	for _, s := range list {
		ts.Raw = append(ts.Raw, strings.TrimSpace(s))
	}
	if len(ts.Raw) == 0 {
		return Toolset{}, fmt.Errorf("toolset [] is empty; use %q for an agent without tools", KindPreset+":"+PresetNone)
	}
	if len(ts.Raw) > MaxInlineSelectors {
		return Toolset{}, fmt.Errorf("toolset %s has %d selectors, more than the %d allowed inline; define a preset",
			ts, len(ts.Raw), MaxInlineSelectors)
	}
	for _, raw := range ts.Raw {
		kind, name, hasColon := strings.Cut(raw, ":")
		switch {
		case hasColon && kind == kindToolset:
			return Toolset{}, fmt.Errorf("toolset %s selector %q is reserved for shared toolsets", ts, raw)
		case hasColon && kind == kindLabel:
			return Toolset{}, fmt.Errorf("toolset %s selector %q is allowed inside presets only", ts, raw)
		case !selectorPattern.MatchString(raw):
			return Toolset{}, fmt.Errorf("toolset %s selector %q is malformed; expected preset:<name>, server:<name>, workflow:<name> or tool:<name>", ts, raw)
		}
		ts.Selectors = append(ts.Selectors, Selector{Kind: kind, Name: name})
	}
	return ts, nil
}
