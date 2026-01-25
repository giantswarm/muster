package teleport

import (
	"strings"
	"testing"
)

func TestValidateAppName(t *testing.T) {
	tests := []struct {
		name      string
		appName   string
		wantError bool
	}{
		{"empty is valid", "", false},
		{"simple name", "mcp-kubernetes", false},
		{"with dots", "app.name.here", false},
		{"with underscores", "app_name", false},
		{"alphanumeric", "app123", false},
		{"starts with number", "123app", false},
		{"mixed", "my-app_v1.0", false},

		// Invalid cases
		{"starts with hyphen", "-invalid", true},
		{"starts with dot", ".invalid", true},
		{"starts with underscore", "_invalid", true},
		{"contains space", "app name", true},
		{"contains newline", "app\nname", true},
		{"contains colon", "app:name", true},
		{"contains slash", "app/name", true},
		{"too long", strings.Repeat("a", 254), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAppName(tt.appName)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateAppName(%q) error = %v, wantError %v", tt.appName, err, tt.wantError)
			}
		})
	}
}

func TestValidateIdentityDir(t *testing.T) {
	tests := []struct {
		name      string
		dir       string
		wantError bool
		wantPath  string
	}{
		{"valid absolute path", "/var/run/tbot/identity", false, "/var/run/tbot/identity"},
		{"valid with trailing slash", "/var/run/tbot/identity/", false, "/var/run/tbot/identity"},
		{"root path", "/", false, "/"},

		// Invalid cases
		{"empty", "", true, ""},
		{"relative path", "relative/path", true, ""},
		{"path with traversal", "/var/run/../etc/passwd", true, ""},
		{"path with double dot", "/var/run/tbot/../identity", true, ""},
		{"just dots", "..", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateIdentityDir(tt.dir)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateIdentityDir(%q) error = %v, wantError %v", tt.dir, err, tt.wantError)
			}
			if !tt.wantError && got != tt.wantPath {
				t.Errorf("ValidateIdentityDir(%q) = %q, want %q", tt.dir, got, tt.wantPath)
			}
		})
	}
}

func TestValidateNamespace(t *testing.T) {
	// Save original and restore after test
	original := AllowedNamespaces
	defer func() { AllowedNamespaces = original }()

	t.Run("with allowed list", func(t *testing.T) {
		AllowedNamespaces = []string{"teleport-system", "muster-system"}

		tests := []struct {
			namespace string
			wantError bool
		}{
			{"teleport-system", false},
			{"muster-system", false},
			{"default", true},
			{"kube-system", true},
			{"", true},
		}

		for _, tt := range tests {
			err := ValidateNamespace(tt.namespace)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateNamespace(%q) error = %v, wantError %v", tt.namespace, err, tt.wantError)
			}
		}
	})

	t.Run("without allowed list", func(t *testing.T) {
		AllowedNamespaces = []string{}

		// All non-empty namespaces should be allowed
		tests := []struct {
			namespace string
			wantError bool
		}{
			{"teleport-system", false},
			{"default", false},
			{"kube-system", false},
			{"any-namespace", false},
			{"", true}, // Empty is still invalid
		}

		for _, tt := range tests {
			err := ValidateNamespace(tt.namespace)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateNamespace(%q) error = %v, wantError %v", tt.namespace, err, tt.wantError)
			}
		}
	})
}

func TestValidateNamespaceWithOwner(t *testing.T) {
	// Save original and restore after test
	original := AllowedNamespaces
	defer func() { AllowedNamespaces = original }()

	AllowedNamespaces = []string{"teleport-system"}

	tests := []struct {
		name           string
		namespace      string
		ownerNamespace string
		wantError      bool
	}{
		{"owner namespace always allowed", "my-namespace", "my-namespace", false},
		{"allowed list namespace", "teleport-system", "other", false},
		{"not in list and not owner", "unauthorized", "my-namespace", true},
		{"empty namespace", "", "my-namespace", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNamespaceWithOwner(tt.namespace, tt.ownerNamespace)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateNamespaceWithOwner(%q, %q) error = %v, wantError %v",
					tt.namespace, tt.ownerNamespace, err, tt.wantError)
			}
		})
	}
}

func TestValidateSecretName(t *testing.T) {
	tests := []struct {
		name      string
		secret    string
		wantError bool
	}{
		{"valid simple", "my-secret", false},
		{"valid with dots", "tbot-identity.output", false},
		{"valid starts with number", "1secret", false},

		// Invalid cases
		{"empty", "", true},
		{"uppercase", "MySecret", true},
		{"starts with hyphen", "-secret", true},
		{"starts with dot", ".secret", true},
		{"contains underscore", "my_secret", true},
		{"too long", strings.Repeat("a", 254), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSecretName(tt.secret)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateSecretName(%q) error = %v, wantError %v", tt.secret, err, tt.wantError)
			}
		})
	}
}
