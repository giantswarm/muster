package servericon

import (
	"bytes"
	"encoding/xml"
	"math"
	"regexp"
	"strings"
)

// presentationProperties are the CSS properties that are also SVG
// presentation attributes with the same value syntax. A declaration setting
// one becomes that attribute, checked as sanitizeAttr checks the attribute.
var presentationProperties = toSet(
	"fill", "fill-opacity", "fill-rule", "stroke", "stroke-width", "stroke-opacity",
	"stroke-linecap", "stroke-linejoin", "stroke-miterlimit", "stroke-dasharray",
	"stroke-dashoffset", "opacity", "color", "display", "visibility", "overflow",
	"clip-path", "clip-rule", "mask", "stop-color", "stop-opacity", "vector-effect",
	"shape-rendering", "paint-order", "font-family", "font-size", "font-weight",
	"font-style", "text-anchor", "dominant-baseline", "letter-spacing", "word-spacing",
)

var classSelector = regexp.MustCompile(`^\.[a-zA-Z_-][a-zA-Z0-9_-]*$`)

// declaration is a presentation declaration whose value passes sanitizeAttr.
type declaration struct {
	property  string
	value     string
	important bool
}

// cascaded is a declaration with its position in the cascade: rules are
// numbered in document order, and a style attribute comes after all of them.
// All class selectors have the same specificity, so order decides between
// declarations of the same importance.
type cascaded struct {
	declaration
	order int
}

const styleAttributeOrder = math.MaxInt

func (d cascaded) overrides(other cascaded) bool {
	if d.important != other.important {
		return d.important
	}
	return d.order > other.order
}

// classRules maps a class name to the declaration that wins for each
// property among the rules selecting that class.
type classRules map[string]map[string]cascaded

// stylesheetClassRules reads the <style> elements of an SVG document that
// apply unconditionally, and keeps the declarations of the rules whose
// selectors are all a single class. At-rules (@import, @media, ...) are
// skipped. A stylesheet cleanCSS refuses or with unbalanced braces is not
// used at all.
func stylesheetClassRules(data []byte) classRules {
	rules := classRules{}
	n := 0
	for _, sheet := range stylesheets(data) {
		sheet, ok := cleanCSS(sheet)
		if !ok {
			continue
		}
		blocks, ok := ruleBlocks(sheet)
		if !ok {
			continue
		}
		for _, b := range blocks {
			n++
			rules.add(n, b[0], b[1])
		}
	}
	return rules
}

// stylesheets returns the text of every <style> element in the SVG
// namespace that a browser applies whatever the medium: no type other than
// CSS, no media query. A document that does not parse yields what was read
// before the error; sanitizeSVG refuses it anyway.
func stylesheets(data []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	var sheets []string
	var current *strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return sheets
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == svgNS && t.Name.Local == "style" && current == nil && unconditional(t.Attr) {
				current = &strings.Builder{}
			}
		case xml.EndElement:
			if t.Name.Space == svgNS && t.Name.Local == "style" && current != nil {
				sheets = append(sheets, current.String())
				current = nil
			}
		case xml.CharData:
			if current != nil {
				current.Write(t)
			}
		}
	}
}

// unconditional reports whether a <style> element with these attributes
// applies in every context.
func unconditional(attrs []xml.Attr) bool {
	for _, a := range attrs {
		if a.Name.Space != "" {
			continue
		}
		v := strings.ToLower(strings.TrimSpace(a.Value))
		switch a.Name.Local {
		case "type":
			if v != "" && v != "text/css" {
				return false
			}
		case "media":
			if v != "" && v != "all" && v != "screen" {
				return false
			}
		}
	}
	return true
}

// cleanCSS returns CSS text with each comment replaced by a space. It
// refuses text that splitting on braces and semicolons would misread: an
// escape, an unterminated comment or string, or a string holding a brace, a
// semicolon or a newline.
func cleanCSS(css string) (string, bool) {
	if strings.ContainsRune(css, '\\') {
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(css); {
		switch {
		case strings.HasPrefix(css[i:], "/*"):
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				return "", false
			}
			b.WriteByte(' ')
			i += 2 + end + 2
		case css[i] == '"' || css[i] == '\'':
			end := strings.IndexByte(css[i+1:], css[i])
			if end < 0 {
				return "", false
			}
			str := css[i : i+end+2]
			if strings.ContainsAny(str, "{};\n\r\f") {
				return "", false
			}
			b.WriteString(str)
			i += len(str)
		default:
			b.WriteByte(css[i])
			i++
		}
	}
	return b.String(), true
}

