package servericon

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 32)...)
	jpegBytes = append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte{0}, 32)...)
	webpBytes = append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), bytes.Repeat([]byte{0}, 32)...)
	svgBytes  = []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"/>`)
)

func dataURI(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func TestSelect(t *testing.T) {
	tests := []struct {
		name  string
		icons []mcp.Icon
		want  string
		ok    bool
	}{
		{name: "none", ok: false},
		{name: "light SVG over raster", ok: true, want: "b.svg", icons: []mcp.Icon{
			{Src: "a.png", MIMEType: "image/png", Sizes: []string{"128x128"}},
			{Src: "b.svg"},
		}},
		{name: "no theme over dark", ok: true, want: "light.png", icons: []mcp.Icon{
			{Src: "dark.svg", Theme: mcp.IconThemeDark},
			{Src: "light.png", MIMEType: "image/png"},
		}},
		{name: "dark when nothing else", ok: true, want: "dark.png", icons: []mcp.Icon{
			{Src: "dark.png", Theme: mcp.IconThemeDark},
		}},
		{name: "largest raster at or under 256", ok: true, want: "256.png", icons: []mcp.Icon{
			{Src: "48.png", Sizes: []string{"48x48"}},
			{Src: "512.png", Sizes: []string{"512x512"}},
			{Src: "256.png", Sizes: []string{"256x256"}},
		}},
		{name: "smallest above 256 when none under", ok: true, want: "300.png", icons: []mcp.Icon{
			{Src: "1024.png", Sizes: []string{"1024x1024"}},
			{Src: "300.png", Sizes: []string{"300x300"}},
		}},
		{name: "disallowed MIME skipped", ok: true, want: "ok.png", icons: []mcp.Icon{
			{Src: "x.gif", MIMEType: "image/gif", Sizes: []string{"64x64"}},
			{Src: "ok.png", MIMEType: "image/png", Sizes: []string{"16x16"}},
		}},
		{name: "svg data URI", ok: true, want: dataURI(mimeSVG, svgBytes), icons: []mcp.Icon{
			{Src: "a.png", Sizes: []string{"64x64"}},
			{Src: dataURI(mimeSVG, svgBytes)},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Select(tt.icons)
			if ok != tt.ok || got.Src != tt.want {
				t.Fatalf("Select = %q, %v; want %q, %v", got.Src, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestFromDataURI(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    string
		invalid bool
	}{
		{name: "png", src: dataURI("image/png", pngBytes), want: dataURI("image/png", pngBytes)},
		{name: "jpeg", src: dataURI("image/jpeg", jpegBytes), want: dataURI("image/jpeg", jpegBytes)},
		{name: "webp", src: dataURI("image/webp", webpBytes), want: dataURI("image/webp", webpBytes)},
		{name: "svg with charset", src: "data:image/svg+xml;charset=utf-8;base64," + base64.StdEncoding.EncodeToString(svgBytes), want: dataURI(mimeSVG, svgBytes)},
		{name: "percent-encoded svg", src: "data:image/svg+xml," + strings.ReplaceAll(string(svgBytes), " ", "%20"), want: dataURI(mimeSVG, svgBytes)},
		{name: "gif not allowed", src: dataURI("image/gif", []byte("GIF89a....")), invalid: true},
		{name: "html not allowed", src: dataURI("text/html", []byte("<html></html>")), invalid: true},
		{name: "declared png is svg", src: dataURI("image/png", svgBytes), invalid: true},
		{name: "declared svg is html", src: dataURI(mimeSVG, []byte("<html><script/></html>")), invalid: true},
		{name: "bad base64", src: "data:image/png;base64,!!!", invalid: true},
		{name: "no payload", src: "data:image/png;base64", invalid: true},
		{name: "not data", src: "https://example.com/a.png", invalid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FromDataURI(tt.src)
			if tt.invalid {
				if !errors.Is(err, errInvalid) {
					t.Fatalf("FromDataURI err = %v, want errInvalid", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("FromDataURI = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestSizeCap(t *testing.T) {
	prefix := len("data:image/png;base64,")
	fits := (MaxEncodedLen - prefix) / 4 * 3
	at := append(append([]byte{}, pngBytes...), make([]byte, fits-len(pngBytes))...)
	if uri, err := encode("image/png", at); err != nil || len(uri) > MaxEncodedLen {
		t.Fatalf("encode at the cap: len %d, err %v", len(uri), err)
	}
	over := append(at, make([]byte, 3)...)
	if _, err := encode("image/png", over); !errors.Is(err, errInvalid) {
		t.Fatalf("encode over the cap: err %v, want errInvalid", err)
	}
	if _, err := FromDataURI(dataURI("image/png", over)); !errors.Is(err, errInvalid) {
		t.Fatalf("FromDataURI over the cap: err %v, want errInvalid", err)
	}
}

// crdIconPattern reads the pattern the MCPServer CRD enforces on
// status.icon, so the test fails when the encoder and the CRD drift apart.
func crdIconPattern(t *testing.T) *regexp.Regexp {
	t.Helper()
	crd, err := os.ReadFile("../../helm/muster-crds/files/crds/muster.giantswarm.io_mcpservers.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(crd)) {
		if pattern, ok := strings.CutPrefix(strings.TrimSpace(line), "pattern: ^data:image/"); ok {
			return regexp.MustCompile("^data:image/" + pattern)
		}
	}
	t.Fatal("status.icon pattern not found in the MCPServer CRD")
	return nil
}

func TestOutputMatchesCRD(t *testing.T) {
	pattern := crdIconPattern(t)
	var inputs []string
	// Every padding length: 0, 1 and 2 trailing "=".
	for extra := range 6 {
		data := append(append([]byte{}, pngBytes...), make([]byte, extra)...)
		inputs = append(inputs, dataURI("image/png", data))
		inputs = append(inputs, "data:image/png;base64,"+base64.RawStdEncoding.EncodeToString(data))
	}
	svgWithSpaces := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0h1"/></svg>`)
	inputs = append(inputs,
		dataURI("image/jpeg", jpegBytes),
		dataURI("image/webp", webpBytes),
		"data:image/svg+xml;charset=utf-8;base64,"+base64.StdEncoding.EncodeToString(svgBytes),
		"data:image/svg+xml,"+strings.ReplaceAll(string(svgBytes), " ", "%20"),
		"data:image/svg+xml;utf8,"+string(svgWithSpaces),
		"data:Image/SVG+XML,%3Csvg%20xmlns%3D%22http%3A%2F%2Fwww.w3.org%2F2000%2Fsvg%22%2F%3E",
	)
	prefix := len("data:image/png;base64,")
	atCap := append(append([]byte{}, pngBytes...), make([]byte, (MaxEncodedLen-prefix)/4*3-len(pngBytes))...)
	inputs = append(inputs, dataURI("image/png", atCap))
	for _, in := range inputs {
		got, err := FromDataURI(in)
		if err != nil {
			t.Fatalf("FromDataURI(%.60q): %v", in, err)
		}
		if !pattern.MatchString(got) || len(got) > 65536 {
			t.Errorf("FromDataURI(%.60q) = %.60q (len %d), does not satisfy the CRD", in, got, len(got))
		}
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8":            true,
		"2606:4700::1111":    true,
		"127.0.0.1":          false,
		"::1":                false,
		"10.1.2.3":           false,
		"172.16.0.1":         false,
		"192.168.1.1":        false,
		"169.254.169.254":    false,
		"fe80::1":            false,
		"fd00::1":            false,
		"0.0.0.0":            false,
		"::":                 false,
		"100.64.0.1":         false,
		"224.0.0.1":          false,
		"::ffff:127.0.0.1":   false,
		"::ffff:10.0.0.1":    false,
		"64:ff9b::8.8.8.8":   true,
		"64:ff9b::10.0.0.1":  false,
		"64:ff9b::127.0.0.1": false,
		"64:ff9b::a9fe:a9fe": false,
	} {
		if got := publicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("publicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func allowAll(netip.Addr) bool { return true }

func testFetcher(srv *httptest.Server, allow func(netip.Addr) bool) *fetcher {
	tlsConfig := srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	return newFetcher(allow, tlsConfig)
}

func iconServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	serve := func(path, contentType string, body []byte) {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", contentType)
			_, _ = w.Write(body)
		})
	}
	serve("/icon.png", "image/png", pngBytes)
	serve("/icon.svg", "image/svg+xml; charset=utf-8", svgBytes)
	serve("/lying.png", "image/png", []byte("<html>not an image</html>"))
	serve("/icon.gif", "image/gif", []byte("GIF89a...."))
	serve("/huge.png", "image/png", append(append([]byte{}, pngBytes...), make([]byte, maxFetchBytes)...))
	mux.HandleFunc("/to-http", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/icon.png", http.StatusFound)
	})
	mux.HandleFunc("/to-https", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/icon.png", http.StatusFound)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetch(t *testing.T) {
	srv := iconServer(t)
	f := testFetcher(srv, allowAll)
	ctx := context.Background()

	if got, err := f.fetch(ctx, srv.URL+"/icon.png"); err != nil || got != dataURI("image/png", pngBytes) {
		t.Fatalf("png: %q, %v", got, err)
	}
	if got, err := f.fetch(ctx, srv.URL+"/icon.svg"); err != nil || got != dataURI(mimeSVG, svgBytes) {
		t.Fatalf("svg: %q, %v", got, err)
	}
	if got, err := f.fetch(ctx, srv.URL+"/to-https"); err != nil || got != dataURI("image/png", pngBytes) {
		t.Fatalf("https redirect: %q, %v", got, err)
	}
	for _, path := range []string{"/lying.png", "/icon.gif", "/huge.png", "/to-http"} {
		if _, err := f.fetch(ctx, srv.URL+path); !errors.Is(err, errInvalid) {
			t.Errorf("%s: err %v, want errInvalid", path, err)
		}
	}
	for _, src := range []string{"http://example.com/a.png", "file:///etc/passwd", "ftp://example.com/a.png"} {
		if _, err := f.fetch(ctx, src); !errors.Is(err, errInvalid) {
			t.Errorf("%s: err %v, want errInvalid", src, err)
		}
	}
}

func TestFetchRefusesLoopback(t *testing.T) {
	srv := iconServer(t)
	f := testFetcher(srv, publicAddr)
	if _, err := f.fetch(context.Background(), srv.URL+"/icon.png"); !errors.Is(err, errInvalid) {
		t.Fatalf("loopback fetch: err %v, want errInvalid", err)
	}
	localhost := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	if _, err := f.fetch(context.Background(), localhost+"/icon.png"); !errors.Is(err, errInvalid) {
		t.Fatalf("localhost fetch: err %v, want errInvalid", err)
	}
}

type changes struct {
	mu   sync.Mutex
	keys []string
	ch   chan string
}

func (c *changes) record(key string) {
	c.mu.Lock()
	c.keys = append(c.keys, key)
	c.mu.Unlock()
	c.ch <- key
}

func TestStoreFetchesOnceAndReportsChanges(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer srv.Close()

	s := newStore(testFetcher(srv, allowAll))
	c := &changes{ch: make(chan string, 8)}
	s.SetOnChange(c.record)

	if _, known := s.Lookup("github"); known {
		t.Fatal("icon known before any handshake")
	}

	impl := mcp.Implementation{Icons: []mcp.Icon{{Src: srv.URL + "/icon.png"}}}
	s.Record("github", impl)
	if key := <-c.ch; key != "github" {
		t.Fatalf("change for %q", key)
	}
	if icon, known := s.Lookup("github"); !known || icon != dataURI("image/png", pngBytes) {
		t.Fatalf("Lookup = %q, %v", icon, known)
	}

	s.Record("github", impl)
	s.Record("github", impl)
	if n := hits.Load(); n != 1 {
		t.Fatalf("fetched %d times, want 1", n)
	}

	// Another server with the same src is served from the cache.
	s.Record("github-2", impl)
	if key := <-c.ch; key != "github-2" {
		t.Fatalf("change for %q", key)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("fetched %d times, want 1", n)
	}

	// The server stops declaring an icon.
	s.Record("github", mcp.Implementation{})
	if key := <-c.ch; key != "github" {
		t.Fatalf("change for %q", key)
	}
	if icon, known := s.Lookup("github"); !known || icon != "" {
		t.Fatalf("Lookup after icon dropped = %q, %v", icon, known)
	}

	s.Forget("github")
	if _, known := s.Lookup("github"); known {
		t.Fatal("icon known after Forget")
	}
}

func TestStoreDataURIAndInvalid(t *testing.T) {
	s := newStore(newFetcher(publicAddr, nil))
	var changed []string
	s.SetOnChange(func(key string) { changed = append(changed, key) })

	s.Record("a", mcp.Implementation{Icons: []mcp.Icon{{Src: dataURI(mimeSVG, svgBytes)}}})
	if icon, _ := s.Lookup("a"); icon != dataURI(mimeSVG, svgBytes) {
		t.Fatalf("Lookup = %q", icon)
	}
	s.Record("a", mcp.Implementation{Icons: []mcp.Icon{{Src: "http://example.com/a.png"}}})
	if icon, known := s.Lookup("a"); !known || icon != "" {
		t.Fatalf("http icon: Lookup = %q, %v", icon, known)
	}
	// No icon for a server never seen is no change.
	s.Record("b", mcp.Implementation{})
	if len(changed) != 2 || changed[0] != "a" || changed[1] != "a" {
		t.Fatalf("changes = %v", changed)
	}
}
