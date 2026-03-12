package aggregator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"
)

// ---------------------------------------------------------------------------
// Tests for handleUserTokensDeletion
// ---------------------------------------------------------------------------

func TestHandleUserTokensDeletion(t *testing.T) {
	t.Run("returns 204 when subject is present and OAuthHandler deletes tokens", func(t *testing.T) {
		var deleteCalledWithSubject string

		mockHandler := &issuerMockOAuthHandler{
			enabled: true,
		}
		// Override DeleteTokensByUser to capture the subject
		var captureHandler deleteCaptureMockHandler
		captureHandler.inner = mockHandler
		captureHandler.onDelete = func(subject string) {
			deleteCalledWithSubject = subject
		}
		api.RegisterOAuthHandler(&captureHandler)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		a := &AggregatorServer{}

		req := httptest.NewRequest(http.MethodDelete, "/user-tokens", nil)
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		a.handleUserTokensDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204, got %d", w.Code)
		}
		if deleteCalledWithSubject != "user@example.com" {
			t.Errorf("expected DeleteTokensByUser to be called with 'user@example.com', got %q", deleteCalledWithSubject)
		}
	})

	t.Run("returns 401 when no subject in context", func(t *testing.T) {
		api.RegisterOAuthHandler(nil)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		a := &AggregatorServer{}

		req := httptest.NewRequest(http.MethodDelete, "/user-tokens", nil)
		// No subject injected into context
		w := httptest.NewRecorder()

		a.handleUserTokensDeletion(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", w.Code)
		}
	})

	t.Run("returns 204 when OAuthHandler is nil", func(t *testing.T) {
		api.RegisterOAuthHandler(nil)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		a := &AggregatorServer{}

		req := httptest.NewRequest(http.MethodDelete, "/user-tokens", nil)
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		// Should not panic when no OAuth handler is registered
		a.handleUserTokensDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204 even without OAuthHandler, got %d", w.Code)
		}
	})

	t.Run("returns 204 when OAuthHandler is disabled", func(t *testing.T) {
		mockHandler := &issuerMockOAuthHandler{enabled: false}
		api.RegisterOAuthHandler(mockHandler)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		a := &AggregatorServer{}

		req := httptest.NewRequest(http.MethodDelete, "/user-tokens", nil)
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		a.handleUserTokensDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204 when OAuthHandler is disabled, got %d", w.Code)
		}
	})

	t.Run("subject with special characters is forwarded correctly", func(t *testing.T) {
		var capturedSubject string

		var captureHandler deleteCaptureMockHandler
		captureHandler.inner = &issuerMockOAuthHandler{enabled: true}
		captureHandler.onDelete = func(subject string) {
			capturedSubject = subject
		}
		api.RegisterOAuthHandler(&captureHandler)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		a := &AggregatorServer{}

		specialSubject := "CgZpZDEyMxIGbG9jYWw"
		req := httptest.NewRequest(http.MethodDelete, "/user-tokens", nil)
		req = req.WithContext(api.WithSubject(req.Context(), specialSubject))
		w := httptest.NewRecorder()

		a.handleUserTokensDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204, got %d", w.Code)
		}
		if capturedSubject != specialSubject {
			t.Errorf("expected subject %q, got %q", specialSubject, capturedSubject)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for handleAuthServerDeletion
// ---------------------------------------------------------------------------

func TestHandleAuthServerDeletion(t *testing.T) {
	t.Run("returns 204 and clears token for existing server with auth info", func(t *testing.T) {
		var clearCalledWithIssuer string

		var captureHandler clearCaptureMockHandler
		captureHandler.inner = &issuerMockOAuthHandler{enabled: true}
		captureHandler.onClear = func(subject, issuer string) {
			clearCalledWithIssuer = issuer
		}
		api.RegisterOAuthHandler(&captureHandler)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		reg := NewServerRegistry("")
		// Directly insert a server with AuthInfo
		reg.mu.Lock()
		reg.servers["my-server"] = &ServerInfo{
			Name: "my-server",
			AuthInfo: &AuthInfo{
				Issuer: "https://auth.example.com",
			},
		}
		reg.mu.Unlock()

		a := &AggregatorServer{
			registry: reg,
		}

		req := httptest.NewRequest(http.MethodDelete, "/auth/my-server", nil)
		req.SetPathValue("server", "my-server")
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		a.handleAuthServerDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204, got %d", w.Code)
		}
		if clearCalledWithIssuer != "https://auth.example.com" {
			t.Errorf("expected ClearTokenByIssuer with 'https://auth.example.com', got %q", clearCalledWithIssuer)
		}
	})

	t.Run("returns 401 when no subject in context", func(t *testing.T) {
		api.RegisterOAuthHandler(nil)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		reg := NewServerRegistry("")
		a := &AggregatorServer{registry: reg}

		req := httptest.NewRequest(http.MethodDelete, "/auth/some-server", nil)
		req.SetPathValue("server", "some-server")
		// No subject in context
		w := httptest.NewRecorder()

		a.handleAuthServerDeletion(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", w.Code)
		}
	})

	t.Run("returns 204 when server not found (prevents enumeration)", func(t *testing.T) {
		api.RegisterOAuthHandler(nil)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		reg := NewServerRegistry("")
		a := &AggregatorServer{registry: reg}

		req := httptest.NewRequest(http.MethodDelete, "/auth/nonexistent", nil)
		req.SetPathValue("server", "nonexistent")
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		a.handleAuthServerDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204 for unknown server (idempotent delete), got %d", w.Code)
		}
	})

	t.Run("returns 204 for existing server without auth info (no token to clear)", func(t *testing.T) {
		api.RegisterOAuthHandler(&issuerMockOAuthHandler{enabled: true})
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		reg := NewServerRegistry("")
		reg.mu.Lock()
		reg.servers["plain-server"] = &ServerInfo{
			Name:     "plain-server",
			AuthInfo: nil, // No auth info
		}
		reg.mu.Unlock()

		a := &AggregatorServer{registry: reg}

		req := httptest.NewRequest(http.MethodDelete, "/auth/plain-server", nil)
		req.SetPathValue("server", "plain-server")
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		a.handleAuthServerDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204 for server without auth info, got %d", w.Code)
		}
	})

	t.Run("invalidates capability cache for the requesting user only", func(t *testing.T) {
		api.RegisterOAuthHandler(&issuerMockOAuthHandler{enabled: true})
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		cache := NewCapabilityCache(5 * time.Minute)
		// Populate cache for two users
		cache.Set("user@example.com", "target-server", nil, nil, nil)
		cache.Set("other-user@example.com", "target-server", nil, nil, nil)

		reg := NewServerRegistry("")
		reg.mu.Lock()
		reg.servers["target-server"] = &ServerInfo{
			Name: "target-server",
			AuthInfo: &AuthInfo{
				Issuer: "https://auth.example.com",
			},
		}
		reg.mu.Unlock()

		a := &AggregatorServer{registry: reg, capabilityCache: cache}

		req := httptest.NewRequest(http.MethodDelete, "/auth/target-server", nil)
		req.SetPathValue("server", "target-server")
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		a.handleAuthServerDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204, got %d", w.Code)
		}
		// The requesting user's cache should have been invalidated
		if _, ok := cache.Get("user@example.com", "target-server"); ok {
			t.Error("expected requesting user's cache entry to be invalidated")
		}
		// The other user's cache should NOT have been invalidated
		if _, ok := cache.Get("other-user@example.com", "target-server"); !ok {
			t.Error("other user's cache entry should still exist")
		}
	})

	t.Run("returns 204 when OAuthHandler is nil (no token clearing attempted)", func(t *testing.T) {
		api.RegisterOAuthHandler(nil)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		reg := NewServerRegistry("")
		reg.mu.Lock()
		reg.servers["server-no-handler"] = &ServerInfo{
			Name: "server-no-handler",
			AuthInfo: &AuthInfo{
				Issuer: "https://auth.example.com",
			},
		}
		reg.mu.Unlock()

		a := &AggregatorServer{registry: reg}

		req := httptest.NewRequest(http.MethodDelete, "/auth/server-no-handler", nil)
		req.SetPathValue("server", "server-no-handler")
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		// Should not panic even with nil handler
		a.handleAuthServerDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204 when no OAuthHandler, got %d", w.Code)
		}
	})

	t.Run("returns 400 when server path value is empty", func(t *testing.T) {
		api.RegisterOAuthHandler(nil)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		a := &AggregatorServer{registry: NewServerRegistry("")}

		req := httptest.NewRequest(http.MethodDelete, "/auth/", nil)
		// Do not set path value -- PathValue returns "" by default
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		a.handleAuthServerDeletion(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400 for empty server name, got %d", w.Code)
		}
	})
}

