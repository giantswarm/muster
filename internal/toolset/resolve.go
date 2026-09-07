package toolset

import (
	"path/filepath"
	"slices"
	"sort"
)

// Resolution is the outcome of evaluating a toolset against a catalogue.
type Resolution struct {
	// Selected holds the exposed names of the tools the toolset resolves to.
	Selected map[string]struct{}
	// Unmatched lists the inline selectors (as written) that selected no
	// tool in this catalogue. preset:none is empty by definition and is never
	// reported.
	Unmatched []string
	// Servers holds every server that contributes at least one selected tool
	// (family members included). Resource and prompt accessors use it to
	// decide which servers are inside the toolset.
	Servers map[string]struct{}
}

// Contains reports whether the exposed tool name is inside the resolution.
func (r Resolution) Contains(name string) bool {
	_, ok := r.Selected[name]
	return ok
}

// ContainsServer reports whether a server contributes to the resolution.
func (r Resolution) ContainsServer(server string) bool {
	_, ok := r.Servers[server]
	return ok
}

// Names returns the selected exposed names, sorted.
func (r Resolution) Names() []string {
	names := make([]string, 0, len(r.Selected))
	for name := range r.Selected {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Resolve evaluates ts against entries. Inline selectors are unioned; each
// preset resolves to its includes minus its excludes, recursively for
// composed presets. Every preset the toolset names must exist (Check) or the
// unknown-preset error is returned.
func (r *Registry) Resolve(ts Toolset, entries []Entry) (Resolution, error) {
	if err := r.Check(ts); err != nil {
		return Resolution{}, err
	}
	res := Resolution{Selected: map[string]struct{}{}, Servers: map[string]struct{}{}}
	for i, sel := range ts.Selectors {
		var matched map[string]struct{}
		switch sel.Kind {
		case KindPreset:
			matched = r.resolvePreset(sel.Name, entries, map[string]bool{})
		default:
			matched = matchInline(sel, entries)
		}
		isNone := sel.Kind == KindPreset && sel.Name == PresetNone
		if len(matched) == 0 && !isNone {
			res.Unmatched = append(res.Unmatched, ts.Raw[i])
		}
		for name := range matched {
			res.Selected[name] = struct{}{}
		}
	}
	for _, e := range entries {
		if _, ok := res.Selected[e.Name]; !ok {
			continue
		}
		if e.Server != "" {
			res.Servers[e.Server] = struct{}{}
		}
		for _, s := range e.Servers {
			res.Servers[s] = struct{}{}
		}
	}
	return res, nil
}

// matchInline evaluates a server:, workflow: or tool: selector.
func matchInline(sel Selector, entries []Entry) map[string]struct{} {
	matched := map[string]struct{}{}
	for _, e := range entries {
		var ok bool
		switch sel.Kind {
		case KindTool:
			ok = e.Name == sel.Name
		case KindServer:
			ok = entryServedBy(e, sel.Name)
		case KindWorkflow:
			ok = e.Kind == KindEntryWorkflow && e.Name == "workflow_"+sel.Name
		}
		if ok {
			matched[e.Name] = struct{}{}
		}
	}
	return matched
}

// entryServedBy reports whether server owns the entry: its server (or family)
// name, or one of the family members providing it.
func entryServedBy(e Entry, server string) bool {
	if e.Kind != KindEntryTool {
		return false
	}
	return e.Server == server || slices.Contains(e.Servers, server)
}

// resolvePreset returns the names a preset selects: includes minus excludes.
// visiting guards against composition cycles (rejected at construction, but
// the walk stays safe regardless).
func (r *Registry) resolvePreset(name string, entries []Entry, visiting map[string]bool) map[string]struct{} {
	matched := map[string]struct{}{}
	p, ok := r.presets[name]
	if !ok || visiting[name] {
		return matched
	}
	visiting[name] = true
	defer delete(visiting, name)

	for _, rule := range p.Include {
		if rule.Preset != "" {
			for n := range r.resolvePreset(rule.Preset, entries, visiting) {
				matched[n] = struct{}{}
			}
			continue
		}
		for _, e := range entries {
			if ruleMatches(rule, e) {
				matched[e.Name] = struct{}{}
			}
		}
	}
	for _, rule := range p.Exclude {
		for _, e := range entries {
			if ruleMatches(rule, e) {
				delete(matched, e.Name)
			}
		}
	}
	return matched
}

// ruleMatches evaluates one preset rule against one entry.
func ruleMatches(rule Rule, e Entry) bool {
	switch {
	case rule.Tool != "":
		return e.Name == rule.Tool
	case rule.Pattern != "":
		ok, _ := filepath.Match(rule.Pattern, e.Name)
		return ok
	case rule.Server != "":
		return entryServedBy(e, rule.Server)
	case rule.Workflow != "":
		return e.Kind == KindEntryWorkflow && e.Name == "workflow_"+rule.Workflow
	case rule.ReadOnly != nil:
		return *rule.ReadOnly && e.ReadOnly
	}
	return false
}
