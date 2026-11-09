package servericon

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

const svgOpen = `<svg xmlns="http://www.w3.org/2000/svg">`

func TestSanitizeSVGKeepsGiantSwarmLogo(t *testing.T) {
	logo, err := os.ReadFile("testdata/giantswarm.svg")
	if err != nil {
		t.Fatal(err)
	}
	logo = bytes.TrimSpace(logo)
	got, err := sanitizeSVG(logo)
	if err != nil {
		t.Fatalf("sanitizeSVG(logo): %v", err)
	}
	if !bytes.Equal(got, logo) {
		t.Fatalf("logo changed:\n got %s\nwant %s", got, logo)
	}
}

func TestSanitizeSVGStrips(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "script", in: svgOpen + `<script>alert(1)</script><path d="M0 0h1"/></svg>`, want: svgOpen + `<path d="M0 0h1"/></svg>`},
		{name: "onload", in: `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><path d="M0 0h1" onclick="x()"/></svg>`, want: svgOpen + `<path d="M0 0h1"/></svg>`},
		{name: "foreignObject", in: svgOpen + `<foreignObject><div xmlns="http://www.w3.org/1999/xhtml"><iframe src="https://evil"/></div></foreignObject><rect width="1"/></svg>`, want: svgOpen + `<rect width="1"/></svg>`},
		{name: "javascript href", in: `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><use xlink:href="javascript:alert(1)"/><use href="javascript:alert(1)"/><rect width="1"/></svg>`, want: svgOpen + `<use/><use/><rect width="1"/></svg>`},
		{name: "external href", in: svgOpen + `<use href="https://evil.example/x.svg#a"/><use href="other.svg#a"/><rect width="1"/></svg>`, want: svgOpen + `<use/><use/><rect width="1"/></svg>`},
		{name: "local href kept as href", in: `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><defs><path id="p" d="M0 0h1"/></defs><use xlink:href="#p"/></svg>`, want: svgOpen + `<defs><path id="p" d="M0 0h1"/></defs><use href="#p"/></svg>`},
		{name: "a, image, style, animate", in: svgOpen + `<style>*{fill:url(https://evil)}</style><a href="https://evil"><rect width="1"/></a><image href="https://evil/x.png"/><set attributeName="href" to="javascript:x"/><animate attributeName="fill"/><circle r="1"/></svg>`, want: svgOpen + `<circle r="1"/></svg>`},
		{name: "external url values", in: svgOpen + `<rect width="1" fill="url(https://evil/#a)" style="fill:red" mask="URL( 'http://x' )" clip-path="url(#c)"/></svg>`, want: svgOpen + `<rect width="1" clip-path="url(#c)"/></svg>`},
		{name: "css escape", in: svgOpen + `<rect width="1" fill="u\72l(https://evil)"/></svg>`, want: svgOpen + `<rect width="1"/></svg>`},
		{name: "uppercase tags", in: svgOpen + `<SCRIPT>alert(1)</SCRIPT><Path d="M0 0"/><rect width="1"/></svg>`, want: svgOpen + `<rect width="1"/></svg>`},
		{name: "nested script", in: svgOpen + `<g><g><script>x</script><path d="M0 0h1"/></g></g></svg>`, want: svgOpen + `<g><g><path d="M0 0h1"/></g></g></svg>`},
		{name: "other namespace", in: `<svg xmlns="http://www.w3.org/2000/svg" xmlns:h="http://www.w3.org/1999/xhtml"><h:script>x</h:script><h:path d="M0 0"/><rect width="1"/></svg>`, want: svgOpen + `<rect width="1"/></svg>`},
		{name: "character references stay text", in: svgOpen + `<title>&#60;script&#62;</title><text>a &amp; b</text></svg>`, want: svgOpen + `<title>&lt;script&gt;</title><text>a &amp; b</text></svg>`},
		{name: "CDATA becomes escaped text", in: svgOpen + `<text><![CDATA[<script>alert(1)</script>]]></text></svg>`, want: svgOpen + `<text>&lt;script&gt;alert(1)&lt;/script&gt;</text></svg>`},
		{name: "CDATA script dropped", in: svgOpen + `<script><![CDATA[alert(1)]]></script><rect width="1"/></svg>`, want: svgOpen + `<rect width="1"/></svg>`},
		{name: "prolog, doctype and comments dropped", in: `<?xml version="1.0"?><!DOCTYPE svg><!-- c --><svg xmlns="http://www.w3.org/2000/svg"><!-- <script> --><rect width="1"/></svg>`, want: svgOpen + `<rect width="1"/></svg>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeSVG([]byte(tt.in))
			if err != nil {
				t.Fatalf("sanitizeSVG: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("sanitizeSVG:\n got %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestSanitizeSVGRejects(t *testing.T) {
	deep := svgOpen + strings.Repeat("<g>", maxSVGDepth) + `<rect width="1"/>` + strings.Repeat("</g>", maxSVGDepth) + "</svg>"
	for name, in := range map[string]string{
		"HTML with svg":         `<html><body><svg xmlns="http://www.w3.org/2000/svg"><rect width="1"/></svg><script>x</script></body></html>`,
		"svg without namespace": `<svg><rect width="1"/></svg>`,
		"uppercase root":        `<SVG xmlns="http://www.w3.org/2000/svg"><rect width="1"/></SVG>`,
		"not well-formed":       svgOpen + `<rect width="1"></svg>`,
		"entity expansion":      `<!DOCTYPE svg [<!ENTITY x "<script>alert(1)</script>">]>` + svgOpen + `<text>&x;</text></svg>`,
		"external entity":       `<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + svgOpen + `<text>&x;</text></svg>`,
		"nothing visible":       svgOpen + `<script>alert(1)</script><title>t</title></svg>`,
		"empty":                 "",
		"two roots":             svgOpen + `<rect width="1"/></svg>` + svgOpen + `<rect width="1"/></svg>`,
		"too deep":              deep,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := sanitizeSVG([]byte(in)); !errors.Is(err, errInvalid) {
				t.Fatalf("sanitizeSVG err = %v, want errInvalid", err)
			}
		})
	}
}

