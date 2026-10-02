package toolset

import (
	"path/filepath"
	"strings"
)

// Unlisted describes a server tool the session cannot list yet because it
// has not signed in to the server: what is known of it before sign-in is its
// name, its server and the server's labels, never its annotations.
type Unlisted struct {
	// Server is the MCPServer that would serve the tool.
	Server string
	// Family is the server's family name when its tools are grouped under
	// it, as the family's listed tools carry it in Entry.Server.
	Family string
	// Prefix is the exposed prefix every tool of the server carries.
	Prefix string
	// Name is the exposed tool name. Empty asks about any tool of the server.
	Name string
	// Owns reports whether an exposed name is one of the server's tools; it
	// answers tool selectors and rules when Name is empty.
	Owns func(exposedName string) bool
}

// MaySelect reports whether ts could select the unlisted tool (or, with an
// empty Name, some tool of the server) once the session signs in and the
// tool is listed. A readOnly rule may or may not match an annotation that is
// not known yet: it counts as a possible include and never as an exclude, so
// MaySelect is true whenever the resolution after sign-in could hold the
// tool, and false only when no listing can put it in the toolset.
func (r *Registry) MaySelect(ts Toolset, u Unlisted, labels ServerLabels) bool {
	for _, sel := range ts.Selectors {
		switch sel.Kind {
		case KindTool:
			if u.names(sel.Name) {
				return true
			}
		case KindServer:
			if u.servedBy(sel.Name) {
				return true
			}
		case KindPreset:
			if r.presetMaySelect(sel.Name, u, labels, map[string]bool{}) {
				return true
			}
		}
	}
	return false
}

// presetMaySelect is MaySelect for one preset: some include may match and no
// exclude certainly does.
func (r *Registry) presetMaySelect(name string, u Unlisted, labels ServerLabels, visiting map[string]bool) bool {
	p, ok := r.presets[name]
	if !ok || visiting[name] {
		return false
	}
	visiting[name] = true
	defer delete(visiting, name)

	included := false
	for _, rule := range p.Include {
		if rule.Preset != "" {
			included = r.presetMaySelect(rule.Preset, u, labels, visiting)
		} else {
			included, _ = ruleMayMatch(rule, u, labels)
		}
		if included {
			break
		}
	}
	if !included {
		return false
	}
	for _, rule := range p.Exclude {
		if _, certain := ruleMayMatch(rule, u, labels); certain {
			return false
		}
	}
	return true
}

// ruleMayMatch evaluates a preset rule against an unlisted tool: may when the
// rule could match it once listed, certain when it matches whatever the
// listing says (with an empty Name: every tool of the server).
func ruleMayMatch(rule Rule, u Unlisted, labels ServerLabels) (may, certain bool) {
	switch {
	case rule.Tool != "":
		matched := u.names(rule.Tool)
		return matched, matched && u.Name != ""
	case rule.Pattern != "":
		if u.Name != "" {
			matched, _ := filepath.Match(rule.Pattern, u.Name)
			return matched, matched
		}
		return patternMayCoverPrefix(rule.Pattern, u.Prefix)
	case rule.Server != "":
		matched := u.servedBy(rule.Server)
		return matched, matched
	case rule.ReadOnly != nil:
		return *rule.ReadOnly, false
	case rule.Label != "":
		matched := entryLabelled(Entry{Kind: KindEntryTool, Server: u.Server}, rule.Label, labels)
		return matched, matched
	}
	return false, false
}

// patternMayCoverPrefix tells, for a glob and a name prefix, whether the glob
// may match some name carrying the prefix and whether it matches all of them.
// The glob's literal head (up to its first meta character) decides the first:
// the head and the prefix must agree where both are defined. It matches all
// of them when the head is a prefix of the prefix and the rest is "*".
func patternMayCoverPrefix(pattern, prefix string) (may, all bool) {
	head := pattern
	if i := strings.IndexAny(pattern, `*?[\`); i >= 0 {
		head = pattern[:i]
	}
	if head == pattern {
		return strings.HasPrefix(pattern, prefix), false
	}
	if !strings.HasPrefix(prefix, head) && !strings.HasPrefix(head, prefix) {
		return false, false
	}
	return true, pattern == head+"*" && strings.HasPrefix(prefix, head)
}

// names reports whether an exact tool name designates the unlisted tool.
func (u Unlisted) names(name string) bool {
	if u.Name != "" {
		return name == u.Name
	}
	return u.Owns != nil && u.Owns(name)
}

// servedBy reports whether a server: selector or rule names the tool's
// server, or the family its tools are grouped under.
func (u Unlisted) servedBy(server string) bool {
	return server == u.Server || (u.Family != "" && server == u.Family)
}
