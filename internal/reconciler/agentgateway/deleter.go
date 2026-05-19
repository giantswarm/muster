package agentgateway

import "context"

// Deleter removes the persisted state for one MCPServer. The yaml Applier
// implements it (per-file cleanup); the K8s Applier does not (cascade via
// OwnerReferences). The reconciler treats Deleter as optional and skips when
// nil.
type Deleter interface {
	Delete(ctx context.Context, name string) error
}
