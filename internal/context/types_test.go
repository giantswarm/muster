package context

import (
	"testing"
)

func TestValidateContextName(t *testing.T) {
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
		{"too long", "a" + string(make([]byte, 63)), true},
		{"max length valid", "a" + string(make([]byte, 61)) + "b", false},
	}

	// Fix the too long test case - create proper strings
	tests[12].input = string(make([]byte, 64)) // all zeros won't match pattern
	for i := range tests[12].input {
		if i < 64 {
			tests[12].input = "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijkl" // 64 chars
		}
	}
	tests[13].input = "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijk" // 63 chars

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
