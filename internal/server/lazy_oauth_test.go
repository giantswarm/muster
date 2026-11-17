package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

// newAlwaysFailingConfig uses a statically rejected issuer to avoid depending on DNS.
func newAlwaysFailingConfig() config.OAuthServerConfig {
	return config.OAuthServerConfig{
		Enabled:  true,
		Provider: OAuthProviderDex,
		BaseURL:  "https://localhost:19999", // nothing listening here
		Dex: config.DexConfig{
			IssuerURL:    "https://127.0.0.1:1",
			ClientID:     "test-client",
			ClientSecret: "test-secret",
		},
	}
}

func TestLazyOAuthHTTPServer_ServesBeforeReady(t *testing.T) {
	cfg := newAlwaysFailingConfig()
	lazy := NewLazyOAuthHTTPServer(t.Context(), cfg, http.NotFoundHandler(), false)

	mux := lazy.CreateMux()

	// /health must return 200 with degraded status
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "degraded")

	// /readyz must return 503 so no traffic reaches the pod yet
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "oidc-discovery-pending")

	// Any other path must return 503
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/authorize", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	// ValidateTokenWithSubject middleware must also return 503 before ready
	protected := lazy.ValidateTokenWithSubject(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec = httptest.NewRecorder()
	protected.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/some-endpoint", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestLazyOAuthHTTPServer_Shutdown_BeforeReady(t *testing.T) {
	cfg := newAlwaysFailingConfig()
	lazy := NewLazyOAuthHTTPServer(t.Context(), cfg, http.NotFoundHandler(), false)

	// Shutdown while still retrying must not block or panic
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	require.NoError(t, lazy.Shutdown(ctx))
}

func TestLazyOAuthHTTPServer_WaitReady_ContextCancelled(t *testing.T) {
	cfg := newAlwaysFailingConfig()
	lazy := NewLazyOAuthHTTPServer(t.Context(), cfg, http.NotFoundHandler(), false)
	defer func() { _ = lazy.Shutdown(context.Background()) }()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := lazy.WaitReady(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestLazyOAuthHTTPServer_SetOnAuthenticated_StoredBeforeReady(t *testing.T) {
	cfg := newAlwaysFailingConfig()
	lazy := NewLazyOAuthHTTPServer(t.Context(), cfg, http.NotFoundHandler(), false)
	defer func() { _ = lazy.Shutdown(context.Background()) }()

	var called atomic.Bool
	lazy.SetOnAuthenticated(func(_ context.Context, _ string) {
		called.Store(true)
	})

	// Callback is stored (not panicked), inner is nil so it can't be forwarded yet
	lazy.mu.RLock()
	cb := lazy.onAuthenticated
	lazy.mu.RUnlock()
	require.NotNil(t, cb)
}

func TestLazyOAuthHTTPServer_DegradedWhileRetryingReachableEndpoint(t *testing.T) {
	connected := make(chan struct{}, 1)
	oidcStub := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			base := "https://" + r.Host
			_, _ = w.Write([]byte(`{
				"issuer":"` + base + `",
				"authorization_endpoint":"` + base + `/oauth/authorize",
				"token_endpoint":"` + base + `/oauth/token",
				"jwks_uri":"` + base + `/oauth/keys",
				"response_types_supported":["code"],
				"subject_types_supported":["public"],
				"id_token_signing_alg_values_supported":["RS256"]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	oidcStub.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	}
	oidcStub.StartTLS()
	defer oidcStub.Close()

	cfg := newAlwaysFailingConfig()
	issuer, err := url.Parse(oidcStub.URL)
	require.NoError(t, err)
	issuer.Host = net.JoinHostPort("localhost", issuer.Port())
	cfg.Dex.IssuerURL = issuer.String()

	lazy := NewLazyOAuthHTTPServer(t.Context(), cfg, http.NotFoundHandler(), false)
	require.Eventually(t, func() bool {
		select {
		case <-connected:
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond)

	// While the loop is running, /health must return degraded.
	mux := lazy.CreateMux()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "degraded")

	// Shutdown must complete without deadlock.
	require.NoError(t, lazy.Shutdown(context.Background()))
}

func TestLazyOAuthHTTPServer_RetryAfterHeader(t *testing.T) {
	cfg := newAlwaysFailingConfig()
	lazy := NewLazyOAuthHTTPServer(t.Context(), cfg, http.NotFoundHandler(), false)
	defer func() { _ = lazy.Shutdown(context.Background()) }()

	mux := lazy.CreateMux()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/token", nil))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain"))
	assert.NotContains(t, rec.Body.String(), `"error"`)
}
