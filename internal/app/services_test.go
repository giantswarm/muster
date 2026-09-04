package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/giantswarm/muster/internal/client"
	"github.com/giantswarm/muster/internal/config"
)

func TestInitializeServices(t *testing.T) {
	tests := []struct {
		name          string
		config        *Config
		expectError   bool
		checkServices func(*testing.T, *Services)
	}{
		{
			name: "basic initialization without aggregator",
			config: &Config{
				Debug: true,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Port: 0,
					},
				},
				ConfigPath: config.GetDefaultConfigPathOrPanic(),
			},
			expectError: false,
			checkServices: func(t *testing.T, s *Services) {
				if s.Orchestrator == nil {
					t.Error("Orchestrator should not be nil")
				}
				if s.OrchestratorAPI == nil {
					t.Error("OrchestratorAPI should not be nil")
				}
			},
		},
		{
			name: "initialization with aggregator",
			config: &Config{
				Debug: false,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Port: 8090,
						Host: "localhost",
					},
				},
				ConfigPath: config.GetDefaultConfigPathOrPanic(),
			},
			expectError: false,
			checkServices: func(t *testing.T, s *Services) {
				if s.AggregatorPort != 8090 {
					t.Errorf("AggregatorPort = %d, want 8090", s.AggregatorPort)
				}
			},
		},
		{
			name: "initialization with default aggregator port",
			config: &Config{
				Debug: false,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Port: 0, // Should default to 8080
						Host: "",
					},
				},
				ConfigPath: config.GetDefaultConfigPathOrPanic(),
			},
			expectError: false,
			checkServices: func(t *testing.T, s *Services) {
				// The aggregator port in Services will still be 0,
				// but the actual aggregator service will use 8080
				if s.AggregatorPort != 0 {
					t.Errorf("AggregatorPort in Services = %d, want 0", s.AggregatorPort)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			services, err := InitializeServices(tt.config)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.expectError && tt.checkServices != nil {
				tt.checkServices(t, services)
			}
		})
	}
}

func TestInitializeServices_OrchestratorConfig(t *testing.T) {
	cfg := &Config{
		Debug: false,
		MusterConfig: &config.MusterConfig{
			Aggregator: config.AggregatorConfig{
				Port: 9090,
			},
		},
		ConfigPath: config.GetDefaultConfigPathOrPanic(),
	}

	// We can't easily test the full initialization without mocking orchestrator.New
	// The orchestrator now gets MCPServers from MCPServerManager, not from config
	// This test verifies that the config structure is valid for services initialization
	services, err := InitializeServices(cfg)
	if err != nil {
		t.Fatalf("Failed to initialize services: %v", err)
	}

	// Verify that services were created successfully
	if services.Orchestrator == nil {
		t.Error("Orchestrator should not be nil")
	}
}

// Test that services are created
func TestServices_Creation(t *testing.T) {
	cfg := &Config{
		Debug: false,
		MusterConfig: &config.MusterConfig{
			Aggregator: config.AggregatorConfig{},
		},
		ConfigPath: config.GetDefaultConfigPathOrPanic(),
	}

	services, err := InitializeServices(cfg)
	assert.NoError(t, err)

	// Test that services are created
	assert.NotNil(t, services.Orchestrator)
	assert.NotNil(t, services.OrchestratorAPI)
}

// TestInitializeServices_KubernetesModeNeverFallsBackToFilesystem is the
// regression test for issue #1143. With `kubernetes: true` and an apiserver
// that cannot be reached, muster used to come up silently in filesystem
// mode: the reconciler's informer still watched the apiserver, every existing
// MCPServer CR was looked up in the empty config directory as "not found" and
// its service deleted, and the orchestrator's auto-start listed zero
// definitions. Initialization must fail instead, so the container restarts.
func TestInitializeServices_KubernetesModeNeverFallsBackToFilesystem(t *testing.T) {
	deadAPIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "apiserver not ready", http.StatusServiceUnavailable)
	}))
	t.Cleanup(deadAPIServer.Close)
	pointKubeconfigAt(t, deadAPIServer.URL)

	origBackoff := client.KubernetesClientBackoff
	client.KubernetesClientBackoff = wait.Backoff{Duration: time.Millisecond, Factor: 1, Steps: 2}
	t.Cleanup(func() { client.KubernetesClientBackoff = origBackoff })

	services, err := InitializeServices(&Config{
		MusterConfig: &config.MusterConfig{Kubernetes: true, Namespace: "muster"},
		ConfigPath:   t.TempDir(),
	})

	require.Error(t, err)
	assert.Nil(t, services)
	assert.Contains(t, err.Error(), "kubernetes mode is configured")
	assert.Contains(t, err.Error(), "refusing to fall back to filesystem mode")
}

// The change detector must watch the store the client reads from. In
// filesystem mode that is the config directory, whatever a kubeconfig in the
// environment might point at.
func TestInitializeServices_ReconcilerWatchesTheClientsStore(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "must not be contacted in filesystem mode", http.StatusServiceUnavailable)
	}))
	t.Cleanup(apiServer.Close)
	pointKubeconfigAt(t, apiServer.URL)

	services, err := InitializeServices(&Config{
		MusterConfig: &config.MusterConfig{Kubernetes: false},
		ConfigPath:   t.TempDir(),
	})

	require.NoError(t, err)
	require.NotNil(t, services.ReconcileManager)
	assert.Equal(t, "filesystem", services.ReconcileManager.GetWatchMode())
}

// pointKubeconfigAt makes controller-runtime's config detection resolve to
// server for the duration of the test.
func pointKubeconfigAt(t *testing.T, server string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	kubeconfig := `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: ` + server + `
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: test
`
	require.NoError(t, os.WriteFile(path, []byte(kubeconfig), 0o600))
	t.Setenv("KUBECONFIG", path)
}
