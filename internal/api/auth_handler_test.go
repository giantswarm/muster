package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockAuthHandler implements AuthHandler for testing.
type mockAuthHandler struct {
	checkAuthRequiredFn    func(ctx context.Context, endpoint string) (bool, error)
	hasValidTokenFn        func(endpoint string) bool
	getBearerTokenFn       func(endpoint string) (string, error)
	loginFn                func(ctx context.Context, endpoint string) error
	loginWithIssuerFn      func(ctx context.Context, endpoint, issuerURL string) error
	logoutFn               func(endpoint string) error
	logoutAllFn            func() error
	getStatusFn            func() []AuthStatus
	getStatusForEndpointFn func(endpoint string) *AuthStatus
	refreshTokenFn         func(ctx context.Context, endpoint string) error
	closeFn                func() error
}

func (m *mockAuthHandler) CheckAuthRequired(ctx context.Context, endpoint string) (bool, error) {
	if m.checkAuthRequiredFn != nil {
		return m.checkAuthRequiredFn(ctx, endpoint)
	}
	return false, nil
}

func (m *mockAuthHandler) HasValidToken(endpoint string) bool {
	if m.hasValidTokenFn != nil {
		return m.hasValidTokenFn(endpoint)
	}
	return false
}

func (m *mockAuthHandler) GetBearerToken(endpoint string) (string, error) {
	if m.getBearerTokenFn != nil {
		return m.getBearerTokenFn(endpoint)
	}
	return "", errors.New("not authenticated")
}

func (m *mockAuthHandler) Login(ctx context.Context, endpoint string) error {
	if m.loginFn != nil {
		return m.loginFn(ctx, endpoint)
	}
	return nil
}

func (m *mockAuthHandler) LoginWithIssuer(ctx context.Context, endpoint, issuerURL string) error {
	if m.loginWithIssuerFn != nil {
		return m.loginWithIssuerFn(ctx, endpoint, issuerURL)
	}
	return nil
}

func (m *mockAuthHandler) Logout(endpoint string) error {
	if m.logoutFn != nil {
		return m.logoutFn(endpoint)
	}
	return nil
}

func (m *mockAuthHandler) LogoutAll() error {
	if m.logoutAllFn != nil {
		return m.logoutAllFn()
	}
	return nil
}

func (m *mockAuthHandler) GetStatus() []AuthStatus {
	if m.getStatusFn != nil {
		return m.getStatusFn()
	}
	return nil
}

func (m *mockAuthHandler) GetStatusForEndpoint(endpoint string) *AuthStatus {
	if m.getStatusForEndpointFn != nil {
		return m.getStatusForEndpointFn(endpoint)
	}
	return nil
}

func (m *mockAuthHandler) RefreshToken(ctx context.Context, endpoint string) error {
	if m.refreshTokenFn != nil {
		return m.refreshTokenFn(ctx, endpoint)
	}
	return nil
}

func (m *mockAuthHandler) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

func TestRegisterAuthHandler(t *testing.T) {
	// Cleanup after test
	defer SetAuthHandlerForTesting(nil)

	t.Run("registers handler", func(t *testing.T) {
		mock := &mockAuthHandler{}
		RegisterAuthHandler(mock)

		handler := GetAuthHandler()
		if handler == nil {
			t.Fatal("expected registered handler, got nil")
		}
		if handler != mock {
			t.Error("expected same handler instance")
		}
	})

	t.Run("replaces existing handler", func(t *testing.T) {
		mock1 := &mockAuthHandler{}
		mock2 := &mockAuthHandler{}

		RegisterAuthHandler(mock1)
		RegisterAuthHandler(mock2)

		handler := GetAuthHandler()
		if handler != mock2 {
			t.Error("expected second handler to replace first")
		}
	})
}

func TestGetAuthHandler(t *testing.T) {
	// Cleanup after test
	defer SetAuthHandlerForTesting(nil)

	t.Run("returns nil when not registered", func(t *testing.T) {
		SetAuthHandlerForTesting(nil)

		handler := GetAuthHandler()
		if handler != nil {
			t.Errorf("expected nil, got %v", handler)
		}
	})

	t.Run("returns registered handler", func(t *testing.T) {
		mock := &mockAuthHandler{}
		RegisterAuthHandler(mock)

		handler := GetAuthHandler()
		if handler == nil {
			t.Fatal("expected handler, got nil")
		}
	})
}

func TestSetAuthHandlerForTesting(t *testing.T) {
	// Cleanup after test
	defer SetAuthHandlerForTesting(nil)

	t.Run("sets handler for testing", func(t *testing.T) {
		mock := &mockAuthHandler{}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		if handler != mock {
			t.Error("expected set handler")
		}
	})

	t.Run("clears handler when set to nil", func(t *testing.T) {
		mock := &mockAuthHandler{}
		SetAuthHandlerForTesting(mock)
		SetAuthHandlerForTesting(nil)

		handler := GetAuthHandler()
		if handler != nil {
			t.Error("expected nil after clearing")
		}
	})
}

