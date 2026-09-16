package cmd

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"

	"github.com/giantswarm/muster/v5/internal/agent/oauth"
	"github.com/giantswarm/muster/v5/internal/cli"
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
	withIDToken := func(expiresAt time.Time) storedIDToken {
		return storedIDToken{session: true, expiresAt: expiresAt}
	}
	tests := []struct {
		name   string
		stored storedIDToken
		force  bool
		renew  bool
		reason string
	}{
		{
			name:   "force renews a session with a valid ID token",
			stored: withIDToken(now.Add(23 * time.Hour)),
			force:  true,
			renew:  true,
			reason: "requested with --force",
		},
		{
			name:   "force needs no session",
			force:  true,
			renew:  true,
			reason: "requested with --force",
		},
		{
			name:  "no stored session, nothing to renew",
			renew: false,
		},
		{
			name:   "session without an ID token is renewed",
			stored: storedIDToken{session: true},
			renew:  true,
			reason: "the session carries no ID token",
		},
		{
			name:   "valid ID token is kept",
			stored: withIDToken(now.Add(23 * time.Hour)),
			renew:  false,
		},
		{
			name:   "expired ID token is renewed",
			stored: withIDToken(now.Add(-12 * 24 * time.Hour)),
			renew:  true,
			reason: "the ID token expired 12 days ago",
		},
		{
			name:   "ID token at its exp is renewed",
			stored: withIDToken(now),
			renew:  true,
			reason: "the ID token expired < 1 minute ago",
		},
		{
			name:   "ID token inside the expiry margin is renewed",
			stored: withIDToken(now.Add(10 * time.Second)),
			renew:  true,
			reason: "the ID token expires in < 1 minute",
		},
		{
			name:   "ID token just outside the expiry margin is kept",
			stored: withIDToken(now.Add(pkgoauth.DefaultExpiryMargin + time.Second)),
			renew:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			renew, reason := idTokenRenewal(tt.stored, tt.force, now)
			if renew != tt.renew {
				t.Fatalf("renew = %v, want %v (reason %q)", renew, tt.renew, reason)
			}
			if reason != tt.reason {
				t.Errorf("reason = %q, want %q", reason, tt.reason)
			}
		})
	}
}

// TestReadStoredIDToken_TokenFileOnly reproduces the session of #1268: the
// aggregator accepts it (the access token is past the CLI's own validity
// margin, the refresh token is current) while the stored ID token expired 12
// days ago. The login decision must find that ID token in the token file and
// renew -- without asking the server anything, because a probe that times out
// or a HEAD the server answers with 200 must not hide the ID token.
func TestReadStoredIDToken_TokenFileOnly(t *testing.T) {
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		probes.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	endpoint := server.URL + "/mcp"
	now := time.Now().Truncate(time.Second)

	// newHandler stores a session for the endpoint the way the CLI does and
	// hands back a fresh adapter that reads it from the file.
	newHandler := func(t *testing.T, idToken string) *cli.AuthAdapter {
		t.Helper()
		dir := t.TempDir()
		store, err := oauth.NewTokenStore(oauth.TokenStoreConfig{StorageDir: dir, FileMode: true})
		if err != nil {
			t.Fatalf("failed to create token store: %v", err)
		}
		token := &oauth2.Token{
			AccessToken:  "access-token",
			TokenType:    "Bearer",
			RefreshToken: "refresh-token",
			Expiry:       now.Add(-time.Hour),
		}
		if idToken != "" {
			token = token.WithExtra(map[string]interface{}{"id_token": idToken})
		}
		if err := store.StoreToken(pkgoauth.NormalizeServerURL(endpoint), "https://dex.example.com", token); err != nil {
			t.Fatalf("failed to store token: %v", err)
		}
		return newTestAuthAdapter(t, dir)
	}

	t.Run("expired ID token is renewed", func(t *testing.T) {
		exp := now.Add(-12 * 24 * time.Hour)
		stored := readStoredIDToken(newHandler(t, signedIDToken(t, exp)), endpoint)
		if !stored.session {
			t.Fatal("expected the token file to hold a session")
		}
		if !stored.expiresAt.Equal(exp) {
			t.Fatalf("expiresAt = %v, want the ID token's exp %v", stored.expiresAt, exp)
		}
		renew, reason := idTokenRenewal(stored, false, now)
		if !renew {
			t.Fatal("expected the expired ID token to be renewed")
		}
		if want := "the ID token expired 12 days ago"; reason != want {
			t.Errorf("reason = %q, want %q", reason, want)
		}
	})

	t.Run("current ID token is kept", func(t *testing.T) {
		stored := readStoredIDToken(newHandler(t, signedIDToken(t, now.Add(23*time.Hour))), endpoint)
		if renew, reason := idTokenRenewal(stored, false, now); renew {
			t.Errorf("expected the current ID token to be kept, got renewal: %s", reason)
		}
	})

	t.Run("session without an ID token is renewed", func(t *testing.T) {
		stored := readStoredIDToken(newHandler(t, ""), endpoint)
		if !stored.session || !stored.expiresAt.IsZero() {
			t.Fatalf("stored = %+v, want a session without an ID token", stored)
		}
		if renew, _ := idTokenRenewal(stored, false, now); !renew {
			t.Error("expected a session without an ID token to be renewed")
		}
	})

	t.Run("no stored session, nothing to renew", func(t *testing.T) {
		stored := readStoredIDToken(newTestAuthAdapter(t, t.TempDir()), endpoint)
		if stored.session {
			t.Fatal("expected no session in an empty token directory")
		}
		if renew, _ := idTokenRenewal(stored, false, now); renew {
			t.Error("expected nothing to renew without a stored session")
		}
	})

	if n := probes.Load(); n != 0 {
		t.Errorf("the decision asked the server %d time(s); it must read the token file only", n)
	}
}

// newTestAuthAdapter returns an adapter over the given token directory.
func newTestAuthAdapter(t *testing.T, dir string) *cli.AuthAdapter {
	t.Helper()
	adapter, err := cli.NewAuthAdapterWithConfig(cli.AuthAdapterConfig{TokenStorageDir: dir})
	if err != nil {
		t.Fatalf("failed to create auth adapter: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	return adapter
}

// signedIDToken returns an OIDC ID token with the given exp.
func signedIDToken(t *testing.T, exp time.Time) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":   "https://dex.example.com",
		"sub":   "user-1",
		"email": "user@example.com",
		"iat":   exp.Add(-24 * time.Hour).Unix(),
		"exp":   exp.Unix(),
	}).SignedString([]byte("test-signing-key"))
	if err != nil {
		t.Fatalf("failed to sign test ID token: %v", err)
	}
	return token
}
