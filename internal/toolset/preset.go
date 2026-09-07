package toolset

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Built-in preset names. They are always defined and cannot be redefined by
// configuration, so a toolset naming one of them can never hit the
// unknown-preset error.
const (
	PresetReadOnly = "read-only"
	PresetNone     = "none"
	PresetFull     = "full"
)

// builtInOrder is the order built-in presets are listed in.
var builtInOrder = []string{PresetReadOnly, PresetNone, PresetFull}

// presetNamePattern bounds preset names to what fits a selector and a values
// key: letters, digits, dot, underscore and dash.
var presetNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Rule is one selector inside a preset. Exactly one field is set.
//
//   - tool: <exact exposed name>
//   - pattern: <glob on the exposed name>   (e.g. core_*, x_mcp-kubernetes_*)
//   - server: <name>                         (every tool of that server; a family
//     name selects the family surface)
//   - workflow: <name>                       (the workflow's execution tool)
//   - readOnly: true                         (every tool annotated read-only,
//     including the derived workflow hint)
//   - preset: <name>                         (composition; include only)
//   - label: <key>=<value> | <key>           (every tool of an MCPServer whose
//     resource carries that label; #1168)
type Rule struct {
	Tool     string `yaml:"tool,omitempty" json:"tool,omitempty"`
	Pattern  string `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	Server   string `yaml:"server,omitempty" json:"server,omitempty"`
	Workflow string `yaml:"workflow,omitempty" json:"workflow,omitempty"`
	ReadOnly *bool  `yaml:"readOnly,omitempty" json:"readOnly,omitempty"`
	Preset   string `yaml:"preset,omitempty" json:"preset,omitempty"`
	Label    string `yaml:"label,omitempty" json:"label,omitempty"`
}

// ruleKeys are the accepted rule keys, named in validation errors.
const ruleKeys = "tool, pattern, server, workflow, readOnly, preset, label"

// labelPattern is the label rule grammar: a key, optionally "=" and a value.
// Keys follow Kubernetes label key syntax loosely (an optional DNS prefix and
// a name); "key=" matches an empty value, "key" alone matches presence.
var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*(=[A-Za-z0-9._-]*)?$`)

// labelSelector splits a label rule into key, value and whether a value was
// given (presence match otherwise).
func labelSelector(rule string) (key, value string, hasValue bool) {
	return strings.Cut(rule, "=")
}

// String renders the rule the way it is written in configuration.
func (r Rule) String() string {
	switch {
	case r.Tool != "":
		return "tool: " + r.Tool
	case r.Pattern != "":
		return "pattern: " + r.Pattern
	case r.Server != "":
		return "server: " + r.Server
	case r.Workflow != "":
		return "workflow: " + r.Workflow
	case r.ReadOnly != nil:
		return fmt.Sprintf("readOnly: %t", *r.ReadOnly)
	case r.Preset != "":
		return "preset: " + r.Preset
	case r.Label != "":
		return "label: " + r.Label
	}
	return "{}"
}

// setKeys counts how many rule keys are set.
func (r Rule) setKeys() int {
	n := 0
	for _, set := range []bool{r.Tool != "", r.Pattern != "", r.Server != "", r.Workflow != "", r.ReadOnly != nil, r.Preset != "", r.Label != ""} {
		if set {
			n++
		}
	}
	return n
}

// Preset is a named, described selection of tools: the union of its include
// rules minus its exclude rules.
type Preset struct {
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Include     []Rule `yaml:"include" json:"include"`
	Exclude     []Rule `yaml:"exclude,omitempty" json:"exclude,omitempty"`
}

// PresetsConfig is the `toolsetPresets:` block of muster's configuration.
type PresetsConfig map[string]Preset

// Info describes a preset for discovery (filter_tools include_presets).
type Info struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	BuiltIn     bool   `json:"built_in"`
}

// Registry holds the built-in presets and the configured ones, validated.
type Registry struct {
	presets    map[string]Preset
	builtIn    map[string]bool
	configured []string // sorted configured names
}

func builtInPresets() map[string]Preset {
	yes := true
	return map[string]Preset{
		PresetReadOnly: {
			Description: "Every tool its server annotates read-only, plus every workflow whose step tools are all read-only",
			Include:     []Rule{{ReadOnly: &yes}},
		},
		PresetNone: {
			Description: "No tools",
			Include:     []Rule{},
		},
		PresetFull: {
			Description: "The whole catalogue, including muster's core tools",
			Include:     []Rule{{Pattern: "*"}},
		},
	}
}