func TestAuthStatus_Structure(t *testing.T) {
	now := time.Now()
	status := AuthStatus{
		Endpoint:      "https://muster.example.com/mcp",
		Authenticated: true,
		ExpiresAt:     now.Add(1 * time.Hour),
		IssuerURL:     "https://dex.example.com",
		Error:         "",
	}

	if status.Endpoint != "https://muster.example.com/mcp" {
		t.Errorf("expected endpoint 'https://muster.example.com/mcp', got %q", status.Endpoint)
	}

	if !status.Authenticated {
		t.Error("expected authenticated to be true")
	}

	if status.ExpiresAt.IsZero() {
		t.Error("expected ExpiresAt to be set")
	}

	if status.IssuerURL != "https://dex.example.com" {
		t.Errorf("expected issuer 'https://dex.example.com', got %q", status.IssuerURL)
	}

	if status.Error != "" {
		t.Errorf("expected empty error, got %q", status.Error)
	}
}

func TestAuthHandler_CheckAuthRequired(t *testing.T) {
	defer SetAuthHandlerForTesting(nil)

	tests := []struct {
		name          string
		mockReturn    bool
		mockError     error
		expectedAuth  bool
		expectedError bool
	}{
		{
			name:          "auth required",
			mockReturn:    true,
			mockError:     nil,
			expectedAuth:  true,
			expectedError: false,
		},
		{
			name:          "auth not required",
			mockReturn:    false,
			mockError:     nil,
			expectedAuth:  false,
			expectedError: false,
		},
		{
			name:          "error checking auth",
			mockReturn:    false,
			mockError:     errors.New("network error"),
			expectedAuth:  false,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockAuthHandler{
				checkAuthRequiredFn: func(ctx context.Context, endpoint string) (bool, error) {
					return tt.mockReturn, tt.mockError
				},
			}
			SetAuthHandlerForTesting(mock)

			handler := GetAuthHandler()
			authRequired, err := handler.CheckAuthRequired(context.Background(), "https://example.com")

			if (err != nil) != tt.expectedError {
				t.Errorf("expected error=%v, got %v", tt.expectedError, err)
			}
			if authRequired != tt.expectedAuth {
				t.Errorf("expected authRequired=%v, got %v", tt.expectedAuth, authRequired)
			}
		})
	}
}

