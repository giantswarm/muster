package servericon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

const (
	fetchTimeout = 5 * time.Second
	maxRedirects = 3
	// maxFetchBytes is the largest body whose data: URI can fit MaxEncodedLen.
	maxFetchBytes = MaxEncodedLen / 4 * 3
)

// blockedPrefixes are ranges netip's predicates do not cover: "this
// network" and carrier-grade NAT.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
}

// publicAddr reports whether muster may connect to addr for an icon: never
// loopback, private, link-local, multicast or unspecified.
func publicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// fetcher downloads https icons. allowAddr guards every address the dialer
// connects to, after name resolution, so a DNS answer cannot steer it to
// an internal address.
type fetcher struct {
	client *http.Client
}

func newFetcher(allowAddr func(netip.Addr) bool, tlsConfig *tls.Config) *fetcher {
	dialer := &net.Dialer{
		Timeout: fetchTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: address %q: %v", errInvalid, address, err)
			}
			if !allowAddr(ap.Addr()) {
				return fmt.Errorf("%w: address %s not allowed", errInvalid, ap.Addr())
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       tlsConfig,
		TLSHandshakeTimeout:   fetchTimeout,
		ResponseHeaderTimeout: fetchTimeout,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
	return &fetcher{client: &http.Client{
		Transport: transport,
		Timeout:   fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("%w: more than %d redirects", errInvalid, maxRedirects)
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("%w: redirect to %s", errInvalid, req.URL.Scheme)
			}
			return nil
		},
	}}
}

// fetch downloads an https icon and returns it as a data: URI.
func (f *fetcher) fetch(ctx context.Context, src string) (string, error) {
	u, err := url.Parse(src)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("%w: only https icons are fetched", errInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errInvalid, err)
	}
	req.Header.Set("Accept", "image/svg+xml, image/png, image/jpeg, image/webp")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", unwrapInvalid(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("icon answered %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if !allowedMIME(normalizeMIME(contentType)) {
		return "", fmt.Errorf("%w: content type %q not allowed", errInvalid, contentType)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return "", fmt.Errorf("read icon: %w", err)
	}
	if len(data) > maxFetchBytes {
		return "", fmt.Errorf("%w: over %d bytes", errInvalid, maxFetchBytes)
	}
	return encode(contentType, data)
}

// unwrapInvalid keeps errInvalid visible through url.Error, so a refused
// address or redirect counts as invalid rather than transient.
func unwrapInvalid(err error) error {
	if errors.Is(err, errInvalid) {
		return fmt.Errorf("%w: %v", errInvalid, err)
	}
	return err
}
