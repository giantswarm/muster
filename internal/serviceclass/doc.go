// Package serviceclass provides Kubernetes CRD-based ServiceClass management
// with full lifecycle operations and status tracking.
//
// # Overview
//
// This package implements ServiceClass management using Kubernetes Custom Resource Definitions (CRDs).
// ServiceClasses define templates for creating and managing service instances with standardized
// lifecycle operations, health monitoring, and dependency management.
//
// # Architecture
//
// The package follows the muster API Service Locator Pattern:
//   - Adapter: Implements the ServiceClassManager interface from internal/api
//   - Client Integration: Uses the unified MusterClient interface for data access
//   - CRD Management: Handles Kubernetes ServiceClass resources with proper validation
//   - Status Tracking: Dynamically calculates tool availability and service readiness
//
// # Key Features
//
//   - Complete CRUD operations for ServiceClass resources
//   - Dynamic tool availability checking and status population
//   - Kubernetes-native storage with CRD support
//   - Fallback to filesystem mode when CRDs are unavailable
//   - Comprehensive argument validation and templating support
//   - Health check configuration and monitoring
//   - Dependency resolution between ServiceClasses
//
// # Usage Example
//
//	// Create and register the adapter
//	adapter, err := serviceclass.NewAdapter()
//	if err != nil {
//	    log.Fatal("Failed to create ServiceClass adapter:", err)
//	}
//	defer adapter.Close()
//
//	// Register with the API layer (required by Service Locator Pattern)
//	adapter.Register()
//
//	// Access via API layer (recommended)
//	manager := api.GetServiceClassManager()
//	serviceClass, err := manager.GetServiceClass("my-service")
//
// # Type Conversions
//
// The package handles conversion between:
//   - Kubernetes CRD types (pkg/apis/muster/v1alpha1)
//   - Internal API types (internal/api)
//   - Raw YAML/JSON representations
//
// # Error Handling
//
// All operations return structured errors with proper context:
//   - ServiceClassNotFoundError: When a ServiceClass doesn't exist
//   - ValidationError: When ServiceClass definition is invalid
//   - KubernetesError: When underlying Kubernetes operations fail
//
// # Thread Safety
//
// The Adapter is thread-safe and can be used concurrently across multiple goroutines.
// The underlying Kubernetes client handles connection pooling and request management.
//
// # Performance Considerations
//
// - Status population happens on every read operation for accuracy
// - Tool availability is checked dynamically via the aggregator
// - Large ServiceClass lists are processed in batches where possible
// - Client connections are reused and properly managed
package serviceclass
