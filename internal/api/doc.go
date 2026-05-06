// Package api is the central service-locator boundary for muster.
//
// All inter-package communication goes through here. Service packages
// expose handler interfaces (ServiceRegistryHandler, ServiceManagerHandler,
// AggregatorHandler, ConfigHandler, MCPServerManagerHandler, WorkflowHandler,
// EventManagerHandler, ReconcileManagerHandler, TeleportClientHandler) and
// implement them in adapter types in their own packages. Adapters call the
// matching api.Register* function during initialization; consumers retrieve
// implementations with the matching api.Get* function.
//
// This package depends on no other internal package. Service packages depend
// on api but never on each other directly. That's the rule that keeps the
// dependency graph acyclic.
//
// In addition to handler registration, this package contains:
//   - Shared request/response types
//   - Shared error types (NotFoundError, ValidationError)
//   - The pub/sub plumbing for tool-update events
//   - The ToolCaller / ToolChecker adapters used by callers that need to
//     reach the aggregator without importing it
package api
