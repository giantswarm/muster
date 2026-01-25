package teleport

import (
	"context"
	"testing"
	"time"

	"muster/internal/api"
)

func TestNewAdapter(t *testing.T) {
	adapter := NewAdapter()

	if adapter == nil {
		t.Fatal("Expected non-nil adapter")
	}

	if adapter.providers == nil {
		t.Error("Expected non-nil providers map")
	}
}

func TestNewAdapterWithDefaults(t *testing.T) {
	defaultConfig := TeleportConfig{
		CertFile:      "custom.crt",
		KeyFile:       "custom.key",
		CAFile:        "custom-ca.crt",
		WatchInterval: 60 * time.Second,
	}

	adapter := NewAdapterWithDefaults(defaultConfig)

	if adapter == nil {
		t.Fatal("Expected non-nil adapter")
	}

	if adapter.defaultConfig.CertFile != "custom.crt" {
		t.Errorf("Expected CertFile to be 'custom.crt', got %s", adapter.defaultConfig.CertFile)
	}

	if adapter.defaultConfig.WatchInterval != 60*time.Second {
		t.Errorf("Expected WatchInterval to be 60s, got %v", adapter.defaultConfig.WatchInterval)
	}
}

func TestAdapter_GetHTTPClientForIdentity(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	// Get HTTP client for the identity
	client, err := adapter.GetHTTPClientForIdentity(dir)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity failed: %v", err)
	}

	if client == nil {
		t.Fatal("Expected non-nil HTTP client")
	}

	// Getting client again should return from cache
	client2, err := adapter.GetHTTPClientForIdentity(dir)
	if err != nil {
		t.Fatalf("Second GetHTTPClientForIdentity failed: %v", err)
	}

	// The provider should be reused, so the client should be the same
	if client != client2 {
		t.Error("Expected same HTTP client from cache")
	}
}

func TestAdapter_GetHTTPClientForIdentity_MissingCerts(t *testing.T) {
	dir := t.TempDir()
	// Don't create certificates

	adapter := NewAdapter()
	defer adapter.Close()

	// Getting HTTP client should fail when certs are missing
	_, err := adapter.GetHTTPClientForIdentity(dir)
	if err == nil {
		t.Error("Expected error when certificates are missing")
	}
}

func TestAdapter_GetHTTPTransportForIdentity(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	transport, err := adapter.GetHTTPTransportForIdentity(dir)
	if err != nil {
		t.Fatalf("GetHTTPTransportForIdentity failed: %v", err)
	}

	if transport == nil {
		t.Fatal("Expected non-nil transport")
	}

	if transport.TLSClientConfig == nil {
		t.Error("Expected non-nil TLSClientConfig")
	}
}

func TestAdapter_GetClientProvider(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	provider, err := adapter.GetClientProvider(dir)
	if err != nil {
		t.Fatalf("GetClientProvider failed: %v", err)
	}

	if provider == nil {
		t.Fatal("Expected non-nil provider")
	}

	if !provider.IsLoaded() {
		t.Error("Expected provider to have loaded certificates")
	}
}

func TestAdapter_GetProviderStatus(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	// First, create the provider
	_, err := adapter.GetHTTPClientForIdentity(dir)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity failed: %v", err)
	}

	// Now get the status
	status, err := adapter.GetProviderStatus(dir)
	if err != nil {
		t.Fatalf("GetProviderStatus failed: %v", err)
	}

	if status == nil {
		t.Fatal("Expected non-nil status")
	}

	if !status.Loaded {
		t.Error("Expected status.Loaded to be true")
	}
}

func TestAdapter_GetProviderStatus_NotFound(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	_, err := adapter.GetProviderStatus("/nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent provider")
	}
}

func TestAdapter_ListProviders(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	createTestCertificates(t, dir1)
	createTestCertificates(t, dir2)

	adapter := NewAdapter()
	defer adapter.Close()

	// Initially no providers
	providers := adapter.ListProviders()
	if len(providers) != 0 {
		t.Errorf("Expected 0 providers initially, got %d", len(providers))
	}

	// Add providers
	_, _ = adapter.GetHTTPClientForIdentity(dir1)
	_, _ = adapter.GetHTTPClientForIdentity(dir2)

	providers = adapter.ListProviders()
	if len(providers) != 2 {
		t.Errorf("Expected 2 providers, got %d", len(providers))
	}
}