// BuiltIns returns a registry with only the built-in presets.
func BuiltIns() *Registry {
	r, _ := NewRegistry(nil)
	return r
}

// NewRegistry validates cfg and returns the registry of built-in plus
// configured presets. Every validation error names the preset (and rule) so
// the operator can fix the values; the first error stops startup.
func NewRegistry(cfg PresetsConfig) (*Registry, error) {
	r := &Registry{presets: builtInPresets(), builtIn: map[string]bool{}}
	for name := range r.presets {
		r.builtIn[name] = true
	}

	names := make([]string, 0, len(cfg))
	for name := range cfg {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if r.builtIn[name] {
			return nil, fmt.Errorf("toolsetPresets: preset %q is built in and cannot be redefined", name)
		}
		if !presetNamePattern.MatchString(name) {
			return nil, fmt.Errorf("toolsetPresets: preset name %q is invalid; use letters, digits, '.', '_' or '-'", name)
		}
		p := cfg[name]
		if err := validateRules(name, "include", p.Include, true); err != nil {
			return nil, err
		}
		if err := validateRules(name, "exclude", p.Exclude, false); err != nil {
			return nil, err
		}
		r.presets[name] = p
		r.configured = append(r.configured, name)
	}

	// Composition must reference known presets and must not cycle.
	for _, name := range r.configured {
		if err := r.checkComposition(name, nil); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func validateRules(preset, list string, rules []Rule, allowPreset bool) error {
	for i, rule := range rules {
		at := fmt.Sprintf("toolsetPresets: preset %q %s[%d]", preset, list, i)
		if rule.setKeys() != 1 {
			return fmt.Errorf("%s must set exactly one of %s", at, ruleKeys)
		}
		switch {
		case rule.Pattern != "":
			if _, err := filepath.Match(rule.Pattern, ""); err != nil {
				return fmt.Errorf("%s pattern %q is invalid: %v", at, rule.Pattern, err)
			}
		case rule.ReadOnly != nil && !*rule.ReadOnly:
			return fmt.Errorf("%s readOnly: false selects nothing; omit the rule", at)
		case rule.Preset != "" && !allowPreset:
			return fmt.Errorf("%s cannot compose a preset in %s; only include may", at, list)
		case rule.Label != "" && !labelPattern.MatchString(rule.Label):
			return fmt.Errorf("%s label %q is invalid; expected <key>=<value> or <key>", at, rule.Label)
		}
	}
	return nil
}

func (r *Registry) checkComposition(name string, path []string) error {
	for _, seen := range path {
		if seen == name {
			return fmt.Errorf("toolsetPresets: preset composition cycles: %s -> %s", strings.Join(path, " -> "), name)
		}
	}
	path = append(path, name)
	p, ok := r.presets[name]
	if !ok {
		return fmt.Errorf("toolsetPresets: preset %q includes unknown preset %q; known presets: %s",
			path[len(path)-2], name, strings.Join(r.Names(), ", "))
	}
	for _, rule := range p.Include {
		if rule.Preset != "" {
			if err := r.checkComposition(rule.Preset, path); err != nil {
				return err
			}
		}
	}
	return nil
}

// Get returns a preset by name.
func (r *Registry) Get(name string) (Preset, bool) {
	p, ok := r.presets[name]
	return p, ok
}

// Names lists every known preset: built-ins first, then configured presets
// sorted by name. This is the order error messages and List use.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.presets))
	names = append(names, builtInOrder...)
	names = append(names, r.configured...)
	return names
}

// List describes every known preset for discovery.
func (r *Registry) List() []Info {
	infos := make([]Info, 0, len(r.presets))
	for _, name := range r.Names() {
		infos = append(infos, Info{Name: name, Description: r.presets[name].Description, BuiltIn: r.builtIn[name]})
	}
	return infos
}

// Check verifies that every preset the toolset names is known. It is what
// makes an unknown preset an error on every meta-tool call, before any
// catalogue is consulted.
func (r *Registry) Check(ts Toolset) error {
	for _, name := range ts.Presets() {
		if _, ok := r.presets[name]; !ok {
			return fmt.Errorf("toolset %s names unknown preset %q; known presets: %s", ts, name, strings.Join(r.Names(), ", "))
		}
	}
	return nil
}
