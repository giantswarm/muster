package context

import (
	"strings"
	"testing"
)

func TestValidateContextName(t *testing.T) {
	// Create test strings for length validation
	tooLong := strings.Repeat("a", 64)   // 64 chars - exceeds max
	maxLength := strings.Repeat("a", 63) // 63 chars - exactly at max

	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{"valid simple name", "production", false},
		{"valid with hyphen", "my-context", false},
		{"valid with numbers", "context1", false},
		{"valid single char", "a", false},
		{"valid complex", "my-prod-context-01", false},
		{"empty name", "", true},
		{"starts with hyphen", "-context", true},
		{"ends with hyphen", "context-", true},
		{"uppercase letters", "Production", true},
		{"contains underscore", "my_context", true},
		{"contains space", "my context", true},
		{"contains dot", "my.context", true},
		{"too long", tooLong, true},
		{"max length valid", maxLength, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateContextName(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateContextName(%q) error = %v, wantError %v", tt.input, err, tt.wantError)
			}
		})
	}
}

func TestContextConfig_GetContext(t *testing.T) {
	config := &ContextConfig{
		Contexts: []Context{
			{Name: "prod", Endpoint: "https://prod.example.com/mcp"},
			{Name: "dev", Endpoint: "https://dev.example.com/mcp"},
		},
	}

	t.Run("found", func(t *testing.T) {
		ctx := config.GetContext("prod")
		if ctx == nil {
			t.Fatal("expected context, got nil")
		}
		if ctx.Endpoint != "https://prod.example.com/mcp" {
			t.Errorf("got endpoint %q, want %q", ctx.Endpoint, "https://prod.example.com/mcp")
		}
	})

	t.Run("not found", func(t *testing.T) {
		ctx := config.GetContext("staging")
		if ctx != nil {
			t.Errorf("expected nil, got %v", ctx)
		}
	})
}

func TestContextConfig_HasContext(t *testing.T) {
	config := &ContextConfig{
		Contexts: []Context{
			{Name: "prod", Endpoint: "https://prod.example.com/mcp"},
		},
	}

	if !config.HasContext("prod") {
		t.Error("expected HasContext to return true for existing context")
	}
	if config.HasContext("dev") {
		t.Error("expected HasContext to return false for non-existing context")
	}
}

func TestContextConfig_AddOrUpdateContext(t *testing.T) {
	t.Run("add new context", func(t *testing.T) {
		config := &ContextConfig{}
		config.AddOrUpdateContext(Context{Name: "prod", Endpoint: "https://prod.example.com/mcp"})

		if len(config.Contexts) != 1 {
			t.Fatalf("expected 1 context, got %d", len(config.Contexts))
		}
		if config.Contexts[0].Name != "prod" {
			t.Errorf("got name %q, want %q", config.Contexts[0].Name, "prod")
		}
	})

	t.Run("update existing context", func(t *testing.T) {
		config := &ContextConfig{
			Contexts: []Context{
				{Name: "prod", Endpoint: "https://old.example.com/mcp"},
			},
		}
		config.AddOrUpdateContext(Context{Name: "prod", Endpoint: "https://new.example.com/mcp"})

		if len(config.Contexts) != 1 {
			t.Fatalf("expected 1 context, got %d", len(config.Contexts))
		}
		if config.Contexts[0].Endpoint != "https://new.example.com/mcp" {
			t.Errorf("got endpoint %q, want %q", config.Contexts[0].Endpoint, "https://new.example.com/mcp")
		}
	})
}

func TestContextConfig_RemoveContext(t *testing.T) {
	t.Run("remove existing context", func(t *testing.T) {
		config := &ContextConfig{
			CurrentContext: "prod",
			Contexts: []Context{
				{Name: "prod", Endpoint: "https://prod.example.com/mcp"},
				{Name: "dev", Endpoint: "https://dev.example.com/mcp"},
			},
		}
		removed := config.RemoveContext("prod")

		if !removed {
			t.Error("expected RemoveContext to return true")
		}
		if len(config.Contexts) != 1 {
			t.Fatalf("expected 1 context, got %d", len(config.Contexts))
		}
		if config.CurrentContext != "" {
			t.Errorf("expected current context to be cleared, got %q", config.CurrentContext)
		}
	})

	t.Run("remove non-existing context", func(t *testing.T) {
		config := &ContextConfig{
			Contexts: []Context{
				{Name: "prod", Endpoint: "https://prod.example.com/mcp"},
			},
		}
		removed := config.RemoveContext("staging")

		if removed {
			t.Error("expected RemoveContext to return false for non-existing context")
		}
		if len(config.Contexts) != 1 {
			t.Fatalf("expected 1 context, got %d", len(config.Contexts))
		}
	})
}
