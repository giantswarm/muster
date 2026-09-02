package workflow

import (
	"context"
	"strings"
	"testing"
)

// TestUnsafeName_RejectedByValidateAndCreate covers the agreement between
// workflow_validate and workflow_create for caller-supplied names that are not
// a safe path segment. Path safety is also enforced in the filesystem store,
// but a name checked only there makes validate call "../../evil" a good
// definition and the create that follows fail.
func TestUnsafeName_RejectedByValidateAndCreate(t *testing.T) {
	names := map[string]string{
		"traversal":    "../../evil",
		"absolute":     "/etc/muster-evil",
		"separator":    "foo/bar",
		"dot-dot":      "..",
		"control-char": "evil\nname",
	}

	for label, name := range names {
		for _, tool := range []string{"workflow_validate", "workflow_create"} {
			t.Run(label+"/"+tool, func(t *testing.T) {
				sa := &stubMusterClient{existing: existingWorkflow()}
				adapter := NewAdapterWithClient(sa, "test-ns", nil, nil, "")
				t.Cleanup(adapter.stopGC)

				// workflow_create surfaces a validation failure as a Go error,
				// workflow_validate as an error result — either way the call
				// must fail and say why.
				result, err := adapter.ExecuteTool(context.Background(), tool, workflowArgs(name))
				var text string
				switch {
				case err != nil:
					text = err.Error()
				case result.IsError:
					text = resultText(t, result)
				default:
					t.Fatalf("%s accepted unsafe name %q: %s", tool, name, resultText(t, result))
				}
				if !strings.Contains(text, "invalid name") {
					t.Errorf("rejection %q does not explain the name is invalid", text)
				}
				if writes := len(sa.created) + len(sa.updated) + len(sa.deleted); writes != 0 {
					t.Fatalf("%s wrote despite the rejection: created=%v updated=%v", tool, sa.created, sa.updated)
				}
			})
		}
	}
}
