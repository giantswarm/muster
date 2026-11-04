package servericon

import (
	"fmt"
	"net/url"
	"strings"
)

type brandMark struct {
	name    string
	color   string
	domains []string
	path    string
}

// brandIcons maps each brand domain to its icon's data: URI; brandURIs is
// the set of those URIs.
var (
	brandIcons = map[string]string{}
	brandURIs  = map[string]bool{}
)

func init() {
	for _, b := range brandMarks {
		uri, err := encode(mimeSVG, []byte(brandSVG(b)))
		if err != nil {
			panic(fmt.Sprintf("brand icon %s: %v", b.name, err))
		}
		for _, d := range b.domains {
			brandIcons[d] = uri
		}
		brandURIs[uri] = true
	}
}

// brandSVG draws the white mark on a rounded square in the brand's colour,
// so it reads on a light and a dark background alike.
func brandSVG(b brandMark) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">` +
		`<title>` + b.name + `</title>` +
		`<rect width="24" height="24" rx="5" fill="` + b.color + `"/>` +
		`<path transform="translate(5 5) scale(0.5833)" fill="#FFFFFF" d="` + b.path + `"/>` +
		`</svg>`
}

// BrandIcon returns the built-in icon of the brand whose domain the host of
// rawURL is, or a subdomain of: "" for any other host. Only exact domains
// count, never a word in the host.
func BrandIcon(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	for host != "" {
		if uri, ok := brandIcons[host]; ok {
			return uri
		}
		_, parent, ok := strings.Cut(host, ".")
		if !ok {
			return ""
		}
		host = parent
	}
	return ""
}

// IsBrandIcon reports whether uri is one of the built-in brand icons.
func IsBrandIcon(uri string) bool { return brandURIs[uri] }
