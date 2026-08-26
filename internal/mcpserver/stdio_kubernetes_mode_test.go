package mcpserver

import (
	"context"
	"strings"
	"testing"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// stdioServer is the definition the gate is about: a command that would be
// spawned as a subprocess of whatever process muster runs in.
func stdioServer() *musterv1alpha1.MCPServer {
	obj := &musterv1alpha1.MCPServer{}
	obj.Name = "stdio-server"
	obj.Namespace = "test-ns"
	obj.Spec.Type = "stdio"
	obj.Spec.Command = "echo"
	return obj
}

// TestStdioAdmission_RejectedInKubernetesMode covers the three tools that read
// a type from the caller: create and update must fail the call, and validate
// must report the same rejection so a caller can check before writing.
func TestStdioAdmission_RejectedInKubernetesMode(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"mcpserver_create":   {"name": "stdio-server", "type": "stdio", "command": "echo"},
		"mcpserver_update":   {"name": "test-server", "type": "stdio", "command": "echo"},
		"mcpserver_validate": {"name": "stdio-server", "type": "stdio", "command": "echo"},
	}

	for tool, args := range cases {
		t.Run(tool, func(t *testing.T) {
			sa := &stubMusterClient{existing: existingServer()}
			adapter := NewAdapterWithClient(sa, "test-ns")

			result, err := adapter.ExecuteTool(context.Background(), tool, args)
			if err != nil {
				t.Fatalf("ExecuteTool: %v", err)
			}
			if !result.IsError {
				t.Fatalf("%s accepted stdio in Kubernetes mode: %s", tool, resultText(t, result))
			}
			text := resultText(t, result)
			// The message has to name the alternative, not just say no.
			for _, want := range []string{"not supported in Kubernetes mode", "streamable-http", "sse", "own workload"} {
				if !strings.Contains(text, want) {
					t.Errorf("rejection %q does not mention %q", text, want)
				}
			}
			if writes := len(sa.created) + len(sa.updated) + len(sa.deleted); writes != 0 {
				t.Fatalf("%s wrote despite the rejection: created=%v updated=%v", tool, sa.created, sa.updated)
			}
		})
	}
}

// TestStdioAdmission_AcceptedInFilesystemMode pins the other half: the local
// CLI is exactly where spawning subprocesses is the point.
func TestStdioAdmission_AcceptedInFilesystemMode(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"mcpserver_create":   {"name": "stdio-server", "type": "stdio", "command": "echo"},
		"mcpserver_update":   {"name": "stdio-server", "type": "stdio", "command": "echo2"},
		"mcpserver_validate": {"name": "stdio-server", "type": "stdio", "command": "echo"},
	}

	for tool, args := range cases {
		t.Run(tool, func(t *testing.T) {
			sa := &stubMusterClient{existing: stdioServer(), filesystem: true}
			adapter := NewAdapterWithClient(sa, "test-ns")

			result, err := adapter.ExecuteTool(context.Background(), tool, args)
			if err != nil {
				t.Fatalf("ExecuteTool: %v", err)
			}
			if result.IsError {
				t.Fatalf("%s rejected stdio in filesystem mode: %s", tool, resultText(t, result))
			}
		})
	}
}

// TestStdioAdmission_RemoteTypesUnaffected guards against the gate widening
// into a general "Kubernetes mode rejects writes" behavior.
func TestStdioAdmission_RemoteTypesUnaffected(t *testing.T) {
	for _, serverType := range []string{"streamable-http", "sse"} {
		t.Run(serverType, func(t *testing.T) {
			sa := &stubMusterClient{}
			adapter := NewAdapterWithClient(sa, "test-ns")

			result, err := adapter.ExecuteTool(context.Background(), "mcpserver_create",
				map[string]interface{}{"name": "remote-server", "type": serverType, "url": "http://example.com/mcp"})
			if err != nil {
				t.Fatalf("ExecuteTool: %v", err)
			}
			if result.IsError {
				t.Fatalf("%s create rejected in Kubernetes mode: %s", serverType, resultText(t, result))
			}
			if len(sa.created) != 1 {
				t.Fatalf("expected the create to reach the client, got %v", sa.created)
			}
		})
	}
}
