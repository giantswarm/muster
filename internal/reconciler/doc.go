// Package reconciler keeps muster resources in sync with their definition
// sources. It detects changes to MCPServer and Workflow definitions (CRDs in
// Kubernetes mode, YAML files in filesystem mode) and dispatches them to a
// type-specific Reconciler with retry and backoff.
//
// Components:
//   - Manager: coordinates reconcilers and the change detector lifecycle
//   - Reconciler: per-resource-type reconciliation logic (one per CRD)
//   - ChangeDetector: emits change events from CRDs (informers) or YAML
//     files (fsnotify)
package reconciler