// ---------------------------------------------------------------------------
// Test helper mock types
// ---------------------------------------------------------------------------

// deleteCaptureMockHandler wraps issuerMockOAuthHandler and captures
// calls to DeleteTokensByUser for assertion.
type deleteCaptureMockHandler struct {
	inner    *issuerMockOAuthHandler
	onDelete func(subject string)
}

func (d *deleteCaptureMockHandler) IsEnabled() bool { return d.inner.IsEnabled() }
func (d *deleteCaptureMockHandler) GetToken(sub, name string) *api.OAuthToken {
	return d.inner.GetToken(sub, name)
}
func (d *deleteCaptureMockHandler) GetTokenByIssuer(sub, issuer string) *api.OAuthToken {
	return d.inner.GetTokenByIssuer(sub, issuer)
}
func (d *deleteCaptureMockHandler) GetFullTokenByIssuer(sub, issuer string) *api.OAuthToken {
	return d.inner.GetFullTokenByIssuer(sub, issuer)
}
func (d *deleteCaptureMockHandler) FindTokenWithIDToken(sub string) *api.OAuthToken {
	return d.inner.FindTokenWithIDToken(sub)
}
func (d *deleteCaptureMockHandler) StoreToken(sub, issuer string, token *api.OAuthToken) {
	d.inner.StoreToken(sub, issuer, token)
}
func (d *deleteCaptureMockHandler) ClearTokenByIssuer(sub, issuer string) {
	d.inner.ClearTokenByIssuer(sub, issuer)
}
func (d *deleteCaptureMockHandler) DeleteTokensByUser(subject string) {
	if d.onDelete != nil {
		d.onDelete(subject)
	}
}
func (d *deleteCaptureMockHandler) CreateAuthChallenge(ctx context.Context, sub, name, issuer, scope string) (*api.AuthChallenge, error) {
	return nil, nil
}
func (d *deleteCaptureMockHandler) GetHTTPHandler() http.Handler { return nil }
func (d *deleteCaptureMockHandler) GetCallbackPath() string      { return "/oauth/proxy/callback" }
func (d *deleteCaptureMockHandler) GetCIMDPath() string          { return "/.well-known/oauth-client.json" }
func (d *deleteCaptureMockHandler) ShouldServeCIMD() bool        { return true }
func (d *deleteCaptureMockHandler) GetCIMDHandler() http.HandlerFunc {
	return nil
}
func (d *deleteCaptureMockHandler) RegisterServer(name, issuer, scope string)               {}
func (d *deleteCaptureMockHandler) SetAuthCompletionCallback(cb api.AuthCompletionCallback) {}
func (d *deleteCaptureMockHandler) Stop()                                                   {}
func (d *deleteCaptureMockHandler) ExchangeTokenForRemoteCluster(ctx context.Context, local, userID string, cfg *api.TokenExchangeConfig) (string, error) {
	return "", nil
}
func (d *deleteCaptureMockHandler) ExchangeTokenForRemoteClusterWithClient(ctx context.Context, local, userID string, cfg *api.TokenExchangeConfig, client *http.Client) (string, error) {
	return "", nil
}

