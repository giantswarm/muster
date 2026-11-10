package servericon

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

const (
	svgNS   = "http://www.w3.org/2000/svg"
	xlinkNS = "http://www.w3.org/1999/xlink"

	// maxSVGDepth bounds element nesting; no icon needs more.
	maxSVGDepth = 64
)

// svgElements are the SVG elements a sanitised icon keeps; the value says
// whether the element draws something.
var svgElements = map[string]bool{
	"svg": false, "g": false, "defs": false, "symbol": false,
	"linearGradient": false, "radialGradient": false, "stop": false,
	"clipPath": false, "mask": false, "title": false, "desc": false,
	"path": true, "rect": true, "circle": true, "ellipse": true, "line": true,
	"polyline": true, "polygon": true, "use": true, "text": true, "tspan": true,
}

// textElements are the elements whose character data is kept.
var textElements = map[string]bool{"title": true, "desc": true, "text": true, "tspan": true}

// svgAttributes are the attributes a sanitised icon keeps, unprefixed. href
// (and xlink:href, written as href) is kept only as a local #fragment.
var svgAttributes = toSet(
	"id", "class", "version", "width", "height", "viewBox", "preserveAspectRatio",
	"x", "y", "x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry", "fx", "fy", "fr",
	"dx", "dy", "d", "points", "pathLength", "transform", "href",
	"fill", "fill-opacity", "fill-rule", "stroke", "stroke-width", "stroke-opacity",
	"stroke-linecap", "stroke-linejoin", "stroke-miterlimit", "stroke-dasharray",
	"stroke-dashoffset", "opacity", "color", "display", "visibility", "overflow",
	"clip-path", "clip-rule", "clipPathUnits", "mask", "maskUnits", "maskContentUnits",
	"offset", "stop-color", "stop-opacity", "gradientUnits", "gradientTransform",
	"spreadMethod", "vector-effect", "shape-rendering", "paint-order",
	"font-family", "font-size", "font-weight", "font-style", "text-anchor",
	"dominant-baseline", "letter-spacing", "word-spacing",
)

func toSet(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

var errSVG = fmt.Errorf("%w: SVG", errInvalid)

// sanitizeSVG parses data as XML and rewrites it keeping only allowlisted
// SVG elements and attributes: no scripts, event handlers, foreign content,
// style elements or attributes, animation, links or external references.
// The presentation declarations of its simple class rules
// (stylesheetClassRules) and of each style attribute become presentation
// attributes of the elements they apply to, overriding the element's own
// attribute of that name as CSS would. It fails when data is not
// well-formed XML with an <svg> root in the SVG namespace, or when nothing
// visible is left.
func sanitizeSVG(data []byte) ([]byte, error) {
	rules := stylesheetClassRules(data)
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true

	var out bytes.Buffer
	var stack []string   // kept elements open in the output
	skip := 0            // depth inside a dropped element
	depth := 0           // depth in the input
	pendingOpen := false // a start tag written without its closing '>'
	rootSeen, visible := false, false

	closePending := func() {
		if pendingOpen {
			out.WriteByte('>')
			pendingOpen = false
		}
	}

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errSVG, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > maxSVGDepth {
				return nil, fmt.Errorf("%w: nested deeper than %d", errSVG, maxSVGDepth)
			}
			if !rootSeen {
				if t.Name.Space != svgNS || t.Name.Local != "svg" {
					return nil, fmt.Errorf("%w: root is not <svg> in the SVG namespace", errSVG)
				}
				rootSeen = true
			} else if depth == 1 {
				return nil, fmt.Errorf("%w: more than one root element", errSVG)
			}
			if skip > 0 {
				skip++
				continue
			}
			draws, ok := svgElements[t.Name.Local]
			if t.Name.Space != svgNS || !ok {
				skip = 1
				continue
			}
			closePending()
			visible = visible || draws
			out.WriteByte('<')
			out.WriteString(t.Name.Local)
			if len(stack) == 0 {
				out.WriteString(` xmlns="` + svgNS + `"`)
			}
			styled := rules.attributes(t.Attr)
			for _, a := range t.Attr {
				if name, value, keep := sanitizeAttr(a); keep && styled[name] == "" {
					writeAttr(&out, name, value)
				}
			}
			for _, name := range slices.Sorted(maps.Keys(styled)) {
				writeAttr(&out, name, styled[name])
			}
			pendingOpen = true
			stack = append(stack, t.Name.Local)
		case xml.EndElement:
			depth--
			if skip > 0 {
				skip--
				continue
			}
			name := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if pendingOpen {
				out.WriteString("/>")
				pendingOpen = false
			} else {
				out.WriteString("</" + name + ">")
			}
		case xml.CharData:
			if skip > 0 || len(stack) == 0 || !textElements[stack[len(stack)-1]] {
				continue
			}
			closePending()
			_ = xml.EscapeText(&out, t)
		}
		// Comments, processing instructions and directives (DOCTYPE) are
		// dropped. The decoder expands only XML's predefined entities and
		// character references and fails on any other entity.
	}
	if !rootSeen {
		return nil, fmt.Errorf("%w: no root element", errSVG)
	}
	if !visible {
		return nil, fmt.Errorf("%w: nothing visible", errSVG)
	}
	return out.Bytes(), nil
}

func writeAttr(out *bytes.Buffer, name, value string) {
	out.WriteString(" " + name + `="`)
	_ = xml.EscapeText(out, []byte(value))
	out.WriteByte('"')
}

// sanitizeAttr returns the attribute as written to the output, and whether
// it is kept at all.
func sanitizeAttr(a xml.Attr) (string, string, bool) {
	name := a.Name.Local
	switch a.Name.Space {
	case "":
	case xlinkNS:
		if name != "href" {
			return "", "", false
		}
	default:
		return "", "", false
	}
	if !svgAttributes[name] {
		return "", "", false
	}
	value := a.Value
	if name == "href" {
		v := strings.TrimSpace(value)
		if !strings.HasPrefix(v, "#") || len(v) == 1 || strings.ContainsAny(v, ":/\\ \t\r\n") {
			return "", "", false
		}
		return name, v, true
	}
	if !safeValue(value) {
		return "", "", false
	}
	return name, value, true
}

// safeValue rejects a value that references anything outside the icon:
// every url(...) must be a local url(#...), and CSS escapes, which could
// hide a reference, are not allowed.
func safeValue(value string) bool {
	if strings.ContainsRune(value, '\\') {
		return false
	}
	lower := strings.ToLower(value)
	for {
		i := strings.Index(lower, "url(")
		if i < 0 {
			return true
		}
		rest := strings.TrimLeft(lower[i+len("url("):], " \t\r\n\f'\"")
		if !strings.HasPrefix(rest, "#") {
			return false
		}
		lower = rest
	}
}
