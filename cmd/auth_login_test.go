package cmd

import (
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

func TestAuthLoginCmdProperties(t *testing.T) {
	t.Run("login command Use field", func(t *testing.T) {
		if authLoginCmd.Use != "login" {
			t.Errorf("expected Use 'login', got %q", authLoginCmd.Use)
		}
	})

	t.Run("login command has short description", func(t *testing.T) {
		if authLoginCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
	})

	t.Run("login command has long description", func(t *testing.T) {
		if authLoginCmd.Long == "" {
			t.Error("expected Long description to be set")
		}
	})

	t.Run("login command has RunE", func(t *testing.T) {
		if authLoginCmd.RunE == nil {
			t.Error("expected RunE to be set")
		}
	})

	t.Run("login command long description mentions examples", func(t *testing.T) {
		if authLoginCmd.Long == "" {
			t.Error("expected Long description to contain examples")
		}
	})
}

func TestAuthLogoutCmdProperties(t *testing.T) {
	t.Run("logout command Use field", func(t *testing.T) {
		if authLogoutCmd.Use != "logout" {
			t.Errorf("expected Use 'logout', got %q", authLogoutCmd.Use)
		}
	})

	t.Run("logout command has short description", func(t *testing.T) {
		if authLogoutCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
	})

	t.Run("logout command has RunE", func(t *testing.T) {
		if authLogoutCmd.RunE == nil {
			t.Error("expected RunE to be set")
		}
	})
}

func TestAuthLoginCmdForceFlag(t *testing.T) {
	if authLoginCmd.Flags().Lookup("force") == nil {
		t.Fatal("expected login command to define --force")
	}
}

func TestIDTokenRenewal(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		status *api.AuthStatus
		force  bool
		renew  bool
		reason string
	}{
		{
			name:   "force renews a session with a valid ID token",
			status: &api.AuthStatus{Authenticated: true, IDTokenExpiresAt: now.Add(23 * time.Hour)},
			force:  true,
			renew:  true,
			reason: "requested with --force",
		},
		{
			name:   "force needs no status",
			force:  true,
			renew:  true,
			reason: "requested with --force",
		},
		{
			name:  "no status, nothing to renew",
			renew: false,
		},
		{
			name:   "session without an ID token has nothing to renew",
			status: &api.AuthStatus{Authenticated: true, ExpiresAt: now.Add(25 * time.Minute)},
			renew:  false,
		},
		{
			name:   "valid ID token is kept",
			status: &api.AuthStatus{Authenticated: true, IDTokenExpiresAt: now.Add(23 * time.Hour)},
			renew:  false,
		},
		{
			name:   "expired ID token is renewed",
			status: &api.AuthStatus{Authenticated: true, IDTokenExpiresAt: now.Add(-12 * 24 * time.Hour)},
			renew:  true,
			reason: "the ID token expired 12 days ago",
		},
		{
			name:   "ID token at its exp is renewed",
			status: &api.AuthStatus{Authenticated: true, IDTokenExpiresAt: now},
			renew:  true,
			reason: "the ID token expired < 1 minute ago",
		},
		{
			name:   "ID token inside the expiry margin is renewed",
			status: &api.AuthStatus{Authenticated: true, IDTokenExpiresAt: now.Add(10 * time.Second)},
			renew:  true,
			reason: "the ID token expires in < 1 minute",
		},
		{
			name:   "ID token just outside the expiry margin is kept",
			status: &api.AuthStatus{Authenticated: true, IDTokenExpiresAt: now.Add(pkgoauth.DefaultExpiryMargin + time.Second)},
			renew:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			renew, reason := idTokenRenewal(tt.status, tt.force, now)
			if renew != tt.renew {
				t.Fatalf("renew = %v, want %v (reason %q)", renew, tt.renew, reason)
			}
			if reason != tt.reason {
				t.Errorf("reason = %q, want %q", reason, tt.reason)
			}
		})
	}
}
