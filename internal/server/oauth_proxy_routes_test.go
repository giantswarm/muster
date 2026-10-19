package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/giantswarm/muster/v5/internal/api"
)

// routeOAuthHandler is the OAuth proxy as the route setup sees it: a callback
// path and the handler mounted for it.
type routeOAuthHandler struct {
	api.OAuthHandler
	callback http.Handler
}

func (h *routeOAuthHandler) IsEnabled() bool                   { return true }
func (h *routeOAuthHandler) GetCallbackPath() string           { return "/oauth/proxy/callback" }
func (h *routeOAuthHandler) GetHTTPHandler() http.Handler      { return h.callback }
func (h *routeOAuthHandler) GetStartPath() string              { return "" }
func (h *routeOAuthHandler) ShouldServeCIMD() bool             { return false }
func (h *routeOAuthHandler) GetStartHandler() http.HandlerFunc { return nil }

// A server outside muster's namespace signs in through its namespace's
// redirect URI, so the callback must be served there too.
func TestSetupOAuthProxyRoutes_ServesTheNamespaceCallbacks(t *testing.T) {
	var served []string
	api.RegisterOAuthHandler(&routeOAuthHandler{callback: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		served = append(served, r.URL.Path)
	})})
	defer api.RegisterOAuthHandler(nil)

	mux := http.NewServeMux()
	(&OAuthHTTPServer{}).setupOAuthProxyRoutes(mux)

	for _, path := range []string{"/oauth/proxy/callback", "/oauth/ns/org-a/callback", "/oauth/ns/org-a/other"} {
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if len(served) != 2 || served[0] != "/oauth/proxy/callback" || served[1] != "/oauth/ns/org-a/callback" {
		t.Errorf("callback served %v, want the plain and the namespace callback only", served)
	}
}
