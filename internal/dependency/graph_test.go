package dependency

import (
	"testing"
)

func TestNew(t *testing.T) {
	g := New()
	if g == nil {
		t.Fatal("New() returned nil")
	}
	if g.nodes == nil {
		t.Fatal("nodes map not initialized")
	}
	if len(g.nodes) != 0 {
		t.Fatalf("expected empty nodes map, got %d nodes", len(g.nodes))
	}
}

func TestAddNode(t *testing.T) {
	tests := []struct {
		name     string
		nodes    []Node
		expected int
	}{
		{
			name: "add single node",
			nodes: []Node{
				{
					ID:           "node1",
					FriendlyName: "Node 1",
					Kind:         KindPortForward,
					DependsOn:    nil,
				},
			},
			expected: 1,
		},
		{
			name: "add multiple nodes",
			nodes: []Node{
				{
					ID:           "k8s1",
					FriendlyName: "K8s Connection",
					Kind:         KindK8sConnection,
					DependsOn:    nil,
				},
				{
					ID:           "pf1",
					FriendlyName: "Port Forward 1",
					Kind:         KindPortForward,
					DependsOn:    []NodeID{"k8s1"},
				},
				{
					ID:           "mcp1",
					FriendlyName: "MCP Server 1",
					Kind:         KindMCP,
					DependsOn:    []NodeID{"pf1"},
				},
			},
			expected: 3,
		},
		{
			name: "replace existing node",
			nodes: []Node{
				{
					ID:           "node1",
					FriendlyName: "Node 1",
					Kind:         KindPortForward,
					DependsOn:    nil,
				},
				{
					ID:           "node1",
					FriendlyName: "Node 1 Updated",
					Kind:         KindPortForward,
					DependsOn:    []NodeID{"k8s1"},
				},
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := New()
			for _, node := range tt.nodes {
				g.AddNode(node)
			}
			if len(g.nodes) != tt.expected {
				t.Errorf("expected %d nodes, got %d", tt.expected, len(g.nodes))
			}
			// Verify the last node was properly added/updated
			if tt.expected > 0 {
				lastNode := tt.nodes[len(tt.nodes)-1]
				if node := g.Get(lastNode.ID); node == nil {
					t.Errorf("node %s not found", lastNode.ID)
				} else if node.FriendlyName != lastNode.FriendlyName {
					t.Errorf("node friendly name mismatch: expected %s, got %s",
						lastNode.FriendlyName, node.FriendlyName)
				}
			}
		})
	}
}

func TestGet(t *testing.T) {
	g := New()

	// Test getting non-existent node
	if node := g.Get("nonexistent"); node != nil {
		t.Error("expected nil for non-existent node")
	}

	// Add a node and test getting it
	testNode := Node{
		ID:           "test1",
		FriendlyName: "Test Node",
		Kind:         KindPortForward,
		DependsOn:    []NodeID{"dep1", "dep2"},
		State:        StateRunning,
	}
	g.AddNode(testNode)

	retrieved := g.Get("test1")
	if retrieved == nil {
		t.Fatal("failed to retrieve added node")
	}
	if retrieved.ID != testNode.ID {
		t.Errorf("ID mismatch: expected %s, got %s", testNode.ID, retrieved.ID)
	}
	if retrieved.FriendlyName != testNode.FriendlyName {
		t.Errorf("FriendlyName mismatch: expected %s, got %s", testNode.FriendlyName, retrieved.FriendlyName)
	}
	if retrieved.Kind != testNode.Kind {
		t.Errorf("Kind mismatch: expected %v, got %v", testNode.Kind, retrieved.Kind)
	}
	if retrieved.State != testNode.State {
		t.Errorf("State mismatch: expected %v, got %v", testNode.State, retrieved.State)
	}
	if len(retrieved.DependsOn) != len(testNode.DependsOn) {
		t.Errorf("DependsOn length mismatch: expected %d, got %d",
			len(testNode.DependsOn), len(retrieved.DependsOn))
	}
}