func TestAuthHandler_HasValidToken(t *testing.T) {
	defer SetAuthHandlerForTesting(nil)

	t.Run("has valid token", func(t *testing.T) {
		mock := &mockAuthHandler{
			hasValidTokenFn: func(endpoint string) bool {
				return true
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		if !handler.HasValidToken("https://example.com") {
			t.Error("expected HasValidToken to return true")
		}
	})

	t.Run("no valid token", func(t *testing.T) {
		mock := &mockAuthHandler{
			hasValidTokenFn: func(endpoint string) bool {
				return false
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		if handler.HasValidToken("https://example.com") {
			t.Error("expected HasValidToken to return false")
		}
	})
}

func TestAuthHandler_GetBearerToken(t *testing.T) {
	defer SetAuthHandlerForTesting(nil)

	t.Run("returns token when authenticated", func(t *testing.T) {
		expectedToken := "Bearer abc123"
		mock := &mockAuthHandler{
			getBearerTokenFn: func(endpoint string) (string, error) {
				return expectedToken, nil
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		token, err := handler.GetBearerToken("https://example.com")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if token != expectedToken {
			t.Errorf("expected token %q, got %q", expectedToken, token)
		}
	})

	t.Run("returns error when not authenticated", func(t *testing.T) {
		mock := &mockAuthHandler{
			getBearerTokenFn: func(endpoint string) (string, error) {
				return "", errors.New("not authenticated")
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		_, err := handler.GetBearerToken("https://example.com")
		if err == nil {
			t.Error("expected error when not authenticated")
		}
	})
}

func TestAuthHandler_Login(t *testing.T) {
	defer SetAuthHandlerForTesting(nil)

	t.Run("successful login", func(t *testing.T) {
		loginCalled := false
		mock := &mockAuthHandler{
			loginFn: func(ctx context.Context, endpoint string) error {
				loginCalled = true
				if endpoint != "https://example.com" {
					t.Errorf("expected endpoint 'https://example.com', got %q", endpoint)
				}
				return nil
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		err := handler.Login(context.Background(), "https://example.com")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !loginCalled {
			t.Error("expected login to be called")
		}
	})

	t.Run("login error", func(t *testing.T) {
		mock := &mockAuthHandler{
			loginFn: func(ctx context.Context, endpoint string) error {
				return errors.New("login failed")
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		err := handler.Login(context.Background(), "https://example.com")
		if err == nil {
			t.Error("expected error on login failure")
		}
	})
}

func TestAuthHandler_LoginWithIssuer(t *testing.T) {
	defer SetAuthHandlerForTesting(nil)

	t.Run("login with issuer", func(t *testing.T) {
		mock := &mockAuthHandler{
			loginWithIssuerFn: func(ctx context.Context, endpoint, issuerURL string) error {
				if endpoint != "https://example.com" {
					t.Errorf("expected endpoint 'https://example.com', got %q", endpoint)
				}
				if issuerURL != "https://dex.example.com" {
					t.Errorf("expected issuer 'https://dex.example.com', got %q", issuerURL)
				}
				return nil
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		err := handler.LoginWithIssuer(context.Background(), "https://example.com", "https://dex.example.com")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestAuthHandler_Logout(t *testing.T) {
	defer SetAuthHandlerForTesting(nil)

	t.Run("successful logout", func(t *testing.T) {
		logoutCalled := false
		mock := &mockAuthHandler{
			logoutFn: func(endpoint string) error {
				logoutCalled = true
				return nil
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		err := handler.Logout("https://example.com")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !logoutCalled {
			t.Error("expected logout to be called")
		}
	})

	t.Run("logout error", func(t *testing.T) {
		mock := &mockAuthHandler{
			logoutFn: func(endpoint string) error {
				return errors.New("logout failed")
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		err := handler.Logout("https://example.com")
		if err == nil {
			t.Error("expected error on logout failure")
		}
	})
}

func TestAuthHandler_LogoutAll(t *testing.T) {
	defer SetAuthHandlerForTesting(nil)

	t.Run("logout all", func(t *testing.T) {
		logoutAllCalled := false
		mock := &mockAuthHandler{
			logoutAllFn: func() error {
				logoutAllCalled = true
				return nil
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		err := handler.LogoutAll()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !logoutAllCalled {
			t.Error("expected logoutAll to be called")
		}
	})
}

func TestAuthHandler_GetStatus(t *testing.T) {
	defer SetAuthHandlerForTesting(nil)

	t.Run("returns status list", func(t *testing.T) {
		expectedStatuses := []AuthStatus{
			{
				Endpoint:      "https://example1.com",
				Authenticated: true,
			},
			{
				Endpoint:      "https://example2.com",
				Authenticated: false,
				Error:         "not authenticated",
			},
		}
		mock := &mockAuthHandler{
			getStatusFn: func() []AuthStatus {
				return expectedStatuses
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		statuses := handler.GetStatus()
		if len(statuses) != 2 {
			t.Errorf("expected 2 statuses, got %d", len(statuses))
		}
	})

	t.Run("returns empty list", func(t *testing.T) {
		mock := &mockAuthHandler{
			getStatusFn: func() []AuthStatus {
				return nil
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		statuses := handler.GetStatus()
		if len(statuses) != 0 {
			t.Errorf("expected 0 statuses, got %d", len(statuses))
		}
	})
}

func TestAuthHandler_GetStatusForEndpoint(t *testing.T) {
	defer SetAuthHandlerForTesting(nil)

	t.Run("returns status for endpoint", func(t *testing.T) {
		mock := &mockAuthHandler{
			getStatusForEndpointFn: func(endpoint string) *AuthStatus {
				if endpoint == "https://example.com" {
					return &AuthStatus{
						Endpoint:      endpoint,
						Authenticated: true,
					}
				}
				return nil
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		status := handler.GetStatusForEndpoint("https://example.com")
		if status == nil {
			t.Fatal("expected status, got nil")
		}
		if !status.Authenticated {
			t.Error("expected authenticated to be true")
		}
	})

	t.Run("returns nil for unknown endpoint", func(t *testing.T) {
		mock := &mockAuthHandler{
			getStatusForEndpointFn: func(endpoint string) *AuthStatus {
				return nil
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		status := handler.GetStatusForEndpoint("https://unknown.com")
		if status != nil {
			t.Errorf("expected nil, got %v", status)
		}
	})
}

func TestAuthHandler_RefreshToken(t *testing.T) {
	defer SetAuthHandlerForTesting(nil)

	t.Run("successful refresh", func(t *testing.T) {
		refreshCalled := false
		mock := &mockAuthHandler{
			refreshTokenFn: func(ctx context.Context, endpoint string) error {
				refreshCalled = true
				return nil
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		err := handler.RefreshToken(context.Background(), "https://example.com")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !refreshCalled {
			t.Error("expected refresh to be called")
		}
	})

	t.Run("refresh error", func(t *testing.T) {
		mock := &mockAuthHandler{
			refreshTokenFn: func(ctx context.Context, endpoint string) error {
				return errors.New("refresh failed")
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		err := handler.RefreshToken(context.Background(), "https://example.com")
		if err == nil {
			t.Error("expected error on refresh failure")
		}
	})
}

func TestAuthHandler_Close(t *testing.T) {
	defer SetAuthHandlerForTesting(nil)

	t.Run("successful close", func(t *testing.T) {
		closeCalled := false
		mock := &mockAuthHandler{
			closeFn: func() error {
				closeCalled = true
				return nil
			},
		}
		SetAuthHandlerForTesting(mock)

		handler := GetAuthHandler()
		err := handler.Close()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !closeCalled {
			t.Error("expected close to be called")
		}
	})
}
