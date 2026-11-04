package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// DevPortalOrigin returns the origin of aggregator.devPortalUrl, the only
// origin a backend's URL elicitation is relayed to, or "" when it is not set.
// A value that is not an absolute http or https URL without credentials is
// refused, so a typo does not silently turn the relay off.
func DevPortalOrigin(devPortalURL string) (string, error) {
	if devPortalURL == "" {
		return "", nil
	}
	origin := URLOrigin(devPortalURL)
	if origin == "" {
		return "", fmt.Errorf("aggregator.devPortalUrl %q is not an absolute http or https URL without a user name or password", devPortalURL)
	}
	return origin, nil
}

// URLOrigin returns the origin of an absolute http or https URL --
// "<scheme>://<host>[:<port>]", lower case, without the scheme's default
// port -- or "" for anything else, a URL that carries a user name or
// password included.
func URLOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host
}