func TestBrandIcon(t *testing.T) {
	for rawURL, want := range map[string]string{
		"https://mcp.atlassian.com/v1/sse":        "Atlassian",
		"https://foo.atlassian.net/":              "Atlassian",
		"https://ATLASSIAN.NET./x":                "Atlassian",
		"https://evilatlassian.com/":              "",
		"https://atlassian.com.evil.example/":     "",
		"https://atlassian.example/":              "",
		"https://mcp.pagerduty.com/mcp":           "PagerDuty",
		"https://api.githubcopilot.com/mcp/":      "GitHub",
		"https://github.com":                      "GitHub",
		"https://gitlab.com/api/v4/mcp":           "GitLab",
		"https://drive.google.com/":               "Google Drive",
		"https://www.googleapis.com/drive/v3":     "",
		"https://google.com/":                     "",
		"https://x.grafana.net/":                  "Grafana",
		"https://mcp.notion.com/mcp":              "Notion",
		"https://mcp.linear.app/sse":              "Linear",
		"https://mcp.sentry.io/":                  "Sentry",
		"https://mcp.datadoghq.eu/":               "Datadog",
		"https://mcp.miro.com/":                   "Miro",
		"https://bedrock.us-east-1.amazonaws.com": "",
		"https://kubernetes.example/":             "",
		"https://github-mcp.internal:8443/":       "",
		"not a url":                               "",
		"":                                        "",
	} {
		got := BrandIcon(rawURL)
		if want == "" {
			if got != "" {
				t.Errorf("BrandIcon(%q) = an icon, want none", rawURL)
			}
			continue
		}
		if got == "" || !strings.Contains(decodeIcon(t, got), "<title>"+want+"</title>") {
			t.Errorf("BrandIcon(%q) is not the %s icon", rawURL, want)
		}
		if !IsBrandIcon(got) {
			t.Errorf("IsBrandIcon(BrandIcon(%q)) = false", rawURL)
		}
	}
}

// TestBrandIconsMatchCRD: every brand icon is sanitised SVG that satisfies
// the CRD's pattern and length.
func TestBrandIconsMatchCRD(t *testing.T) {
	pattern := crdIconPattern(t)
	if len(brandMarks) != len(brandURIs) {
		t.Fatalf("%d brands, %d distinct icons", len(brandMarks), len(brandURIs))
	}
	for uri := range brandURIs {
		if !pattern.MatchString(uri) || len(uri) > MaxEncodedLen {
			t.Errorf("brand icon %.60q does not satisfy the CRD", uri)
		}
		if again, err := FromDataURI(uri); err != nil || again != uri {
			t.Errorf("brand icon is not stable through the sanitizer: %v", err)
		}
	}
}

