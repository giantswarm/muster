package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"golang.org/x/oauth2"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// clientIDServer is an authorization server whose token endpoint records the
// client_id of every request it answers.
type clientIDServer struct {
	*httptest.Server

	mu        sync.Mutex
	clientIDs []string
}

func newClientIDServer(t *testing.T) *clientIDServer {
	t.Helper()

	s := &clientIDServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pkgoauth.WellKnownAuthorizationServer:
			_ = json.NewEncoder(w).Encode(pkgoauth.Metadata{
				Issuer:                        s.URL,
				AuthorizationEndpoint:         s.URL + "/authorize",
				TokenEndpoint:                 s.URL + "/token",
				CodeChallengeMethodsSupported: []string{"S256"},
			})
		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			s.mu.Lock()
			s.clientIDs = append(s.clientIDs, r.PostForm.Get("client_id"))
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "new-access-token",
				"token_type":    "Bearer",
				"refresh_token": "new-refresh-token",
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *clientIDServer) requestedClientIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.clientIDs...)
}

// TestSignIn_StoresTheClientID locks in that a sign-in stores the client_id
// it authorized and exchanged the code with next to the token.
func TestSignIn_StoresTheClientID(t *testing.T) {
	server := newClientIDServer(t)
	serverURL := "https://mcp.example.com/mcp"

	client, err := NewClient(ClientConfig{
		TokenStoreConfig: TokenStoreConfig{StorageDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	authURL, err := client.StartAuthFlowWithOptions(t.Context(), serverURL, server.URL, nil)
	if err != nil {
		t.Fatalf("StartAuthFlowWithOptions: %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	if got := parsed.Query().Get("client_id"); got != DefaultAgentClientID {
		t.Errorf("expected client_id %q on the authorization URL, got %q", DefaultAgentClientID, got)
	}

	flow := client.currentFlow
	go func() {
		callbackURL := flow.CallbackServer.GetRedirectURI() + "?code=auth-code&state=" + url.QueryEscape(flow.State)
		response, err := http.Get(callbackURL) //nolint:gosec // G107: URL built from the flow's own local callback server
		if err == nil {
			_ = response.Body.Close()
		}
	}()

	if _, err := client.WaitForCallback(t.Context()); err != nil {
		t.Fatalf("WaitForCallback: %v", err)
	}

	if got := server.requestedClientIDs(); len(got) != 1 || got[0] != DefaultAgentClientID {
		t.Errorf("expected the code exchange with client_id %q, got %q", DefaultAgentClientID, got)
	}
	stored := client.tokenStore.GetToken(serverURL)
	if stored == nil {
		t.Fatal("expected the signed-in token in the store")
	}
	if stored.ClientID != DefaultAgentClientID {
		t.Errorf("expected the stored client_id %q, got %q", DefaultAgentClientID, stored.ClientID)
	}
}

// TestSetupOAuthConfig_RefreshUsesTheStoredClientID drives a refresh through
// mcp-go's OAuth handler with the config SetupOAuthConfigWithDir builds: the
// refresh goes out with the client_id the token was stored with, or with
// DefaultAgentClientID for a token file that records none, and the refreshed
// token is stored with that client_id.
func TestSetupOAuthConfig_RefreshUsesTheStoredClientID(t *testing.T) {
	tests := []struct {
		name         string
		storedID     string
		wantClientID string
	}{
		{
			name:         "token stored with a client_id",
			storedID:     "https://registered.example.com/client.json",
			wantClientID: "https://registered.example.com/client.json",
		},
		{
			name:         "token file written before the client_id was recorded",
			storedID:     "",
			wantClientID: DefaultAgentClientID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newClientIDServer(t)
			dir := t.TempDir()

			store, err := NewTokenStore(TokenStoreConfig{StorageDir: dir, FileMode: true})
			if err != nil {
				t.Fatalf("NewTokenStore: %v", err)
			}
			expired := &oauth2.Token{
				AccessToken:  "old-access-token",
				RefreshToken: "old-refresh-token",
				TokenType:    "Bearer",
				Expiry:       time.Now().Add(-time.Hour),
			}
			if err := store.StoreToken(server.URL, server.URL, tt.storedID, expired); err != nil {
				t.Fatalf("StoreToken: %v", err)
			}

			cfg, _, err := SetupOAuthConfigWithDir(server.URL, dir)
			if err != nil {
				t.Fatalf("SetupOAuthConfigWithDir: %v", err)
			}
			cfg.AuthServerMetadataURL = server.URL + pkgoauth.WellKnownAuthorizationServer

			handler := transport.NewOAuthHandler(*cfg)
			handler.SetBaseURL(server.URL)
			if _, err := handler.GetAuthorizationHeader(context.Background()); err != nil {
				t.Fatalf("GetAuthorizationHeader: %v", err)
			}

			if got := server.requestedClientIDs(); len(got) != 1 || got[0] != tt.wantClientID {
				t.Errorf("expected the refresh with client_id %q, got %q", tt.wantClientID, got)
			}

			reread, err := NewTokenStore(TokenStoreConfig{StorageDir: dir, FileMode: true})
			if err != nil {
				t.Fatalf("NewTokenStore: %v", err)
			}
			got := reread.GetToken(server.URL)
			if got == nil || got.AccessToken != "new-access-token" {
				t.Fatalf("expected the refreshed token in the store, got %+v", got)
			}
			if got.ClientID != tt.wantClientID {
				t.Errorf("expected the refreshed token stored with client_id %q, got %q", tt.wantClientID, got.ClientID)
			}
		})
	}
}

func TestStoredToken_RefreshClientID(t *testing.T) {
	if got := (&StoredToken{ClientID: "https://registered.example.com/client.json"}).RefreshClientID(); got != "https://registered.example.com/client.json" {
		t.Errorf("expected the stored client_id, got %q", got)
	}
	if got := (&StoredToken{}).RefreshClientID(); got != DefaultAgentClientID {
		t.Errorf("expected DefaultAgentClientID for a token without a client_id, got %q", got)
	}
}
