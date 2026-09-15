package cli

import (
	"testing"

	"github.com/giantswarm/muster/internal/metatools"
)

// TestNewMCPToolInfo covers the server a listed tool is attributed to: the
// server list_tools reports, the kind for muster's own tools, and -- for an
// aggregator that reports neither -- the exposed name.
func TestNewMCPToolInfo(t *testing.T) {
	tests := []struct {
		name       string
		entry      metatools.ToolInfo
		wantServer string
		wantText   string
	}{
		{
			name:       "aggregated tool carries the server it belongs to",
			entry:      metatools.ToolInfo{Name: "x_files_read_file", Description: "Read a file", Server: "files", Kind: "tool"},
			wantServer: "files",
			wantText:   "Read a file",
		},
		{
			name:       "server name wins over a divergent tool prefix",
			entry:      metatools.ToolInfo{Name: "x_pro_issues", Summary: "List issues.", Server: "gazelle-mcp-pro", Kind: "tool"},
			wantServer: "gazelle-mcp-pro",
			wantText:   "List issues.",
		},
		{
			name:       "family tool carries the family name",
			entry:      metatools.ToolInfo{Name: "x_k8s_get_pods", Server: "k8s", Kind: "tool"},
			wantServer: "k8s",
		},
		{
			name:       "core tool is grouped under its kind",
			entry:      metatools.ToolInfo{Name: "core_service_list", Kind: "core"},
			wantServer: "core",
		},
		{
			name:       "workflow tool is grouped under its kind",
			entry:      metatools.ToolInfo{Name: "workflow_deploy", Kind: "workflow"},
			wantServer: "workflow",
		},
		// An aggregator predating the server and kind fields.
		{
			name:       "no attribution: exposed prefix of an aggregated tool",
			entry:      metatools.ToolInfo{Name: "x_files_read_file"},
			wantServer: "files",
		},
		{
			name:       "no attribution: kind tool without a server falls back to the name",
			entry:      metatools.ToolInfo{Name: "x_files_read_file", Kind: "tool"},
			wantServer: "files",
		},
		{
			name:       "no attribution: core tool by its prefix",
			entry:      metatools.ToolInfo{Name: "core_service_list"},
			wantServer: "core",
		},
		{
			name:       "no attribution: workflow tool by its prefix",
			entry:      metatools.ToolInfo{Name: "workflow_deploy"},
			wantServer: "workflow",
		},
		{
			name:       "no attribution: a name without a prefix has no server",
			entry:      metatools.ToolInfo{Name: "ping"},
			wantServer: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newMCPToolInfo(tt.entry)
			if got.Name != tt.entry.Name {
				t.Errorf("Name = %q, want %q", got.Name, tt.entry.Name)
			}
			if got.Server != tt.wantServer {
				t.Errorf("Server = %q, want %q", got.Server, tt.wantServer)
			}
			if got.Description != tt.wantText {
				t.Errorf("Description = %q, want %q", got.Description, tt.wantText)
			}
		})
	}
}