func decodeIcon(t *testing.T, uri string) string {
	t.Helper()
	data, err := base64Payload(uri)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func base64Payload(uri string) ([]byte, error) {
	_, payload, _ := strings.Cut(uri, ",")
	return base64.StdEncoding.DecodeString(payload)
}

func TestSanitizeSVGInlinesClassRules(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "class fill",
			in:   svgOpen + `<style>.st0{fill:#fff}</style><path class="st0" d="M0 0h1"/></svg>`,
			want: svgOpen + `<path class="st0" d="M0 0h1" fill="#fff"/></svg>`,
		},
		{
			name: "all four properties, CDATA, whitespace",
			in:   svgOpen + "<defs><style type=\"text/css\"><![CDATA[\n  .a {\n    fill: rgb(1, 2, 3);\n    stroke: none;\n    fill-opacity: .5;\n    stroke-width: 2px;\n  }\n]]></style></defs><rect class=\"a\" width=\"1\"/></svg>",
			want: svgOpen + `<defs/><rect class="a" width="1" fill="rgb(1, 2, 3)" fill-opacity=".5" stroke="none" stroke-width="2px"/></svg>`,
		},
		{
			name: "stylesheet after the elements it styles",
			in:   svgOpen + `<path class="b" d="M0 0h1"/><style>.b{fill:red}</style></svg>`,
			want: svgOpen + `<path class="b" d="M0 0h1" fill="red"/></svg>`,
		},
		{
			name: "class rule wins over a presentation attribute",
			in:   svgOpen + `<style>.b{fill:#0e2151}</style><path fill="#000" class="b" d="M0 0h1"/></svg>`,
			want: svgOpen + `<path class="b" d="M0 0h1" fill="#0e2151"/></svg>`,
		},
		{
			name: "later rule wins, several classes, selector group",
			in:   svgOpen + `<style>.a,.c{fill:red;stroke:blue}.b{fill:green}</style><path class="b  a" d="M0 0h1"/><path class="c" d="M0 0h1"/></svg>`,
			want: svgOpen + `<path class="b  a" d="M0 0h1" fill="green" stroke="blue"/><path class="c" d="M0 0h1" fill="red" stroke="blue"/></svg>`,
		},
		{
			name: "class on a group",
			in:   svgOpen + `<style>.g{fill:#abc}</style><g class="g"><path d="M0 0h1"/></g></svg>`,
			want: svgOpen + `<g class="g" fill="#abc"><path d="M0 0h1"/></g></svg>`,
		},
		{
			name: "presentation properties are inlined",
			in:   svgOpen + `<style>.a{fill:url(#g);fill-rule:evenodd;opacity:.5;clip-path:url(#c);stroke-miterlimit:10;display:none}</style><path class="a" d="M0 0h1"/></svg>`,
			want: svgOpen + `<path class="a" d="M0 0h1" clip-path="url(#c)" display="none" fill="url(#g)" fill-rule="evenodd" opacity=".5" stroke-miterlimit="10"/></svg>`,
		},
		{
			name: "other properties are not inlined",
			in:   svgOpen + `<style>.a{fill:red;transform:rotate(9deg);x:1;d:none;filter:blur(1px);mix-blend-mode:multiply;style:x;href:#p}</style><path class="a" d="M0 0h1"/></svg>`,
			want: svgOpen + `<path class="a" d="M0 0h1" fill="red"/></svg>`,
		},
		{
			name: "values get the attribute check, important is dropped",
			in: svgOpen + `<style>.a{fill:url(https://evil/x);stroke:URL( x );stroke-width:}.b{fill:url(#g) !important;stroke:red!IMPORTANT}</style>` +
				`<path class="a" d="M0 0h1"/><path class="b" d="M0 0h1"/></svg>`,
			want: svgOpen + `<path class="a" d="M0 0h1"/><path class="b" d="M0 0h1" fill="url(#g)" stroke="red"/></svg>`,
		},
		{
			name: "a refused value leaves the earlier rule in place",
			in:   svgOpen + `<style>.a{fill:red}.b{fill:url(https://evil/x)}</style><path class="a b" d="M0 0h1"/></svg>`,
			want: svgOpen + `<path class="a b" d="M0 0h1" fill="red"/></svg>`,
		},
		{
			name: "rules whose selector is not a plain class are not applied",
			in: svgOpen + `<style>path{fill:red}*{fill:red}#p{fill:red}.a .b{fill:red}.a.b{fill:red}.a>.b{fill:red}.b:hover{fill:red}[class=b]{fill:red}.a,path{fill:red}.b{stroke:blue}</style>` +
				`<g class="a"><path id="p" class="a b" d="M0 0h1"/></g></svg>`,
			want: svgOpen + `<g class="a"><path id="p" class="a b" d="M0 0h1" stroke="blue"/></g></svg>`,
		},
		{
			name: "at-rules and conditional stylesheets are skipped",
			in: svgOpen + `<style>@import url(https://evil/x.css);.a{fill:red}@media (prefers-color-scheme: dark){.a{fill:#fff}.b{fill:#000}}.a{stroke:blue}</style>` +
				`<style media="(prefers-color-scheme: dark)">.a{stroke:#fff}</style><style type="text/x-other">.a{stroke:#000}</style><style media="all">.a{stroke-width:2}</style>` +
				`<path class="a" d="M0 0h1"/></svg>`,
			want: svgOpen + `<path class="a" d="M0 0h1" fill="red" stroke="blue" stroke-width="2"/></svg>`,
		},
		{
			name: "unclosed at-rule refuses the whole stylesheet",
			in:   svgOpen + `<style>.a{fill:red}@media screen{.a{fill:#fff}</style><path class="a" d="M0 0h1"/></svg>`,
			want: svgOpen + `<path class="a" d="M0 0h1"/></svg>`,
		},
		{
			name: "escapes, comments and quotes refuse the whole stylesheet",
			in: svgOpen + `<style>.a{fill:red}.b{fill:\72 ed}</style><style>.a{fill:red}/* x */</style><style>.a{fill:red}.b{fill:"red"}</style><style>.a{fill:red}}</style>` +
				`<path class="a" d="M0 0h1"/></svg>`,
			want: svgOpen + `<path class="a" d="M0 0h1"/></svg>`,
		},
		{
			name: "style outside the SVG namespace",
			in:   `<svg xmlns="http://www.w3.org/2000/svg" xmlns:h="http://www.w3.org/1999/xhtml"><h:style>.a{fill:red}</h:style><path class="a" d="M0 0h1"/></svg>`,
			want: svgOpen + `<path class="a" d="M0 0h1"/></svg>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeSVG([]byte(tt.in))
			if err != nil {
				t.Fatalf("sanitizeSVG: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("sanitizeSVG:\n got %s\nwant %s", got, tt.want)
			}
		})
	}
}

// TestFromDataURIKeepsWebAssetsIconColours uses giantswarm/web-assets
// assets/app-icons/muster/1/light.svg, which colours its paths only through
// a <style> block.
func TestFromDataURIKeepsWebAssetsIconColours(t *testing.T) {
	icon, err := os.ReadFile("testdata/muster-light.svg")
	if err != nil {
		t.Fatal(err)
	}
	uri, err := FromDataURI("data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(icon))
	if err != nil {
		t.Fatalf("FromDataURI: %v", err)
	}
	var fills []string
	dec := xml.NewDecoder(strings.NewReader(decodeIcon(t, uri)))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if el, ok := tok.(xml.StartElement); ok && el.Name.Local == "path" {
			fill := ""
			for _, a := range el.Attr {
				if a.Name.Local == "fill" {
					fill = a.Value
				}
			}
			fills = append(fills, fill)
		}
	}
	if want := []string{"#0e2151", "#fff"}; !slices.Equal(fills, want) {
		t.Fatalf("path fills = %q, want %q", fills, want)
	}
}

// TestSanitizeSVGClassRulesAtSizeCap feeds the largest SVG FromDataURI
// accepts, split between rules for one class and repetitions of that class,
// and elements carrying it: the work per element must not grow with the
// number of rules.
func TestSanitizeSVGClassRulesAtSizeCap(t *testing.T) {
	quarter := MaxEncodedLen * 2 / 4
	sheet := strings.Repeat(".a{fill:red}", quarter/len(".a{fill:red}"))
	class := strings.Repeat("a ", quarter/2)
	paths := strings.Repeat(`<g class="a"/>`, quarter/len(`<g class="a"/>`))
	in := svgOpen + "<style>" + sheet + `</style><path class="` + class + `" d="M0 0h1"/>` + paths + `</svg>`
	got, err := sanitizeSVG([]byte(in))
	if err != nil {
		t.Fatalf("sanitizeSVG: %v", err)
	}
	if want := `<path class="` + class + `" d="M0 0h1" fill="red"/>`; !strings.Contains(string(got), want) {
		t.Fatalf("sanitizeSVG: path not styled")
	}
}
