package aggregator

import (
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

func (a *AggregatorServer) warnUntrustedOrigins(next http.Handler) http.Handler {
	var allowed []string
	if configuration, ok := a.config.OAuthServer.Config.(config.OAuthServerConfig); ok {
		if base, err := url.Parse(configuration.BaseURL); err == nil && base.Host != "" {
			allowed = append(allowed, base.Scheme+"://"+base.Host)
		}
		for _, origin := range strings.Split(configuration.AllowedOrigins, ",") {
			if strings.TrimSpace(origin) != "" {
				allowed = append(allowed, origin)
			}
		}
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if slices.Contains(actorBearerPaths, request.URL.Path) {
			origins := request.Header.Values("Origin")
			if len(origins) > 0 && !allowedRequestOrigin(request, origins, allowed) {
				logging.Warn("Aggregator", "DEPRECATED: Origin on %s is not allowed; configure oauth.server.allowedOrigins before Origin enforcement returns 403", request.URL.Path)
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func allowedRequestOrigin(request *http.Request, origins, allowed []string) bool {
	if len(origins) != 1 {
		return false
	}
	origin := canonicalOrigin(origins[0])
	if origin == "" {
		return false
	}
	for _, candidate := range allowed {
		if origin == canonicalOrigin(strings.TrimSpace(candidate)) {
			return true
		}
	}
	if len(allowed) == 0 {
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		}
		return origin == canonicalOrigin(scheme+"://"+request.Host)
	}
	return false
}

func canonicalOrigin(value string) string {
	origin, err := url.Parse(value)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery || strings.ContainsAny(value, "#*") || strings.HasSuffix(origin.Host, ":") {
		return ""
	}
	scheme := strings.ToLower(origin.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	port := origin.Port()
	if port == "" {
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(origin.Hostname()), port)
}
