// Package events provides core infrastructure for generating Kubernetes Events
// for muster CRD lifecycle operations.
//
// This package extends the existing unified MusterClient architecture to support
// Kubernetes Events generation, enabling visibility into CRD operations through
// standard Kubernetes tooling (kubectl get events).
//
// Architecture:
//
// The events package follows the existing service locator pattern and integrates
// with the unified MusterClient interface. It provides:
//
//   - EventGenerator: Core event generation utilities using MusterClient
//   - MessageTemplateEngine: Dynamic message templating system
//   - Event type definitions and constants
//   - API integration following service locator pattern
//
// Backend Support:
//
//   - Kubernetes: Creates actual Kubernetes Events API objects
//   - Filesystem: Logs events to console and events.log file
//
// The package automatically adapts to the current client mode (Kubernetes vs filesystem)
// through the unified MusterClient interface, ensuring consistent behavior across
// different deployment environments.
//
// Usage:
//
// Components should access event generation functionality through the API layer:
//
//	eventManager := api.GetEventManager()
//	if eventManager != nil {
//		err := eventManager.CreateEvent(ctx, objectRef, "Created", "MCPServer successfully created", "Normal")
//	}
//
// Direct usage of EventGenerator is also supported for advanced scenarios:
//
//	generator := events.NewEventGenerator(musterClient)
//	err := generator.MCPServerEvent(server, events.ReasonCreated, events.EventData{})
package events
