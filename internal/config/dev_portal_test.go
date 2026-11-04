package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestURLOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"https://portal.example.com/credentials?x=1": "https://portal.example.com",
		"HTTPS://Portal.Example.com:443/":            "https://portal.example.com",
		"https://portal.example.com:8443/a":          "https://portal.example.com:8443",
		"http://localhost:80/":                       "http://localhost",
		"http://[::1]:7007/":                         "http://[::1]:7007",
		"https://user:pass@portal.example.com/":      "",
		"javascript:alert(1)":                        "",
		"/relative/path":                             "",
		"ftp://portal.example.com/":                  "",
		"":                                           "",
	} {
		require.Equal(t, want, URLOrigin(raw), raw)
	}
}

func TestDevPortalOrigin(t *testing.T) {
	origin, err := DevPortalOrigin("")
	require.NoError(t, err)
	require.Empty(t, origin, "unset relays nothing")

	origin, err = DevPortalOrigin("https://portal.example.com/")
	require.NoError(t, err)
	require.Equal(t, "https://portal.example.com", origin)

	_, err = DevPortalOrigin("portal.example.com")
	require.Error(t, err, "a value that is not a URL is refused, not ignored")
}
