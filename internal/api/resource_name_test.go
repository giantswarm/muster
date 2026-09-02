package api

import "testing"

func TestValidateResourceName(t *testing.T) {
	// Path-safe names are accepted, including characters (underscore, uppercase,
	// spaces) that Kubernetes admission would reject — this validator only
	// guards against directory escape and log injection, not DNS-1123
	// conformance.
	valid := []string{"foo", "foo-bar", "foo.bar", "a1b2", "x", "special-chars-workflow_123", "Foo", "foo bar", "..foo", "ünïcode"}
	for _, name := range valid {
		if err := ValidateResourceName(name); err != nil {
			t.Errorf("expected %q to be valid, got: %v", name, err)
		}
	}

	invalid := []string{
		"", "..", ".",
		"../evil", "a/b", "a\\b", "/etc/passwd", "foo/",
		// Control characters: a null byte corrupts the file path, and a newline
		// forges a record in the line-oriented legacy events.log. The bare
		// "x\nforged" case carries no separator on purpose — it fails only if
		// the control-character rule is doing the work, where the realistic
		// forgery payload below would also be caught by the separator check.
		"a\x00b", "x\nforged",
		"x\n[2020-01-01T00:00:00Z] MCPServer default-prometheus: MCPServerDeleted - gone (Normal)",
		"a\rb", "a\tb", "a\x1bb", "a\x7fb",
	}
	for _, name := range invalid {
		if err := ValidateResourceName(name); err == nil {
			t.Errorf("expected %q to be rejected", name)
		}
	}
}
