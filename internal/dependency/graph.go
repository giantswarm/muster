// internal/dependency/graph.go
package dependency

// NodeState represents the lifecycle state of a node (service). It can be used by
// higher-level orchestration logic or the TUI to colourise status output. At the
// moment it is informational only.
type NodeState int

const (
	StateUnknown NodeState = iota
	StateStopped
	StateStarting
	StateRunning
	StateError
)

// NodeID is the unique identifier for a node inside a dependency graph.
// We purposely keep it as a string alias so that callers can freely choose an
// encoding scheme (e.g. "mcp:prometheus", "pf:Prometheus (MC)").
type NodeID string

// NodeKind categorises nodes.  The current codebase needs just two kinds, but we
// keep it extensible.
type NodeKind int

const (
	KindUnknown NodeKind = iota
	KindMCP
	KindPortForward
	KindK8sConnection // Represents a kubernetes cluster connection
)

// Node represents a runtime unit (MCP proxy, port-forward, etc.) together with
// its dependency list.
//
// A node can depend on zero or more other nodes.  The graph should therefore be
// a Directed Acyclic Graph (DAG).  Cycle detection is not implemented because
// the static graph we build is small and carefully curated.
type Node struct {
	ID           NodeID
	FriendlyName string
	Kind         NodeKind
	DependsOn    []NodeID
	State        NodeState
}

// Graph is a very small helper to answer dependency queries.  It is *not*
// thread-safe by itself; callers must synchronise if they write concurrently.

type Graph struct {
	nodes map[NodeID]*Node
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{nodes: make(map[NodeID]*Node)}
}

// AddNode adds (or replaces) a node in the graph.
func (g *Graph) AddNode(n Node) {
	if g.nodes == nil {
		g.nodes = make(map[NodeID]*Node)
	}
	// Copy to avoid external mutations
	copied := n
	g.nodes[n.ID] = &copied
}

// Get returns a pointer to the stored node or nil if it does not exist.
func (g *Graph) Get(id NodeID) *Node {
	return g.nodes[id]
}

// Dependencies returns a slice of immediate dependency IDs for the given node.
func (g *Graph) Dependencies(id NodeID) []NodeID {
	if n, ok := g.nodes[id]; ok {
		// Return a copy to avoid callers modifying internal slice.
		depsCopy := make([]NodeID, len(n.DependsOn))
		copy(depsCopy, n.DependsOn)
		return depsCopy
	}
	return nil
}

// Dependents returns all node IDs that have a direct dependency on the given
// node.  This is an expensive O(n) walk but the graph is tiny, so fine.
func (g *Graph) Dependents(id NodeID) []NodeID {
	var res []NodeID
	for _, n := range g.nodes {
		for _, dep := range n.DependsOn {
			if dep == id {
				res = append(res, n.ID)
				break
			}
		}
	}
	return res
}
