package aggregator

import (
	"context"

	"github.com/giantswarm/muster/internal/api"
)

// EntityProvider streams declarative entity state into the aggregator.
//
// In cluster mode the implementation watches Kubernetes CRDs; in filesystem
// mode it watches YAML files on disk. Both shapes are mapped onto the same
// port so the aggregator domain does not branch on deployment mode.
type EntityProvider interface {
	WatchMCPServers(ctx context.Context) <-chan EntityChange[MCPServer]
	WatchWorkflows(ctx context.Context) <-chan EntityChange[Workflow]
	UpdateStatus(ctx context.Context, kind, name string, status any) error
}

// EntityChangeType discriminates one transition in an entity's lifecycle.
type EntityChangeType int

const (
	EntityUnknown EntityChangeType = iota
	EntityAdded
	EntityUpdated
	EntityDeleted
)

// EntityChange describes one transition. For [EntityDeleted] the Entity
// field carries the last-known state.
type EntityChange[T any] struct {
	Type   EntityChangeType
	Entity T
}

// MCPServer is the aggregator's port-side view of an MCPServer entity.
// Aliased to the api package's struct so that introducing the port does
// not duplicate the ~390-LOC entity shape; consumers reference the port
// type, which is what matters when the underlying definition later moves
// into this package.
type MCPServer = api.MCPServer

// Workflow is the aggregator's port-side view of a Workflow entity.
// See [MCPServer] for the aliasing rationale.
type Workflow = api.Workflow
