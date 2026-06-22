package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/giantswarm/muster/internal/cli"
)

// eventMap builds a core_events display object as returned by the tool.
func eventMap(ts, reason, name, msg, typ string) map[string]interface{} {
	return map[string]interface{}{
		"timestamp":     ts,
		"resource_type": "Workflow",
		"resource_name": name,
		"namespace":     "default",
		"reason":        reason,
		"message":       msg,
		"type":          typ,
	}
}

// TestInitialFollowLines_ChronologicalOrder verifies that the events returned by
// the initial core_events query (newest-first) are rendered oldest-first so the
// follow stream reads naturally top to bottom.
func TestInitialFollowLines_ChronologicalOrder(t *testing.T) {
	// core_events returns newest-first.
	raw := []interface{}{
		eventMap("2024-01-15 10:00:02", "WorkflowUpdated", "wf", "updated", "Normal"),
		eventMap("2024-01-15 10:00:01", "WorkflowCreated", "wf", "created", "Normal"),
	}

	lines := initialFollowLines(raw, cli.OutputFormatTable)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "WorkflowCreated") || !strings.Contains(lines[1], "WorkflowUpdated") {
		t.Fatalf("expected chronological order [created, updated], got: %v", lines)
	}
}

// TestInitialFollowLines_NonArrayInput verifies tolerance of a non-array result
// (e.g. an error string or empty result) without panicking.
func TestInitialFollowLines_NonArrayInput(t *testing.T) {
	if lines := initialFollowLines("not-an-array", cli.OutputFormatTable); len(lines) != 0 {
		t.Fatalf("expected no lines for non-array input, got: %v", lines)
	}
	if lines := initialFollowLines(nil, cli.OutputFormatTable); len(lines) != 0 {
		t.Fatalf("expected no lines for nil input, got: %v", lines)
	}
}

// TestFormatFollowEvent_RendersAllFields verifies a pushed event map (as
// delivered via MCP notification params) renders the full display line.
func TestFormatFollowEvent_RendersAllFields(t *testing.T) {
	ev := eventMap("2024-01-15 10:00:03", "WorkflowDeleted", "wf", "deleted", "Warning")

	line := formatFollowEvent(ev, cli.OutputFormatTable)
	for _, want := range []string{"2024-01-15 10:00:03", "Workflow", "default", "wf", "WorkflowDeleted", "deleted", "Warning"} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatted line missing %q: %s", want, line)
		}
	}
}

// TestFormatFollowEvent_JSON verifies that the json output format renders each
// streamed event as a single valid JSON object (newline-delimited JSON), so
// `muster events --follow --output json` can be piped into jq.
func TestFormatFollowEvent_JSON(t *testing.T) {
	ev := eventMap("2024-01-15 10:00:03", "WorkflowDeleted", "wf", "deleted", "Warning")

	line := formatFollowEvent(ev, cli.OutputFormatJSON)
	if strings.Contains(line, "\n") {
		t.Fatalf("json follow line must be a single line, got: %s", line)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("json follow line is not valid JSON: %v (%s)", err, line)
	}
	if got["reason"] != "WorkflowDeleted" || got["type"] != "Warning" {
		t.Fatalf("json follow line missing fields: %s", line)
	}
}

// TestFormatFollowEvent_MissingFields verifies missing keys render as empty
// strings rather than panicking or printing Go zero values.
func TestFormatFollowEvent_MissingFields(t *testing.T) {
	line := formatFollowEvent(map[string]interface{}{"reason": "WorkflowCreated"}, cli.OutputFormatTable)
	if !strings.Contains(line, "WorkflowCreated") {
		t.Fatalf("expected reason in line, got: %s", line)
	}
	if strings.Contains(line, "<nil>") || strings.Contains(line, "%!") {
		t.Fatalf("unexpected zero-value formatting in line: %s", line)
	}
}