// clearCaptureMockHandler wraps issuerMockOAuthHandler and captures
// calls to ClearTokenByIssuer for assertion.
type clearCaptureMockHandler struct {
	inner   *issuerMockOAuthHandler
	onClear func(subject, issuer string)
}

func (c *clearCaptureMockHandler) IsEnabled() bool { return c.inner.IsEnabled() }
func (c *clearCaptureMockHandler) GetToken(sub, name string) *api.OAuthToken {
	return c.inner.GetToken(sub, name)
}
func (c *clearCaptureMockHandler) GetTokenByIssuer(sub, issuer string) *api.OAuthToken {
	return c.inner.GetTokenByIssuer(sub, issuer)
}
func (c *clearCaptureMockHandler) GetFullTokenByIssuer(sub, issuer string) *api.OAuthToken {
	return c.inner.GetFullTokenByIssuer(sub, issuer)
}
func (c *clearCaptureMockHandler) FindTokenWithIDToken(sub string) *api.OAuthToken {
	return c.inner.FindTokenWithIDToken(sub)
}
func (c *clearCaptureMockHandler) StoreToken(sub, issuer string, token *api.OAuthToken) {
	c.inner.StoreToken(sub, issuer, token)
}
func (c *clearCaptureMockHandler) ClearTokenByIssuer(subject, issuer string) {
	if c.onClear != nil {
		c.onClear(subject, issuer)
	}
}
func (c *clearCaptureMockHandler) DeleteTokensByUser(subject string) {}
func (c *clearCaptureMockHandler) CreateAuthChallenge(ctx context.Context, sub, name, issuer, scope string) (*api.AuthChallenge, error) {
	return nil, nil
}
func (c *clearCaptureMockHandler) GetHTTPHandler() http.Handler { return nil }
func (c *clearCaptureMockHandler) GetCallbackPath() string      { return "/oauth/proxy/callback" }
func (c *clearCaptureMockHandler) GetCIMDPath() string          { return "/.well-known/oauth-client.json" }
func (c *clearCaptureMockHandler) ShouldServeCIMD() bool        { return true }
func (c *clearCaptureMockHandler) GetCIMDHandler() http.HandlerFunc {
	return nil
}
func (c *clearCaptureMockHandler) RegisterServer(name, issuer, scope string)               {}
func (c *clearCaptureMockHandler) SetAuthCompletionCallback(cb api.AuthCompletionCallback) {}
func (c *clearCaptureMockHandler) Stop()                                                   {}
func (c *clearCaptureMockHandler) ExchangeTokenForRemoteCluster(ctx context.Context, local, userID string, cfg *api.TokenExchangeConfig) (string, error) {
	return "", nil
}
func (c *clearCaptureMockHandler) ExchangeTokenForRemoteClusterWithClient(ctx context.Context, local, userID string, cfg *api.TokenExchangeConfig, client *http.Client) (string, error) {
	return "", nil
}
