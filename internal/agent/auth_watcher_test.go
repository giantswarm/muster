package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"muster/internal/agent/oauth"
)

func TestAuthWatcher_NewAuthWatcher(t *testing.T) {
	tmpDir := t.TempDir()
	tokenStore, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	// Create a client (we don't need a real connection for this test)
	client := NewClient("http://localhost:8090/sse", nil, TransportSSE)

	watcher := NewAuthWatcher(client, tokenStore)
	if watcher == nil {
		t.Fatal("Expected non-nil watcher")
	}

	if watcher.pollInterval != DefaultAuthWatcherPollInterval {
		t.Errorf("Expected poll interval %v, got %v", DefaultAuthWatcherPollInterval, watcher.pollInterval)
	}
}

func TestAuthWatcher_WithOptions(t *testing.T) {
	tmpDir := t.TempDir()
	tokenStore, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	client := NewClient("http://localhost:8090/sse", nil, TransportSSE)
	customInterval := 5 * time.Second

	watcher := NewAuthWatcher(client, tokenStore,
		WithPollInterval(customInterval),
	)

	if watcher.pollInterval != customInterval {
		t.Errorf("Expected poll interval %v, got %v", customInterval, watcher.pollInterval)
	}
}

func TestAuthWatcher_DetectNewChallenges(t *testing.T) {
	tmpDir := t.TempDir()
	tokenStore, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	client := NewClient("http://localhost:8090/sse", nil, TransportSSE)
	watcher := NewAuthWatcher(client, tokenStore)

	// Test detecting new challenges from nil old status
	newStatus := &AuthStatusResponse{
		ServerAuths: []ServerAuthStatus{
			{
				ServerName: "server1",
				Status:     "auth_required",
				AuthChallenge: &AuthChallengeInfo{
					Issuer:       "https://dex.example.com",
					AuthToolName: "x_server1_authenticate",
				},
			},
		},
	}

	challenges := watcher.detectNewChallenges(nil, newStatus)
	if len(challenges) != 1 {
		t.Fatalf("Expected 1 challenge, got %d", len(challenges))
	}

	if challenges[0].ServerName != "server1" {
		t.Errorf("Expected server1, got %s", challenges[0].ServerName)
	}

	// Test with old status having same challenge (should not detect again)
	oldStatus := newStatus
	newChallenges := watcher.detectNewChallenges(oldStatus, newStatus)
	if len(newChallenges) != 0 {
		t.Errorf("Expected 0 challenges when status unchanged, got %d", len(newChallenges))
	}
}

func TestAuthWatcher_DetectResolvedChallenges(t *testing.T) {
	tmpDir := t.TempDir()
	tokenStore, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	client := NewClient("http://localhost:8090/sse", nil, TransportSSE)
	watcher := NewAuthWatcher(client, tokenStore)

	// Old status has auth_required
	oldStatus := &AuthStatusResponse{
		ServerAuths: []ServerAuthStatus{
			{
				ServerName: "server1",
				Status:     "auth_required",
			},
		},
	}

	// New status has connected
	newStatus := &AuthStatusResponse{
		ServerAuths: []ServerAuthStatus{
			{
				ServerName: "server1",
				Status:     "connected",
			},
		},
	}

	resolved := watcher.detectResolvedChallenges(oldStatus, newStatus)
	if len(resolved) != 1 {
		t.Fatalf("Expected 1 resolved, got %d", len(resolved))
	}

	if resolved[0] != "server1" {
		t.Errorf("Expected server1, got %s", resolved[0])
	}
}

func TestAuthWatcher_Callbacks(t *testing.T) {
	tmpDir := t.TempDir()
	tokenStore, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	client := NewClient("http://localhost:8090/sse", nil, TransportSSE)

	authCompleteCalled := false
	browserAuthCalled := false

	callbacks := AuthWatcherCallbacks{
		OnAuthComplete: func(serverName string) {
			authCompleteCalled = true
		},
		OnBrowserAuthRequired: func(serverName, authToolName string) {
			browserAuthCalled = true
		},
	}

	watcher := NewAuthWatcher(client, tokenStore, WithCallbacks(callbacks))

	// Simulate a resolved challenge
	oldStatus := &AuthStatusResponse{
		ServerAuths: []ServerAuthStatus{
			{ServerName: "server1", Status: "auth_required"},
		},
	}
	newStatus := &AuthStatusResponse{
		ServerAuths: []ServerAuthStatus{
			{ServerName: "server1", Status: "connected"},
		},
	}

	watcher.lastStatus = oldStatus

	// Manually call checkAuthStatus logic (without the network call)
	resolved := watcher.detectResolvedChallenges(oldStatus, newStatus)
	for _, serverName := range resolved {
		if watcher.callbacks.OnAuthComplete != nil {
			watcher.callbacks.OnAuthComplete(serverName)
		}
	}

	if !authCompleteCalled {
		t.Error("Expected OnAuthComplete callback to be called")
	}

	// Test browser auth callback
	ctx := context.Background()
	challenge := ServerAuthStatus{
		ServerName: "server2",
		Status:     "auth_required",
		AuthChallenge: &AuthChallengeInfo{
			Issuer:       "https://dex.example.com",
			AuthToolName: "x_server2_authenticate",
		},
	}

	watcher.handleNewChallenge(ctx, challenge)

	if !browserAuthCalled {
		t.Error("Expected OnBrowserAuthRequired callback to be called")
	}
}

func TestAuthStatusResponse_Marshaling(t *testing.T) {
	status := AuthStatusResponse{
		MusterAuth: &MusterAuthStatus{
			Authenticated: true,
			User:          "testuser",
			Issuer:        "https://dex.example.com",
		},
		ServerAuths: []ServerAuthStatus{
			{
				ServerName: "mcp-kubernetes",
				Status:     "connected",
			},
			{
				ServerName: "mcp-github",
				Status:     "auth_required",
				AuthChallenge: &AuthChallengeInfo{
					Issuer:       "https://github.com",
					Scope:        "repo",
					AuthToolName: "x_mcp-github_authenticate",
				},
			},
		},
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed AuthStatusResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if parsed.MusterAuth.Authenticated != true {
		t.Error("Expected authenticated to be true")
	}

	if len(parsed.ServerAuths) != 2 {
		t.Errorf("Expected 2 server auths, got %d", len(parsed.ServerAuths))
	}

	if parsed.ServerAuths[1].AuthChallenge == nil {
		t.Fatal("Expected auth challenge for second server")
	}

	if parsed.ServerAuths[1].AuthChallenge.Issuer != "https://github.com" {
		t.Errorf("Expected issuer https://github.com, got %s", parsed.ServerAuths[1].AuthChallenge.Issuer)
	}
}
