package reconciler

import (
	"testing"
	"time"
)

func TestIsValidResourceType(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		want         bool
	}{
		{
			name:         "valid MCPServer",
			resourceType: "MCPServer",
			want:         true,
		},
		{
			name:         "valid ServiceClass",
			resourceType: "ServiceClass",
			want:         true,
		},
		{
			name:         "valid Workflow",
			resourceType: "Workflow",
			want:         true,
		},
		{
			name:         "invalid empty string",
			resourceType: "",
			want:         false,
		},
		{
			name:         "invalid type",
			resourceType: "InvalidType",
			want:         false,
		},
		{
			name:         "case sensitive - lowercase",
			resourceType: "mcpserver",
			want:         false,
		},
		{
			name:         "SQL injection attempt",
			resourceType: "MCPServer'; DROP TABLE resources;--",
			want:         false,
		},
		{
			name:         "path traversal attempt",
			resourceType: "../../../etc/passwd",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidResourceType(tt.resourceType); got != tt.want {
				t.Errorf("IsValidResourceType(%q) = %v, want %v", tt.resourceType, got, tt.want)
			}
		})
	}
}

func TestSanitizeErrorMessage(t *testing.T) {
	tests := []struct {
		name    string
		errMsg  string
		wantNot []string // Strings that should NOT appear in output
		want    string   // Optional: exact expected output (empty to skip exact check)
	}{
		{
			name:   "empty string",
			errMsg: "",
			want:   "",
		},
		{
			name:    "absolute file path",
			errMsg:  "failed to read file /home/user/secrets/config.yaml",
			wantNot: []string{"/home/user/secrets/"},
		},
		{
			name:    "multiple paths",
			errMsg:  "error: /var/lib/muster/data.json not found, also check /etc/muster/config",
			wantNot: []string{"/var/lib/muster/", "/etc/muster/"},
		},
		{
			name:    "bearer token",
			errMsg:  "auth failed with bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0",
			wantNot: []string{"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"},
		},
		{
			name:    "password in error",
			errMsg:  "connection failed: password=supersecret123 host=localhost",
			wantNot: []string{"supersecret123"},
		},
		{
			name:    "apikey in error",
			errMsg:  "API call failed: apikey=sk_live_abcdef123456789",
			wantNot: []string{"sk_live_abcdef123456789"},
		},
		{
			name:   "normal error message unchanged",
			errMsg: "resource not found: MCPServer prometheus",
			want:   "resource not found: MCPServer prometheus",
		},
		{
			name:   "kubernetes style error",
			errMsg: "MCPServer.muster.giantswarm.io \"prometheus\" not found",
			want:   "MCPServer.muster.giantswarm.io \"prometheus\" not found",
		},
		{
			name:    "long base64 string (potential secret)",
			errMsg:  "failed with data: aVeryLongBase64EncodedSecretValueThatShouldBeRedactedBecauseItMightBeASensitiveToken==",
			wantNot: []string{"aVeryLongBase64EncodedSecretValueThatShouldBeRedactedBecauseItMightBeASensitiveToken"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeErrorMessage(tt.errMsg)

			// Check exact match if specified
			if tt.want != "" && got != tt.want {
				t.Errorf("SanitizeErrorMessage() = %q, want %q", got, tt.want)
			}

			// Check that sensitive strings are not present
			for _, notWant := range tt.wantNot {
				if contains(got, notWant) {
					t.Errorf("SanitizeErrorMessage() = %q, should not contain %q", got, notWant)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && (s == substr || containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestManagerConfigDefaults(t *testing.T) {
	// Create manager with empty config to test defaults
	manager := NewManager(ManagerConfig{})

	// Verify defaults are set
	if manager.config.WorkerCount != 2 {
		t.Errorf("default WorkerCount = %d, want 2", manager.config.WorkerCount)
	}

	if manager.config.MaxRetries != 5 {
		t.Errorf("default MaxRetries = %d, want 5", manager.config.MaxRetries)
	}

	if manager.config.InitialBackoff != time.Second {
		t.Errorf("default InitialBackoff = %v, want %v", manager.config.InitialBackoff, time.Second)
	}

	if manager.config.MaxBackoff != 5*time.Minute {
		t.Errorf("default MaxBackoff = %v, want %v", manager.config.MaxBackoff, 5*time.Minute)
	}

	if manager.config.DebounceInterval != 500*time.Millisecond {
		t.Errorf("default DebounceInterval = %v, want %v", manager.config.DebounceInterval, 500*time.Millisecond)
	}

	if manager.config.ReconcileTimeout != 30*time.Second {
		t.Errorf("default ReconcileTimeout = %v, want %v", manager.config.ReconcileTimeout, 30*time.Second)
	}
}

func TestManagerConfigCustomTimeout(t *testing.T) {
	customTimeout := 60 * time.Second
	manager := NewManager(ManagerConfig{
		ReconcileTimeout: customTimeout,
	})

	if manager.config.ReconcileTimeout != customTimeout {
		t.Errorf("ReconcileTimeout = %v, want %v", manager.config.ReconcileTimeout, customTimeout)
	}
}

func TestValidResourceTypesCompleteness(t *testing.T) {
	// Ensure ValidResourceTypes contains all expected types
	expectedTypes := []ResourceType{
		ResourceTypeMCPServer,
		ResourceTypeServiceClass,
		ResourceTypeWorkflow,
	}

	for _, rt := range expectedTypes {
		if !ValidResourceTypes[rt] {
			t.Errorf("ValidResourceTypes missing %s", rt)
		}
	}

	// Ensure the count matches (no extras, no missing)
	if len(ValidResourceTypes) != len(expectedTypes) {
		t.Errorf("ValidResourceTypes has %d entries, expected %d", len(ValidResourceTypes), len(expectedTypes))
	}
}

