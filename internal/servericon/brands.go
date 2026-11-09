package servericon

import (
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

type brandMark struct {
	name    string
	color   string
	domains []string
	// inCluster are the names that identify the brand on an in-cluster
	// host: a service name, a namespace or a first path segment.
	inCluster []string
	path      string
}

// brandIcons maps each brand domain to its icon's data: URI, clusterIcons
// each in-cluster name; brandURIs is the set of those URIs.
var (
	brandIcons   = map[string]string{}
	clusterIcons = map[string]string{}
	brandURIs    = map[string]bool{}
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
		for _, n := range b.inCluster {
			clusterIcons[n] = uri
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

// BrandIcon returns the built-in icon of the brand rawURL points at, or "".
// On a public host that is the brand whose domain the host is, or a
// subdomain of. On an in-cluster host (ending in .svc or .cluster.local, or
// whose last label is no public TLD) it is the brand one of whose in-cluster
// names equals the service name (first label), the namespace (second label)
// or the first path segment. Only exact domains and names count, never a
// word in the host.
func BrandIcon(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return ""
	}
	if inCluster(host) {
		return clusterBrandIcon(host, u.Path)
	}
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

// inCluster reports whether host is a cluster-internal name rather than a
// public one.
func inCluster(host string) bool {
	if strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".cluster.local") {
		return true
	}
	last := host[strings.LastIndex(host, ".")+1:]
	_, icann := publicsuffix.PublicSuffix(last)
	return !icann
}

func clusterBrandIcon(host, path string) string {
	labels := strings.Split(host, ".")
	tokens := append([]string(nil), labels[:min(2, len(labels))]...)
	for seg := range strings.SplitSeq(path, "/") {
		if seg != "" {
			tokens = append(tokens, seg)
			break
		}
	}
	for _, t := range tokens {
		if uri, ok := clusterIcons[t]; ok {
			return uri
		}
	}
	return ""
}

// IsBrandIcon reports whether uri is one of the built-in brand icons.
func IsBrandIcon(uri string) bool { return brandURIs[uri] }