func TestAdapter_ReloadProvider(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	// First, create the provider
	_, err := adapter.GetHTTPClientForIdentity(dir)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity failed: %v", err)
	}

	// Reload should succeed
	err = adapter.ReloadProvider(dir)
	if err != nil {
		t.Fatalf("ReloadProvider failed: %v", err)
	}
}

func TestAdapter_ReloadProvider_NotFound(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	err := adapter.ReloadProvider("/nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent provider")
	}
}

func TestAdapter_RemoveProvider(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	// Create provider
	_, err := adapter.GetHTTPClientForIdentity(dir)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity failed: %v", err)
	}

	// Verify it exists
	providers := adapter.ListProviders()
	if len(providers) != 1 {
		t.Fatalf("Expected 1 provider, got %d", len(providers))
	}

	// Remove it
	err = adapter.RemoveProvider(dir)
	if err != nil {
		t.Fatalf("RemoveProvider failed: %v", err)
	}

	// Verify it's gone
	providers = adapter.ListProviders()
	if len(providers) != 0 {
		t.Errorf("Expected 0 providers after removal, got %d", len(providers))
	}
}

func TestAdapter_RemoveProvider_NotFound(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	// Removing nonexistent provider should not error
	err := adapter.RemoveProvider("/nonexistent")
	if err != nil {
		t.Errorf("RemoveProvider for nonexistent should not error: %v", err)
	}
}

func TestAdapter_Close(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	createTestCertificates(t, dir1)
	createTestCertificates(t, dir2)

	adapter := NewAdapter()

	// Create multiple providers
	_, _ = adapter.GetHTTPClientForIdentity(dir1)
	_, _ = adapter.GetHTTPClientForIdentity(dir2)

	// Close should clean up all providers
	err := adapter.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify providers are gone
	providers := adapter.ListProviders()
	if len(providers) != 0 {
		t.Errorf("Expected 0 providers after close, got %d", len(providers))
	}
}

func TestAdapter_MultipleIdentities(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	createTestCertificates(t, dir1)
	createTestCertificates(t, dir2)

	adapter := NewAdapter()
	defer adapter.Close()

	// Get clients for different identities
	client1, err := adapter.GetHTTPClientForIdentity(dir1)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity for dir1 failed: %v", err)
	}

	client2, err := adapter.GetHTTPClientForIdentity(dir2)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity for dir2 failed: %v", err)
	}

	// Different identities should have different clients
	if client1 == client2 {
		t.Error("Expected different HTTP clients for different identities")
	}
}

func TestAdapter_GetHTTPClientForIdentity_PathTraversal(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	// Path traversal should be rejected
	_, err := adapter.GetHTTPClientForIdentity("/var/run/../etc/passwd")
	if err == nil {
		t.Error("Expected error for path traversal attempt")
	}
}

func TestAdapter_GetHTTPClientForIdentity_RelativePath(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	// Relative paths should be rejected
	_, err := adapter.GetHTTPClientForIdentity("relative/path")
	if err == nil {
		t.Error("Expected error for relative path")
	}
}

func TestAdapter_GetHTTPClientForConfig_InvalidAppName(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	ctx := context.Background()

	tests := []struct {
		name    string
		appName string
	}{
		{"starts with hyphen", "-invalid"},
		{"contains newline", "app\nname"},
		{"contains colon", "app:name"},
		{"contains slash", "app/name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := api.TeleportClientConfig{
				IdentityDir: dir,
				AppName:     tt.appName,
			}
			_, err := adapter.GetHTTPClientForConfig(ctx, config)
			if err == nil {
				t.Errorf("Expected error for invalid app name: %s", tt.appName)
			}
		})
	}
}

func TestAdapter_GetHTTPClientForConfig_ValidAppName(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	ctx := context.Background()

	tests := []struct {
		name    string
		appName string
	}{
		{"simple", "mcp-kubernetes"},
		{"with dots", "app.name.here"},
		{"with underscores", "app_name"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := api.TeleportClientConfig{
				IdentityDir: dir,
				AppName:     tt.appName,
			}
			_, err := adapter.GetHTTPClientForConfig(ctx, config)
			if err != nil {
				t.Errorf("Unexpected error for valid app name %q: %v", tt.appName, err)
			}
		})
	}
}
