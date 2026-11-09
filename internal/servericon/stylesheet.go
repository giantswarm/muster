package servericon

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
)

// classProperties are the CSS properties a class rule may set; they are
// written to the elements carrying the class as presentation attributes.
var classProperties = map[string]*regexp.Regexp{
	"fill":         paintValue,
	"stroke":       paintValue,
	"fill-opacity": regexp.MustCompile(`^` + cssNumber + `%?$`),
	"stroke-width": regexp.MustCompile(`^` + cssNumber + `(px|%)?$`),
}

const cssNumber = `[+-]?(\d+(\.\d*)?|\.\d+)`

// paintValue is a colour: a keyword, a hex colour, or rgb(), rgba(), hsl()
// or hsla() over numbers.
var paintValue = regexp.MustCompile(`^([a-zA-Z]+|#[0-9a-fA-F]{3,8}|(rgba?|hsla?)\([0-9.,%\s]*\))$`)

var classSelector = regexp.MustCompile(`^\.[a-zA-Z_-][a-zA-Z0-9_-]*$`)

// classRules maps a class name to the declarations its rules set, each with
// the index of its rule so the later rule wins, as in CSS.
type classRules map[string][]classDecl

type classDecl struct {
	rule     int
	property string
	value    string
}

// stylesheetClassRules reads the <style> elements of an SVG document and
// keeps only rules whose selectors are all a single class, and of those
// only the classProperties declarations whose value passes its check. A
// stylesheet with an at-rule, an escape, a comment, a quote or unbalanced
// braces is not used at all.
func stylesheetClassRules(data []byte) classRules {
	rules := classRules{}
	n := 0
	for _, sheet := range stylesheets(data) {
		if strings.ContainsAny(sheet, `@\"'`) || strings.Contains(sheet, "/*") {
			continue
		}
		blocks, ok := ruleBlocks(sheet)
		if !ok {
			continue
		}
		for _, b := range blocks {
			n++
			addRule(rules, n, b[0], b[1])
		}
	}
	return rules
}

// stylesheets returns the text of every <style> element in the SVG
// namespace. A document that does not parse yields what was read before the
// error; sanitizeSVG refuses it anyway.
func stylesheets(data []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	var sheets []string
	var current *strings.Builder
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) || err != nil {
			return sheets
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == svgNS && t.Name.Local == "style" && current == nil {
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

// ruleBlocks splits a stylesheet into its rules' selector and declaration
// text. It fails on a nested, unopened or unclosed block.
func ruleBlocks(sheet string) ([][2]string, bool) {
	var blocks [][2]string
	rest := sheet
	for {
		open := strings.IndexByte(rest, '{')
		closing := strings.IndexByte(rest, '}')
		if open < 0 {
			return blocks, closing < 0 && strings.TrimSpace(rest) == ""
		}
		if closing < open {
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

func addRule(rules classRules, index int, selectors, body string) {
	var classes []string
	for sel := range strings.SplitSeq(selectors, ",") {
		sel = strings.TrimSpace(sel)
		if !classSelector.MatchString(sel) {
			return
		}
		classes = append(classes, sel[1:])
	}
	for decl := range strings.SplitSeq(body, ";") {
		property, value, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		property = strings.ToLower(strings.TrimSpace(property))
		value = strings.TrimSpace(value)
		check, known := classProperties[property]
		if !known || !check.MatchString(value) || !safeValue(value) {
			continue
		}
		for _, c := range classes {
			rules[c] = append(rules[c], classDecl{rule: index, property: property, value: value})
		}
	}
}

// attributes returns the presentation attributes the rules give an element
// with the class attribute value class, sorted by name.
func (r classRules) attributes(class string) map[string]string {
	var decls []classDecl
	for c := range strings.FieldsSeq(class) {
		decls = append(decls, r[c]...)
	}
	if len(decls) == 0 {
		return nil
	}
	slices.SortStableFunc(decls, func(a, b classDecl) int { return a.rule - b.rule })
	out := make(map[string]string, len(decls))
	for _, d := range decls {
		out[d.property] = d.value
	}
	return out
}
