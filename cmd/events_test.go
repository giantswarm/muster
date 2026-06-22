package cmd

import (
	"strings"
	"testing"
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

// TestFollowTracker_DeduplicatesAcrossPolls verifies that the --follow loop's
// tracker prints each event exactly once even when consecutive polls return
// overlapping windows (the common case, since the tool returns the most recent
// events newest-first on every call).
func TestFollowTracker_DeduplicatesAcrossPolls(t *testing.T) {
	tracker := newFollowTracker()

	// First poll: two existing events, newest-first as the tool returns them.
	poll1 := []interface{}{
		eventMap("2024-01-15 10:00:02", "WorkflowUpdated", "wf", "updated", "Normal"),
		eventMap("2024-01-15 10:00:01", "WorkflowCreated", "wf", "created", "Normal"),
	}
	lines := tracker.newLines(poll1)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines on first poll, got %d: %v", len(lines), lines)
	}
	// Output must be chronological (oldest first): Created (10:00:01) before
	// Updated (10:00:02).
	if !strings.Contains(lines[0], "WorkflowCreated") || !strings.Contains(lines[1], "WorkflowUpdated") {
		t.Fatalf("expected chronological order [created, updated], got: %v", lines)
	}

	// Second poll: same two events plus one new event. Only the new one prints.
	poll2 := []interface{}{
		eventMap("2024-01-15 10:00:03", "WorkflowDeleted", "wf", "deleted", "Normal"),
		eventMap("2024-01-15 10:00:02", "WorkflowUpdated", "wf", "updated", "Normal"),
		eventMap("2024-01-15 10:00:01", "WorkflowCreated", "wf", "created", "Normal"),
	}
	lines = tracker.newLines(poll2)
	if len(lines) != 1 {
		t.Fatalf("expected 1 new line on second poll, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "WorkflowDeleted") {
		t.Fatalf("expected the new WorkflowDeleted event, got: %v", lines)
	}

	// Third poll with no changes prints nothing.
	if lines := tracker.newLines(poll2); len(lines) != 0 {
		t.Fatalf("expected no new lines on unchanged poll, got: %v", lines)
	}
}

// TestFollowTracker_NonArrayInput verifies the tracker tolerates a non-array
// JSON result (e.g. an error string) without panicking.
func TestFollowTracker_NonArrayInput(t *testing.T) {
	tracker := newFollowTracker()
	if lines := tracker.newLines("not-an-array"); lines != nil {
		t.Fatalf("expected nil lines for non-array input, got: %v", lines)
	}
	if lines := tracker.newLines(nil); lines != nil {
		t.Fatalf("expected nil lines for nil input, got: %v", lines)
	}
}
