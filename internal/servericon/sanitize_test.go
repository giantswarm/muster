package servericon

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
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