func TestDependencies(t *testing.T) {
	g := New()

	// Test dependencies of non-existent node
	deps := g.Dependencies("nonexistent")
	if len(deps) != 0 {
		t.Errorf("expected empty dependencies for non-existent node, got %v", deps)
	}

	// Build a dependency chain
	g.AddNode(Node{
		ID:           "k8s1",
		FriendlyName: "K8s Connection",
		Kind:         KindK8sConnection,
		DependsOn:    nil,
	})
	g.AddNode(Node{
		ID:           "pf1",
		FriendlyName: "Port Forward 1",
		Kind:         KindPortForward,
		DependsOn:    []NodeID{"k8s1"},
	})
	g.AddNode(Node{
		ID:           "mcp1",
		FriendlyName: "MCP Server 1",
		Kind:         KindMCP,
		DependsOn:    []NodeID{"pf1"},
	})
	g.AddNode(Node{
		ID:           "mcp2",
		FriendlyName: "MCP Server 2",
		Kind:         KindMCP,
		DependsOn:    []NodeID{"pf1", "k8s1"}, // Depends on both
	})

	// Test various dependencies
	tests := []struct {
		nodeID   NodeID
		expected []NodeID
	}{
		{"k8s1", []NodeID{}},
		{"pf1", []NodeID{"k8s1"}},
		{"mcp1", []NodeID{"pf1"}},
		{"mcp2", []NodeID{"pf1", "k8s1"}},
	}

	for _, tt := range tests {
		t.Run(string(tt.nodeID), func(t *testing.T) {
			deps := g.Dependencies(tt.nodeID)
			if len(deps) != len(tt.expected) {
				t.Errorf("expected %d dependencies, got %d", len(tt.expected), len(deps))
			}
			// Check each expected dependency
			for _, exp := range tt.expected {
				found := false
				for _, dep := range deps {
					if dep == exp {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected dependency %s not found", exp)
				}
			}
		})
	}
}

func TestDependents(t *testing.T) {
	g := New()

	// Test dependents of non-existent node
	deps := g.Dependents("nonexistent")
	if len(deps) != 0 {
		t.Errorf("expected empty dependents for non-existent node, got %v", deps)
	}

	// Build a dependency chain
	g.AddNode(Node{
		ID:           "k8s1",
		FriendlyName: "K8s Connection",
		Kind:         KindK8sConnection,
		DependsOn:    nil,
	})
	g.AddNode(Node{
		ID:           "pf1",
		FriendlyName: "Port Forward 1",
		Kind:         KindPortForward,
		DependsOn:    []NodeID{"k8s1"},
	})
	g.AddNode(Node{
		ID:           "pf2",
		FriendlyName: "Port Forward 2",
		Kind:         KindPortForward,
		DependsOn:    []NodeID{"k8s1"},
	})
	g.AddNode(Node{
		ID:           "mcp1",
		FriendlyName: "MCP Server 1",
		Kind:         KindMCP,
		DependsOn:    []NodeID{"pf1"},
	})
	g.AddNode(Node{
		ID:           "mcp2",
		FriendlyName: "MCP Server 2",
		Kind:         KindMCP,
		DependsOn:    []NodeID{"pf1", "k8s1"}, // Depends on both
	})

	// Test various dependents
	tests := []struct {
		nodeID   NodeID
		expected []NodeID
	}{
		{"k8s1", []NodeID{"pf1", "pf2", "mcp2"}}, // All that depend on k8s1
		{"pf1", []NodeID{"mcp1", "mcp2"}},        // MCPs that depend on pf1
		{"pf2", []NodeID{}},                      // Nothing depends on pf2
		{"mcp1", []NodeID{}},                     // Nothing depends on mcp1
	}

	for _, tt := range tests {
		t.Run(string(tt.nodeID), func(t *testing.T) {
			deps := g.Dependents(tt.nodeID)
			if len(deps) != len(tt.expected) {
				t.Errorf("expected %d dependents, got %d: %v", len(tt.expected), len(deps), deps)
			}
			// Check each expected dependent
			for _, exp := range tt.expected {
				found := false
				for _, dep := range deps {
					if dep == exp {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected dependent %s not found in %v", exp, deps)
				}
			}
		})
	}
}

func TestComplexDependencyGraph(t *testing.T) {
	// Test a more complex real-world scenario
	g := New()

	// K8s connections
	g.AddNode(Node{ID: "k8s-mc", Kind: KindK8sConnection})
	g.AddNode(Node{ID: "k8s-wc", Kind: KindK8sConnection})

	// Port forwards
	g.AddNode(Node{ID: "pf-prometheus", Kind: KindPortForward, DependsOn: []NodeID{"k8s-mc"}})
	g.AddNode(Node{ID: "pf-grafana", Kind: KindPortForward, DependsOn: []NodeID{"k8s-mc"}})
	g.AddNode(Node{ID: "pf-alloy", Kind: KindPortForward, DependsOn: []NodeID{"k8s-wc"}})

	// MCP servers
	g.AddNode(Node{ID: "mcp-kubernetes", Kind: KindMCP, DependsOn: []NodeID{"k8s-mc"}})
	g.AddNode(Node{ID: "mcp-prometheus", Kind: KindMCP, DependsOn: []NodeID{"pf-prometheus"}})
	g.AddNode(Node{ID: "mcp-grafana", Kind: KindMCP, DependsOn: []NodeID{"pf-grafana"}})

	// Test transitive dependents
	mcDependents := g.Dependents("k8s-mc")
	expectedMCDependents := map[NodeID]bool{
		"pf-prometheus":  true,
		"pf-grafana":     true,
		"mcp-kubernetes": true,
	}

	for _, dep := range mcDependents {
		if !expectedMCDependents[dep] {
			t.Errorf("unexpected dependent of k8s-mc: %s", dep)
		}
		delete(expectedMCDependents, dep)
	}

	if len(expectedMCDependents) > 0 {
		t.Errorf("missing dependents of k8s-mc: %v", expectedMCDependents)
	}

	// Test that stopping pf-prometheus should affect mcp-prometheus
	promDependents := g.Dependents("pf-prometheus")
	if len(promDependents) != 1 || promDependents[0] != "mcp-prometheus" {
		t.Errorf("expected mcp-prometheus as dependent of pf-prometheus, got %v", promDependents)
	}
}

// Note: The Graph struct is documented as not thread-safe by design.
// Callers (like the orchestrator) must handle synchronization.
