package capability

import (
	"context"

	"muster/internal/api"
)

// Provider represents something that can provide capabilities
type Provider interface {
	// GetCapabilityTypes returns the types of capabilities this provider can offer
	GetCapabilityTypes() []string

	// CanProvide checks if this provider can provide a specific capability type with the given features
	CanProvide(capabilityType string, features []string) bool

	// Request requests a capability fulfillment
	Request(ctx context.Context, req api.CapabilityRequest) (*api.CapabilityHandle, error)

	// Release releases a previously requested capability
	Release(ctx context.Context, handle *api.CapabilityHandle) error
}

// Manager defines the interface for managing capability requirements and fulfillment
type Manager interface {
	// AddRequirement adds a capability requirement for a service
	AddRequirement(serviceID string, req api.CapabilityRequirement) error

	// RemoveRequirement removes a capability requirement for a service
	RemoveRequirement(serviceID string, handle *api.CapabilityHandle) error

	// GetRequirements returns all requirements for a service
	GetRequirements(serviceID string) []api.CapabilityRequirement

	// ListProviders returns all registered providers
	ListProviders() []Provider

	// RegisterProvider registers a capability provider
	RegisterProvider(provider Provider)

	// GetCapability returns an active capability by ID
	GetCapability(capabilityID string) (*api.Capability, error)

	// ListCapabilities returns all active capabilities
	ListCapabilities() []*api.Capability
}
