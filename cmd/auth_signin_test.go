package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"golang.org/x/oauth2"

	"github.com/giantswarm/muster/v5/internal/agent/oauth"
	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/cli"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// recordingHandler counts the sign-ins signIn asks for. Login is delegated
// when a real handler is embedded; Relogin stands in for the interactive flow
// and runs onRelogin, the server's view of the flow's outcome.
type recordingHandler struct {
	api.AuthHandler
	logins, relogins int
	onRelogin        func()
}

func (h *recordingHandler) Login(ctx context.Context, endpoint string) error {
	h.logins++
	if h.AuthHandler == nil {
		return nil
	}
	return h.AuthHandler.Login(ctx, endpoint)
}

func (h *recordingHandler) Relogin(context.Context, string) error {
	h.relogins++
	if h.onRelogin != nil {
		h.onRelogin()
	}
	return nil
}

func TestSignIn(t *testing.T) {
	const endpoint = "https://muster.example.com/mcp"
	rejected := transport.ErrUnauthorized

	tests := []struct {
		name         string
		force        bool
		serverBefore error // the server's answer before an interactive flow
		serverAfter  error // the server's answer after it
		wantLogins   int
		wantRelogins int
		wantFailed   bool
	}{
		{name: "stored session accepted", wantLogins: 1},
		{name: "stored session rejected runs the flow", serverBefore: rejected, wantLogins: 1, wantRelogins: 1},
		{name: "force runs the flow at once", force: true, serverBefore: rejected, wantRelogins: 1},
		{name: "session still rejected after the flow", serverBefore: rejected, serverAfter: rejected, wantLogins: 1, wantRelogins: 1, wantFailed: true},
		{name: "unreachable server after login", serverBefore: errors.New("connection refused"), wantLogins: 1, wantFailed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answer := tt.serverBefore
			handler := &recordingHandler{onRelogin: func() { answer = tt.serverAfter }}

			err := signIn(context.Background(), handler, endpoint, tt.force, func() error { return answer })

			if handler.logins != tt.wantLogins || handler.relogins != tt.wantRelogins {
				t.Errorf("logins/relogins = %d/%d, want %d/%d", handler.logins, handler.relogins, tt.wantLogins, tt.wantRelogins)
			}
			var failed *cli.AuthFailedError
			if got := errors.As(err, &failed); got != tt.wantFailed {
				t.Fatalf("err = %v, want an AuthFailedError: %v", err, tt.wantFailed)
			}
			if !tt.wantFailed && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestSignIn_LocallyValidTokenRejectedByServer stores an access token that is
// unexpired by its stored expiry for a server that answers every request with
// 401, as after the server dropped the session. Login judges the token by its
// expiry and starts no flow; the connection to the server must lead signIn to
// the interactive flow.
func TestSignIn_LocallyValidTokenRejectedByServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="http://`+r.Host+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	endpoint := server.URL + "/mcp"

	home := t.TempDir()
	t.Setenv("HOME", home)
	tokenDir := filepath.Join(home, pkgoauth.DefaultTokenStorageDir)
	store, err := oauth.NewTokenStore(oauth.TokenStoreConfig{StorageDir: tokenDir, FileMode: true})
	if err != nil {
		t.Fatalf("failed to create token store: %v", err)
	}
	token := &oauth2.Token{
		AccessToken:  "dropped-access-token",
		TokenType:    "Bearer",
		RefreshToken: "dropped-refresh-token",
		Expiry:       time.Now().Add(time.Hour),
	}
	if err := store.StoreToken(pkgoauth.NormalizeServerURL(endpoint), "https://dex.example.com", oauth.DefaultAgentClientID, token); err != nil {
		t.Fatalf("failed to store token: %v", err)
	}

	adapter := newTestAuthAdapter(t, tokenDir)
	ctx := context.Background()
	connect := func() error { return tryMCPConnection(ctx, adapter, endpoint) }

	if err := connect(); !pkgoauth.IsOAuthUnauthorizedError(err) {
		t.Fatalf("connection error = %v, want the server's rejection", err)
	}
	handler := &recordingHandler{AuthHandler: adapter}
	err = signIn(ctx, handler, endpoint, false, connect)

	if handler.relogins != 1 {
		t.Fatalf("relogins = %d, want the interactive flow once", handler.relogins)
	}
	// The stand-in flow leaves the server rejecting the session: the sign-in
	// must not report success.
	var failed *cli.AuthFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want an AuthFailedError", err)
	}
}
