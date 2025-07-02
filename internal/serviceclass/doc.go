// Package serviceclass provides service class definition management for muster.
//
// This package is responsible for loading, parsing, validating, and managing
// ServiceClass definitions that describe how to create and manage dynamic
// service instances. ServiceClass definitions are YAML-based blueprints that
// specify service lifecycle operations, arg mappings, and tool integrations.
//
// # Architecture
//
// The serviceclass package follows the API Service Locator Pattern mandated by
// the muster architecture. It provides:
//
//   - ServiceClassManager: Core manager for loading and validating ServiceClass definitions
//   - ServiceClass Types: Go structs that map to ServiceClass YAML schema
//   - API Integration: Proper adapter for integration with the central API layer
//
// # ServiceClass Concept
//
// A ServiceClass is a template that defines:
//   - Metadata (name, type, version, description)
//   - Service configuration (lifecycle tools, args, health checks)
//   - Arg mappings for service creation
//   - Tool integrations for lifecycle management
//
// ServiceClass definitions are loaded from YAML files in the definitions directory
// and can be used to create dynamic service instances at runtime.
//
// # Separation of Concerns
//
// This package is deliberately separated from the capability system to provide
// a clean architectural boundary between:
//   - Capability operations (what can be done)
//   - Service lifecycle management (how services are created and managed)
//
// The ServiceClassManager handles all aspects of service blueprint management,
// while the capability system focuses on operational workflows and tool orchestration.
//
// # API Integration
//
// Following the project's API Service Locator Pattern, this package exposes its
// functionality through the ServiceClassManagerHandler interface registered with
// the central API layer. Other packages should access serviceclass functionality
// exclusively through api.GetServiceClassManager() rather than direct imports.
//
// # Usage Example
//
//	// Access through API layer (correct approach)
//	handler := api.GetServiceClassManager()
//	if handler != nil {
//		classes := handler.ListServiceClasses()
//		for _, class := range classes {
//			fmt.Printf("ServiceClass: %s (%s)\n", class.Name, class.Type)
//		}
//	}
//
// # YAML Schema
//
// ServiceClass definitions follow this structure:
//   - name: Unique identifier for the service class
//   - type: Service class type
//   - version: Schema version
//   - description: Human-readable description
//   - serviceConfig: Lifecycle and behavior configuration
//   - operations: Available operations (for external API compatibility)
//   - metadata: Additional metadata
package serviceclass
