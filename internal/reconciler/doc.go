// Package reconciler provides a unified reconciliation system for muster resources.
//
// # Overview
//
// The reconciler package implements automatic change detection and reconciliation
// for both Kubernetes CRDs and filesystem-based YAML configurations. It ensures
// that the actual state of muster resources matches the desired state defined
// in configuration files or Kubernetes custom resources.
//
// # Architecture
//
// The reconciliation system consists of several key components:
//
//   - ReconcileManager: Central coordinator that manages all reconcilers
//   - Reconciler: Interface for resource-specific reconciliation logic
//   - ChangeDetector: Interface for detecting changes in resource sources
//   - ReconcileLoop: Generic reconciliation loop with retry and backoff
//
// The system supports two modes of operation:
//
//   - Kubernetes Mode: Uses informers and controllers for CRD changes
//   - Filesystem Mode: Uses fsnotify for watching YAML file changes
//
// # Usage
//
// The reconciliation system is automatically integrated with the muster
// application bootstrap process. It starts watching for changes when
// the application starts and stops when the application shuts down.
//
// Example usage:
//
//	manager := reconciler.NewManager(config)
//	if err := manager.Start(ctx); err != nil {
//	    return fmt.Errorf("failed to start reconciliation: %w", err)
//	}
//	defer manager.Stop()
//
// # Resource Types
//
// The following resource types are supported for reconciliation:
//
//   - MCPServer: MCP server definitions and lifecycle management
//   - ServiceClass: Service class templates for dynamic services
//   - Workflow: Workflow definitions for multi-step operations
//
// # Event Integration
//
// The reconciliation system integrates with muster's existing event system
// to provide notifications about reconciliation activities. Events are
// generated for successful reconciliations, failures, and retries.
//
// # Performance Considerations
//
// The system implements several optimizations:
//
//   - Debouncing: Multiple rapid changes are batched together
//   - Efficient watching: Uses informers for Kubernetes, fsnotify for files
//   - Backoff: Failed reconciliations use exponential backoff
//   - Rate limiting: Prevents overwhelming the system with rapid changes
package reconciler
