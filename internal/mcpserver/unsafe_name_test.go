package mcpserver

import (
	"context"
	"strings"
	"testing"
)

// TestUnsafeName_RejectedByValidateAndCreate covers the agreement between
// mcpserver_validate and mcpserver_create for caller-supplied names that are
// not a safe path segment. Path safety is also enforced in the filesystem
// store, but a name checked only there makes validate call "../../evil" a good
// definition and the create that follows fail — an agent that validates before
// creating gets contradictory answers.
func TestUnsafeName_RejectedByValidateAndCreate(t *testing.T) {
	names := map[string]string{
		"traversal":    "../../evil",
		"absolute":     "/etc/muster-evil",
		"separator":    "foo/bar",
		"dot-dot":      "..",
		"control-char": "evil\nname",
	}

	for label, name := range names {
		for _, tool := range []string{"mcpserver_validate", "mcpserver_create"} {
			t.Run(label+"/"+tool, func(t *testing.T) {
				sa := &stubMusterClient{filesystem: true, existing: existingServer()}
				adapter := NewAdapterWithClient(sa, "test-ns")

				args := map[string]interface{}{
					"name": name,
					"type": "streamable-http",
					"url":  "http://localhost:1/mcp",
				}
				result, err := adapter.ExecuteTool(context.Background(), tool, args)
				if err != nil {
					t.Fatalf("ExecuteTool: %v", err)
				}
				if !result.IsError {
					t.Fatalf("%s accepted unsafe name %q: %s", tool, name, resultText(t, result))
				}
				if text := resultText(t, result); !strings.Contains(text, "invalid name") {
					t.Errorf("rejection %q does not explain the name is invalid", text)
				}
				if writes := len(sa.created) + len(sa.updated) + len(sa.deleted); writes != 0 {
					t.Fatalf("%s wrote despite the rejection: created=%v updated=%v", tool, sa.created, sa.updated)
				}
			})
		}
	}
}
