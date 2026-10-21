package toolset

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolGroup is a set of family members and their contribution of one tool,
// Servers[i] contributing Tools[i].
type ToolGroup struct {
	Servers []string
	Tools   []mcp.Tool
}

// SplitFamilyTool splits the members contributing one family tool by the
// arguments their input schema accepts (InputSchemaFingerprint). The largest schema group keeps the family name (a tie goes to the
// group of the alphabetically first server, so the choice is stable across
// listings); rest holds the members of every other schema group, which fall
// back to per-server names. Descriptions take no part: members whose tool
// differs only in its own or its arguments' descriptions are one group.
func SplitFamilyTool(servers []string, tools []mcp.Tool) (group, rest ToolGroup) {
	type schemaGroup struct {
		ToolGroup
		first string
	}
	var groups []*schemaGroup
	bySchema := make(map[string]*schemaGroup)
	for i, sn := range servers {
		fp := InputSchemaFingerprint(tools[i])
		g, ok := bySchema[fp]
		if !ok {
			g = &schemaGroup{first: sn}
			bySchema[fp] = g
			groups = append(groups, g)
		}
		g.Servers = append(g.Servers, sn)
		g.Tools = append(g.Tools, tools[i])
		g.first = min(g.first, sn)
	}
	best := slices.MaxFunc(groups, func(a, b *schemaGroup) int {
		if c := len(a.Servers) - len(b.Servers); c != 0 {
			return c
		}
		return strings.Compare(b.first, a.first)
	})
	for _, g := range groups {
		if g == best {
			continue
		}
		rest.Servers = append(rest.Servers, g.Servers...)
		rest.Tools = append(rest.Tools, g.Tools...)
	}
	return best.ToolGroup, rest
}

// InputSchemaFingerprint returns a canonical JSON encoding of the arguments
// the tool's input schema accepts, raw or structured: the annotation keywords
// (description, title, examples, $comment) are dropped at every level, so two
// versions of a server that only reword a tool's argument descriptions
// compare equal, and key order does not matter.
func InputSchemaFingerprint(tool mcp.Tool) string {
	raw := []byte(tool.RawInputSchema)
	if len(raw) == 0 {
		var err error
		if raw, err = json.Marshal(tool.InputSchema); err != nil {
			return ""
		}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	canonical, err := json.Marshal(stripSchemaAnnotations(v))
	if err != nil {
		return string(raw)
	}
	return string(canonical)
}

// schemaAnnotations are the JSON Schema keywords that describe an argument
// without changing which values it accepts.
var schemaAnnotations = map[string]bool{schemaDescriptionKeyword: true, "title": true, "examples": true, "$comment": true}

// schemaDescriptionKeyword is the JSON Schema keyword of an argument's
// description.
const schemaDescriptionKeyword = "description"

// schemaNameMaps are the JSON Schema keywords whose value maps names (of
// properties or definitions) to schemas: their keys are names, never
// keywords, so a property called "description" is kept.
var schemaNameMaps = map[string]bool{"properties": true, "patternProperties": true, "$defs": true, "definitions": true, "dependentSchemas": true}

// schemaValues are the JSON Schema keywords whose value is data, not a
// schema, and is compared as it is.
var schemaValues = map[string]bool{"enum": true, "const": true, "default": true}

// stripSchemaAnnotations returns the schema node without its annotation
// keywords and with its required names in canonical form, recursing into
// every subschema.
func stripSchemaAnnotations(node any) any {
	switch n := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, v := range n {
			switch {
			case schemaAnnotations[k]:
			case k == "required":
				// An empty list requires nothing, as an absent one does, and
				// the order of the names does not matter.
				if names, ok := v.([]any); ok && len(names) > 0 {
					sorted := slices.Clone(names)
					slices.SortFunc(sorted, func(a, b any) int {
						as, _ := a.(string)
						bs, _ := b.(string)
						return strings.Compare(as, bs)
					})
					out[k] = sorted
				} else if !ok {
					out[k] = v
				}
			case schemaValues[k]:
				out[k] = v
			case schemaNameMaps[k]:
				if names, ok := v.(map[string]any); ok {
					stripped := make(map[string]any, len(names))
					for name, sub := range names {
						stripped[name] = stripSchemaAnnotations(sub)
					}
					out[k] = stripped
				} else {
					out[k] = v
				}
			default:
				out[k] = stripSchemaAnnotations(v)
			}
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, v := range n {
			out[i] = stripSchemaAnnotations(v)
		}
		return out
	default:
		return node
	}
}

// CommonDescription returns the description most of the tools carry. A tie
// goes to the longest description, as a newer member tends to describe a tool
// more fully, then to the alphabetically first, so the choice is stable.
func CommonDescription(tools []mcp.Tool) string {
	counts := make(map[string]int, len(tools))
	for _, t := range tools {
		counts[t.Description]++
	}
	var best string
	bestCount := 0
	for d, n := range counts {
		switch {
		case n > bestCount,
			n == bestCount && len(d) > len(best),
			n == bestCount && len(d) == len(best) && d < best:
			best, bestCount = d, n
		}
	}
	return best
}
