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
		var deleteCalledWithUserID string

		mockHandler := &issuerMockOAuthHandler{
			enabled: true,
		}
		var captureHandler deleteCaptureMockHandler
		captureHandler.inner = mockHandler
		captureHandler.onDelete = func(userID string) {
			deleteCalledWithUserID = userID
		}
		api.RegisterOAuthHandler(&captureHandler)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		a := &AggregatorServer{
			subjectSessions: newSubjectSessionTracker(),
		}

		req := httptest.NewRequest(http.MethodDelete, "/user-tokens", nil)
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		a.handleUserTokensDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204, got %d", w.Code)
		}
		if deleteCalledWithUserID != "user@example.com" {
			t.Errorf("expected DeleteTokensByUser to be called with 'user@example.com', got %q", deleteCalledWithUserID)
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

		a := &AggregatorServer{
			subjectSessions: newSubjectSessionTracker(),
		}

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

		a := &AggregatorServer{
			subjectSessions: newSubjectSessionTracker(),
		}

		req := httptest.NewRequest(http.MethodDelete, "/user-tokens", nil)
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		a.handleUserTokensDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204 when OAuthHandler is disabled, got %d", w.Code)
		}
	})

	t.Run("user ID with special characters is forwarded correctly", func(t *testing.T) {
		var capturedUserID string

		var captureHandler deleteCaptureMockHandler
		captureHandler.inner = &issuerMockOAuthHandler{enabled: true}
		captureHandler.onDelete = func(userID string) {
			capturedUserID = userID
		}
		api.RegisterOAuthHandler(&captureHandler)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		a := &AggregatorServer{
			subjectSessions: newSubjectSessionTracker(),
		}

		specialUserID := "CgZpZDEyMxIGbG9jYWw"
		req := httptest.NewRequest(http.MethodDelete, "/user-tokens", nil)
		req = req.WithContext(api.WithSubject(req.Context(), specialUserID))
		w := httptest.NewRecorder()

		a.handleUserTokensDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204, got %d", w.Code)
		}
		if capturedUserID != specialUserID {
			t.Errorf("expected user ID %q, got %q", specialUserID, capturedUserID)
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
		captureHandler.onClear = func(sessionID, issuer string) {
			clearCalledWithIssuer = issuer
		}
		api.RegisterOAuthHandler(&captureHandler)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		reg := NewServerRegistry("")
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
		ctx := api.WithSubject(req.Context(), "user@example.com")
		ctx = api.WithSessionID(ctx, "session-123")
		req = req.WithContext(ctx)
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

	t.Run("returns 401 when session ID is missing", func(t *testing.T) {
		reg := NewServerRegistry("")
		a := &AggregatorServer{registry: reg}

		req := httptest.NewRequest(http.MethodDelete, "/auth/some-server", nil)
		req.SetPathValue("server", "some-server")
		req = req.WithContext(api.WithSubject(req.Context(), "user@example.com"))
		w := httptest.NewRecorder()

		a.handleAuthServerDeletion(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401 for missing session, got %d", w.Code)
		}
	})

	t.Run("returns 204 when server not found (prevents enumeration)", func(t *testing.T) {
		api.RegisterOAuthHandler(nil)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		reg := NewServerRegistry("")
		a := &AggregatorServer{registry: reg}

		req := httptest.NewRequest(http.MethodDelete, "/auth/nonexistent", nil)
		req.SetPathValue("server", "nonexistent")
		ctx := api.WithSubject(req.Context(), "user@example.com")
		ctx = api.WithSessionID(ctx, "test-session")
		req = req.WithContext(ctx)
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
		ctx := api.WithSubject(req.Context(), "user@example.com")
		ctx = api.WithSessionID(ctx, "test-session")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		a.handleAuthServerDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204 for server without auth info, got %d", w.Code)
		}
	})

	t.Run("invalidates capability cache for the requesting session only", func(t *testing.T) {
		api.RegisterOAuthHandler(&issuerMockOAuthHandler{enabled: true})
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

		store := NewInMemoryCapabilityStore(30 * time.Minute)
		defer store.Stop()
		_ = store.Set(context.Background(), "session-A", "target-server", &Capabilities{})
		_ = store.Set(context.Background(), "session-B", "target-server", &Capabilities{})

		reg := NewServerRegistry("")
		reg.mu.Lock()
		reg.servers["target-server"] = &ServerInfo{
			Name: "target-server",
			AuthInfo: &AuthInfo{
				Issuer: "https://auth.example.com",
			},
		}
		reg.mu.Unlock()

		a := &AggregatorServer{registry: reg, capabilityStore: store}

		req := httptest.NewRequest(http.MethodDelete, "/auth/target-server", nil)
		req.SetPathValue("server", "target-server")
		ctx := api.WithSubject(req.Context(), "user@example.com")
		ctx = api.WithSessionID(ctx, "session-A")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		a.handleAuthServerDeletion(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status 204, got %d", w.Code)
		}
		caps, _ := store.Get(context.Background(), "session-A", "target-server")
		if caps != nil {
			t.Error("expected requesting session's store entry to be deleted")
		}
		caps, _ = store.Get(context.Background(), "session-B", "target-server")
		if caps == nil {
			t.Error("other session's store entry should still exist")
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
		ctx := api.WithSubject(req.Context(), "user@example.com")
		ctx = api.WithSessionID(ctx, "test-session")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

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
		ctx := api.WithSubject(req.Context(), "user@example.com")
		ctx = api.WithSessionID(ctx, "test-session")
		req = req.WithContext(ctx)
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
	onDelete func(userID string)
}

func (d *deleteCaptureMockHandler) IsEnabled() bool { return d.inner.IsEnabled() }
func (d *deleteCaptureMockHandler) GetToken(sessionID, name string) *api.OAuthToken {
	return d.inner.GetToken(sessionID, name)
}
func (d *deleteCaptureMockHandler) GetTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	return d.inner.GetTokenByIssuer(sessionID, issuer)
}
func (d *deleteCaptureMockHandler) GetFullTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	return d.inner.GetFullTokenByIssuer(sessionID, issuer)
}
func (d *deleteCaptureMockHandler) FindTokenWithIDToken(sessionID string) *api.OAuthToken {
	return d.inner.FindTokenWithIDToken(sessionID)
}
func (d *deleteCaptureMockHandler) StoreToken(_, _, _ string, _ *api.OAuthToken) {}
func (d *deleteCaptureMockHandler) ClearTokenByIssuer(_, _ string)               {}
func (d *deleteCaptureMockHandler) DeleteTokensByUser(userID string) {
	if d.onDelete != nil {
		d.onDelete(userID)
	}
}
func (d *deleteCaptureMockHandler) DeleteTokensBySession(_ string) {}
func (d *deleteCaptureMockHandler) CreateAuthChallenge(_ context.Context, _, _, _, _, _ string) (*api.AuthChallenge, error) {
	return nil, nil
}
func (d *deleteCaptureMockHandler) GetHTTPHandler() http.Handler { return nil }
func (d *deleteCaptureMockHandler) GetCallbackPath() string      { return "/oauth/proxy/callback" }
func (d *deleteCaptureMockHandler) GetCIMDPath() string {
	return "/.well-known/oauth-client.json"
}
func (d *deleteCaptureMockHandler) ShouldServeCIMD() bool { return true }
func (d *deleteCaptureMockHandler) GetCIMDHandler() http.HandlerFunc {
	return nil
}
func (d *deleteCaptureMockHandler) RegisterServer(_, _, _ string)                          {}
func (d *deleteCaptureMockHandler) SetAuthCompletionCallback(_ api.AuthCompletionCallback) {}
func (d *deleteCaptureMockHandler) Stop()                                                  {}
func (d *deleteCaptureMockHandler) ExchangeTokenForRemoteCluster(_ context.Context, _, _ string, _ *api.TokenExchangeConfig) (string, error) {
	return "", nil
}
func (d *deleteCaptureMockHandler) ExchangeTokenForRemoteClusterWithClient(_ context.Context, _, _ string, _ *api.TokenExchangeConfig, _ *http.Client) (string, error) {
	return "", nil
}

// clearCaptureMockHandler wraps issuerMockOAuthHandler and captures
// calls to ClearTokenByIssuer for assertion.
type clearCaptureMockHandler struct {
	inner   *issuerMockOAuthHandler
	onClear func(sessionID, issuer string)
}

func (c *clearCaptureMockHandler) IsEnabled() bool { return c.inner.IsEnabled() }
func (c *clearCaptureMockHandler) GetToken(sessionID, name string) *api.OAuthToken {
	return c.inner.GetToken(sessionID, name)
}
func (c *clearCaptureMockHandler) GetTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	return c.inner.GetTokenByIssuer(sessionID, issuer)
}
func (c *clearCaptureMockHandler) GetFullTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	return c.inner.GetFullTokenByIssuer(sessionID, issuer)
}
func (c *clearCaptureMockHandler) FindTokenWithIDToken(sessionID string) *api.OAuthToken {
	return c.inner.FindTokenWithIDToken(sessionID)
}
func (c *clearCaptureMockHandler) StoreToken(_, _, _ string, _ *api.OAuthToken) {}
func (c *clearCaptureMockHandler) ClearTokenByIssuer(sessionID, issuer string) {
	if c.onClear != nil {
		c.onClear(sessionID, issuer)
	}
}
func (c *clearCaptureMockHandler) DeleteTokensByUser(_ string)    {}
func (c *clearCaptureMockHandler) DeleteTokensBySession(_ string) {}
func (c *clearCaptureMockHandler) CreateAuthChallenge(_ context.Context, _, _, _, _, _ string) (*api.AuthChallenge, error) {
	return nil, nil
}
func (c *clearCaptureMockHandler) GetHTTPHandler() http.Handler { return nil }
func (c *clearCaptureMockHandler) GetCallbackPath() string      { return "/oauth/proxy/callback" }
func (c *clearCaptureMockHandler) GetCIMDPath() string {
	return "/.well-known/oauth-client.json"
}
func (c *clearCaptureMockHandler) ShouldServeCIMD() bool { return true }
func (c *clearCaptureMockHandler) GetCIMDHandler() http.HandlerFunc {
	return nil
}
func (c *clearCaptureMockHandler) RegisterServer(_, _, _ string)                          {}
func (c *clearCaptureMockHandler) SetAuthCompletionCallback(_ api.AuthCompletionCallback) {}
func (c *clearCaptureMockHandler) Stop()                                                  {}
func (c *clearCaptureMockHandler) ExchangeTokenForRemoteCluster(_ context.Context, _, _ string, _ *api.TokenExchangeConfig) (string, error) {
	return "", nil
}
func (c *clearCaptureMockHandler) ExchangeTokenForRemoteClusterWithClient(_ context.Context, _, _ string, _ *api.TokenExchangeConfig, _ *http.Client) (string, error) {
	return "", nil
}
