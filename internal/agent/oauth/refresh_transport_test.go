package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"golang.org/x/oauth2"
)

// rotatingServer is an authorization server with OAuth 2.1 refresh token
// rotation and reuse detection: each refresh token is good for one refresh,
// and presenting a spent one counts as reuse.
type rotatingServer struct {
	*httptest.Server

	mu         sync.Mutex
	current    string // the one refresh token the server accepts
	generation int
	refreshes  int
	reuses     int
}

func newRotatingServer(t *testing.T, refreshToken string) *rotatingServer {
	t.Helper()
	s := &rotatingServer{current: refreshToken}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 s.URL,
				"authorization_endpoint": s.URL + "/authorize",
				"token_endpoint":         s.URL + "/token",
			})
		case "/token":
			_ = r.ParseForm()
			s.mu.Lock()
			defer s.mu.Unlock()
			s.refreshes++
			if r.PostForm.Get("refresh_token") != s.current {
				s.reuses++
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
				return
			}
			s.generation++
			s.current = fmt.Sprintf("refresh-%d", s.generation)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  fmt.Sprintf("access-%d", s.generation),
				"token_type":    "Bearer",
				"refresh_token": s.current,
				"expires_in":    3600,
				"id_token":      fmt.Sprintf("id-%d", s.generation),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *rotatingServer) counts() (refreshes, reuses int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshes, s.reuses
}

// muster is one muster process: its own token store on the shared directory
// and its own mcp-go OAuth handler.
type muster struct {
	store   *AgentTokenStore
	handler *transport.OAuthHandler
}

func newMuster(t *testing.T, server *rotatingServer, dir string) *muster {
	t.Helper()
	cfg, store, err := SetupOAuthConfigWithDir(server.URL, dir)
	if err != nil {
		t.Fatalf("SetupOAuthConfigWithDir: %v", err)
	}
	cfg.AuthServerMetadataURL = server.URL + "/.well-known/oauth-authorization-server"
	handler := transport.NewOAuthHandler(*cfg)
	handler.SetBaseURL(server.URL)
	return &muster{store: store, handler: handler}
}

func (m *muster) header(t *testing.T) string {
	t.Helper()
	header, err := m.handler.GetAuthorizationHeader(context.Background())
	if err != nil {
		t.Fatalf("GetAuthorizationHeader: %v", err)
	}
	return header
}

func storeSession(t *testing.T, server *rotatingServer, dir, accessToken, refreshToken string, expiry time.Time) {
	t.Helper()
	token := (&oauth2.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		Expiry:       expiry,
	}).WithExtra(map[string]interface{}{"id_token": "id-0"})
	if err := newFileStore(t, dir).StoreToken(server.URL, server.URL, DefaultAgentClientID, token); err != nil {
		t.Fatalf("StoreToken: %v", err)
	}
}

// A process whose cached token another process has since refreshed adopts
// the refreshed token from the file instead of sending its spent refresh
// token, which the server would take for reuse.
func TestRefresh_StaleCacheAdoptsTheFileToken(t *testing.T) {
	server := newRotatingServer(t, "refresh-0")
	dir := t.TempDir()
	storeSession(t, server, dir, "access-0", "refresh-0", time.Now().Add(-time.Minute))

	first, second := newMuster(t, server, dir), newMuster(t, server, dir)
	if _, err := second.store.GetToken(context.Background()); err != nil {
		t.Fatalf("GetToken: %v", err)
	}

	if got := first.header(t); got != "Bearer access-1" {
		t.Fatalf("first process: %q, want the refreshed token", got)
	}
	if got := second.header(t); got != "Bearer access-1" {
		t.Fatalf("second process: %q, want the token the first one stored", got)
	}
	if got := second.store.GetIDToken(); got != "id-1" {
		t.Errorf("second process ID token: %q, want id-1", got)
	}
	if refreshes, reuses := server.counts(); refreshes != 1 || reuses != 0 {
		t.Errorf("server saw %d refreshes and %d reuses, want 1 and 0", refreshes, reuses)
	}
}

// Processes reaching an expired token at the same moment refresh it once.
func TestRefresh_ConcurrentProcessesRefreshOnce(t *testing.T) {
	server := newRotatingServer(t, "refresh-0")
	dir := t.TempDir()
	storeSession(t, server, dir, "access-0", "refresh-0", time.Now().Add(-time.Minute))

	const processes = 8
	musters := make([]*muster, processes)
	for i := range musters {
		musters[i] = newMuster(t, server, dir)
	}

	headers := make([]string, processes)
	errs := make([]error, processes)
	var wg sync.WaitGroup
	for i, m := range musters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			headers[i], errs[i] = m.handler.GetAuthorizationHeader(context.Background())
		}()
	}
	wg.Wait()

	for i := range musters {
		if errs[i] != nil {
			t.Errorf("process %d: %v", i, errs[i])
		} else if headers[i] != "Bearer access-1" {
			t.Errorf("process %d: %q, want Bearer access-1", i, headers[i])
		}
	}
	if refreshes, reuses := server.counts(); refreshes != 1 || reuses != 0 {
		t.Errorf("server saw %d refreshes and %d reuses, want 1 and 0", refreshes, reuses)
	}
}

// A process whose cached refresh token was spent by another process, whose
// own token has expired since, refreshes with the file's refresh token.
func TestRefresh_SpentRefreshTokenIsReplacedByTheFileOne(t *testing.T) {
	server := newRotatingServer(t, "refresh-0")
	dir := t.TempDir()
	storeSession(t, server, dir, "access-0", "refresh-0", time.Now().Add(-time.Minute))

	stale := newMuster(t, server, dir)
	if _, err := stale.store.GetToken(context.Background()); err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if got := newMuster(t, server, dir).header(t); got != "Bearer access-1" {
		t.Fatalf("other process: %q, want Bearer access-1", got)
	}
	// The other process's token has expired too by the time this one refreshes.
	storeSession(t, server, dir, "access-1", "refresh-1", time.Now().Add(-time.Minute))

	if got := stale.header(t); got != "Bearer access-2" {
		t.Fatalf("stale process: %q, want Bearer access-2", got)
	}
	if refreshes, reuses := server.counts(); refreshes != 2 || reuses != 0 {
		t.Errorf("server saw %d refreshes and %d reuses, want 2 and 0", refreshes, reuses)
	}
	if got := newFileStore(t, dir).GetToken(server.URL); got == nil || got.RefreshToken != "refresh-2" {
		t.Errorf("file holds %+v, want refresh-2", got)
	}
}

func TestTokenStore_MemoryModeRefreshLockAndReload(t *testing.T) {
	store, err := NewTokenStore(TokenStoreConfig{})
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	unlock, err := store.LockRefresh("https://muster.example/mcp")
	if err != nil {
		t.Fatalf("LockRefresh: %v", err)
	}
	unlock()
	if got := store.ReloadToken("https://muster.example/mcp"); got != nil {
		t.Errorf("ReloadToken: %+v, want nil", got)
	}
	if err := store.StoreToken("https://muster.example/mcp", testDexURL, DefaultAgentClientID, numberedToken("m", 1)); err != nil {
		t.Fatalf("StoreToken: %v", err)
	}
	if got := store.ReloadToken("https://muster.example/mcp"); got == nil || got.AccessToken != "m-1" {
		t.Errorf("ReloadToken: %+v, want the cached token", got)
	}
}