// ruleBlocks splits a stylesheet into its rules' selector and declaration
// text, skipping at-rules. It fails on a nested, unopened or unclosed block.
func ruleBlocks(sheet string) ([][2]string, bool) {
	var blocks [][2]string
	rest := sheet
	for {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return blocks, true
		}
		open := strings.IndexByte(rest, '{')
		if rest[0] == '@' {
			if semi := strings.IndexByte(rest, ';'); semi >= 0 && (open < 0 || semi < open) {
				rest = rest[semi+1:]
				continue
			}
			end, ok := blockEnd(rest, open)
			if !ok {
				return nil, false
			}
			rest = rest[end+1:]
			continue
		}
		closing := strings.IndexByte(rest, '}')
		if open < 0 || closing < open {
			return nil, false
		}
		body := rest[open+1 : closing]
		if strings.ContainsRune(body, '{') {
			return nil, false
		}
		blocks = append(blocks, [2]string{rest[:open], body})
		rest = rest[closing+1:]
	}
}

// blockEnd returns the index of the '}' closing the block opened at open.
func blockEnd(s string, open int) (int, bool) {
	if open < 0 {
		return 0, false
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// add records the declarations of a rule whose selectors are all a single
// class; a rule with any other selector is not applied.
func (r classRules) add(order int, selectors, block string) {
	var classes []string
	for sel := range strings.SplitSeq(selectors, ",") {
		sel = strings.TrimSpace(sel)
		if !classSelector.MatchString(sel) {
			return
		}
		classes = append(classes, sel[1:])
	}
	for _, d := range declarations(block) {
		decl := cascaded{declaration: d, order: order}
		for _, c := range classes {
			if r[c] == nil {
				r[c] = map[string]cascaded{}
			}
			if prev, ok := r[c][d.property]; !ok || decl.overrides(prev) {
				r[c][d.property] = decl
			}
		}
	}
}

// declarations returns the presentationProperties declarations of a
// declaration block whose value passes sanitizeAttr. A declaration with a
// priority other than !important is invalid CSS and left out.
func declarations(block string) []declaration {
	var out []declaration
	for decl := range strings.SplitSeq(block, ";") {
		property, value, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		property = strings.ToLower(strings.TrimSpace(property))
		value, priority, prioritised := strings.Cut(value, "!")
		value = strings.TrimSpace(value)
		important := strings.EqualFold(strings.TrimSpace(priority), "important")
		if !presentationProperties[property] || value == "" || prioritised && !important {
			continue
		}
		if _, _, keep := sanitizeAttr(xml.Attr{Name: xml.Name{Local: property}, Value: value}); !keep {
			continue
		}
		out = append(out, declaration{property: property, value: value, important: important})
	}
	return out
}

// attributes returns the presentation attributes an element gets from the
// rules selecting its classes and from its style attribute.
func (r classRules) attributes(attrs []xml.Attr) map[string]string {
	var winners map[string]cascaded
	pick := func(d cascaded) {
		if winners == nil {
			winners = map[string]cascaded{}
		}
		if w, ok := winners[d.property]; !ok || d.overrides(w) {
			winners[d.property] = d
		}
	}
	for _, a := range attrs {
		if a.Name.Space != "" {
			continue
		}
		switch a.Name.Local {
		case "class":
			for c := range strings.FieldsSeq(a.Value) {
				for _, d := range r[c] {
					pick(d)
				}
			}
		case "style":
			if block, ok := cleanCSS(a.Value); ok {
				for _, d := range declarations(block) {
					pick(cascaded{declaration: d, order: styleAttributeOrder})
				}
			}
		}
	}
	if winners == nil {
		return nil
	}
	out := make(map[string]string, len(winners))
	for property, d := range winners {
		out[property] = d.value
	}
	return out
}
