package aggregator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// ServerRegistry manages the collection of registered MCP servers and their capabilities.
//
// The registry maintains a thread-safe mapping of server names to their information,
// including cached capabilities (tools, resources, prompts) and connection status.
// It provides intelligent name resolution with prefixing to avoid conflicts between
// servers that might have tools with the same names.
//
// Key responsibilities:
//   - Server lifecycle management (registration/deregistration)
//   - Capability caching for performance
//   - Name collision resolution through prefixing
//   - Thread-safe access to server information
//   - Update notifications for capability changes
type ServerRegistry struct {
	servers map[string]*ServerInfo // Map of server name to server information
	mu      sync.RWMutex           // Protects concurrent access to servers map

	// Channel for notifying subscribers about registry changes
	updateChan chan struct{}

	// Name conflict tracking and resolution
	nameTracker *NameTracker
}

// NewServerRegistry creates a new server registry with the specified global prefix.
//
// The registry uses the musterPrefix to ensure all exposed capabilities are
// prefixed appropriately to distinguish them from other MCP tools in the environment.
//
// Args:
//   - musterPrefix: Global prefix applied to all aggregated capabilities (default: "x")
//
// Returns a new, empty server registry ready for use.
func NewServerRegistry(musterPrefix string) *ServerRegistry {
	return &ServerRegistry{
		servers:     make(map[string]*ServerInfo),
		updateChan:  make(chan struct{}, 1),
		nameTracker: NewNameTracker(musterPrefix),
	}
}

// Register adds a new MCP server to the registry and initializes its capabilities.
//
// This method performs the following operations:
//  1. Validates that the server name is not already in use
//  2. Initializes the MCP client if needed
//  3. Queries the server for its initial capabilities
//  4. Stores the server information and updates the name tracker
//  5. Notifies subscribers of the registry update
//
// The method is thread-safe and can be called concurrently.
//
// Args:
//   - ctx: Context for initialization and capability queries
//   - name: Unique identifier for the server
//   - client: MCP client instance for communicating with the server
//   - toolPrefix: Server-specific prefix for tools (uses server name if empty)
//
// Returns an error if the server name is already registered, client initialization
// fails, or the server cannot be reached.
func (r *ServerRegistry) Register(ctx context.Context, name string, client MCPClient, toolPrefix string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.servers[name]; exists {
		return fmt.Errorf("server %s already registered", name)
	}

	// Check if client is already initialized, if not try to initialize
	if initializer, ok := client.(interface{ Initialize(context.Context) error }); ok {
		// Use a short timeout to avoid blocking the registration process
		initCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		if err := initializer.Initialize(initCtx); err != nil {
			return fmt.Errorf("failed to initialize client for %s: %w", name, err)
		}
	}

	// Create server info structure
	info := &ServerInfo{
		Name:       name,
		Client:     client,
		Connected:  true,
		ToolPrefix: toolPrefix,
	}

	// Configure the server prefix in the name tracker
	r.nameTracker.SetServerPrefix(name, toolPrefix)

	// Fetch initial capabilities from the server
	if err := r.refreshServerCapabilities(ctx, info); err != nil {
		logging.Warn("Aggregator", "Failed to get initial capabilities for %s: %v", name, err)
		// Log diagnostic information about partial success
		info.mu.RLock()
		logging.Debug("Aggregator", "Server %s registered with %d tools, %d resources, %d prompts",
			name, len(info.Tools), len(info.Resources), len(info.Prompts))
		info.mu.RUnlock()
	} else {
		info.mu.RLock()
		logging.Info("Aggregator", "Server %s registered successfully with %d tools, %d resources, %d prompts",
			name, len(info.Tools), len(info.Resources), len(info.Prompts))
		info.mu.RUnlock()
	}

	r.servers[name] = info
	r.notifyUpdate()

	logging.Info("Aggregator", "Registered MCP server: %s", name)
	return nil
}

// Deregister removes an MCP server from the registry and cleans up its resources.
//
// This method safely removes a server from the registry, closes its client connection,
// and notifies subscribers of the change. All tools, resources, and prompts provided
// by the server will no longer be available through the aggregator.
//
// The method is thread-safe and can be called concurrently.
//
// Args:
//   - name: Unique identifier of the server to remove
//
// Returns an error if the server is not found in the registry.
func (r *ServerRegistry) Deregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	info, exists := r.servers[name]
	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	// Close the client connection gracefully
	if err := info.Client.Close(); err != nil {
		logging.Warn("Aggregator", "Error closing client for %s: %v", name, err)
	}

	delete(r.servers, name)
	r.notifyUpdate()

	logging.Info("Aggregator", "Deregistered MCP server: %s", name)
	return nil
}

// GetClient returns the MCP client for a specific registered server.
//
// This method provides access to the underlying MCP client for direct communication
// with a specific server. The client can be used to execute tools, read resources,
// or retrieve prompts from the server.
//
// Args:
//   - name: Unique identifier of the server
//
// Returns the MCP client interface and nil error if successful.
// Returns nil client and an error if the server is not found or not connected.
func (r *ServerRegistry) GetClient(name string) (MCPClient, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, exists := r.servers[name]
	if !exists {
		return nil, fmt.Errorf("server %s not found", name)
	}

	if !info.IsConnected() {
		return nil, fmt.Errorf("server %s is not connected", name)
	}

	return info.Client, nil
}

// GetAllTools returns a consolidated list of all tools from all connected servers.
//
// This method aggregates tools from all registered and connected servers, applying
// intelligent prefixing to avoid name conflicts. Only servers that are currently
// connected contribute their tools to the result.
//
// The returned tools have their names modified to include appropriate prefixes
// following the pattern: {muster_prefix}_{server_prefix}_{original_name}
//
// Returns a slice of MCP tools ready for exposure through the aggregator.
func (r *ServerRegistry) GetAllTools() []mcp.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var allTools []mcp.Tool
	connectedCount := 0
	totalServerCount := 0

	for serverName, info := range r.servers {
		totalServerCount++
		if !info.IsConnected() {
			logging.Debug("Aggregator", "Server %s is not connected, skipping tools", serverName)
			continue
		}
		connectedCount++

		info.mu.RLock()
		serverToolCount := len(info.Tools)
		for _, tool := range info.Tools {
			// Apply smart prefixing to avoid name conflicts
			exposedTool := tool
			exposedTool.Name = r.nameTracker.GetExposedToolName(serverName, tool.Name)
			allTools = append(allTools, exposedTool)
		}
		info.mu.RUnlock()

		logging.Debug("Aggregator", "Server %s has %d tools", serverName, serverToolCount)
	}

	logging.Debug("Aggregator", "GetAllTools: returning %d tools from %d connected servers (out of %d total servers)",
		len(allTools), connectedCount, totalServerCount)

	return allTools
}

// GetAllResources returns a consolidated list of all resources from all connected servers.
//
// This method aggregates resources from all registered and connected servers, applying
// intelligent prefixing to resource URIs to avoid conflicts. Only servers that are
// currently connected contribute their resources to the result.
//
// Returns a slice of MCP resources ready for exposure through the aggregator.
func (r *ServerRegistry) GetAllResources() []mcp.Resource {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var allResources []mcp.Resource

	for serverName, info := range r.servers {
		if !info.IsConnected() {
			continue
		}

		info.mu.RLock()
		for _, resource := range info.Resources {
			// Apply smart prefixing to resource URIs
			exposedResource := resource
			exposedResource.URI = r.nameTracker.GetExposedResourceURI(serverName, resource.URI)
			allResources = append(allResources, exposedResource)
		}
		info.mu.RUnlock()
	}

	return allResources
}

// GetAllPrompts returns a consolidated list of all prompts from all connected servers.
//
// This method aggregates prompts from all registered and connected servers, applying
// intelligent prefixing to avoid name conflicts. Only servers that are currently
// connected contribute their prompts to the result.
//
// Returns a slice of MCP prompts ready for exposure through the aggregator.
func (r *ServerRegistry) GetAllPrompts() []mcp.Prompt {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var allPrompts []mcp.Prompt

	for serverName, info := range r.servers {
		if !info.IsConnected() {
			continue
		}

		info.mu.RLock()
		for _, prompt := range info.Prompts {
			// Apply smart prefixing to prompt names
			exposedPrompt := prompt
			exposedPrompt.Name = r.nameTracker.GetExposedPromptName(serverName, prompt.Name)
			allPrompts = append(allPrompts, exposedPrompt)
		}
		info.mu.RUnlock()
	}

	return allPrompts
}

// ResolveToolName resolves an exposed (prefixed) tool name back to its source server and original name.
//
// This method is used when a tool call is received to determine which server should
// handle the request and what the original tool name was before prefixing.
//
// Args:
//   - exposedName: The prefixed tool name as seen by clients
//
// Returns the server name, original tool name, and nil error if resolution succeeds.
// Returns empty strings and an error if the name cannot be resolved or refers to a different item type.
func (r *ServerRegistry) ResolveToolName(exposedName string) (serverName, originalName string, err error) {
	serverName, originalName, itemType, err := r.nameTracker.ResolveName(exposedName)
	if err != nil {
		return "", "", err
	}
	if itemType != "tool" {
		return "", "", fmt.Errorf("name %s is a %s, not a tool", exposedName, itemType)
	}
	return serverName, originalName, nil
}

// ResolvePromptName resolves an exposed (prefixed) prompt name back to its source server and original name.
//
// This method is used when a prompt request is received to determine which server should
// handle the request and what the original prompt name was before prefixing.
//
// Args:
//   - exposedName: The prefixed prompt name as seen by clients
//
// Returns the server name, original prompt name, and nil error if resolution succeeds.
// Returns empty strings and an error if the name cannot be resolved or refers to a different item type.
func (r *ServerRegistry) ResolvePromptName(exposedName string) (serverName, originalName string, err error) {
	serverName, originalName, itemType, err := r.nameTracker.ResolveName(exposedName)
	if err != nil {
		return "", "", err
	}
	if itemType != "prompt" {
		return "", "", fmt.Errorf("name %s is a %s, not a prompt", exposedName, itemType)
	}
	return serverName, originalName, nil
}

// ResolveResourceName resolves an exposed (prefixed) resource URI back to its source server and original URI.
//
// This method is used when a resource read request is received to determine which server
// should handle the request and what the original resource URI was before prefixing.
//
// Args:
//   - exposedURI: The prefixed resource URI as seen by clients
//
// Returns the server name, original resource URI, and nil error if resolution succeeds.
// Returns empty strings and an error if the URI cannot be resolved or refers to a different item type.
func (r *ServerRegistry) ResolveResourceName(exposedURI string) (serverName, originalURI string, err error) {
	serverName, originalURI, itemType, err := r.nameTracker.ResolveName(exposedURI)
	if err != nil {
		return "", "", err
	}
	if itemType != "resource" {
		return "", "", fmt.Errorf("URI %s is a %s, not a resource", exposedURI, itemType)
	}
	return serverName, originalURI, nil
}

// notifyUpdate sends a notification through the update channel to inform subscribers
// that the registry has been modified.
//
// This method is non-blocking - if the channel already has a pending notification,
// no additional notification is queued.
func (r *ServerRegistry) notifyUpdate() {
	select {
	case r.updateChan <- struct{}{}:
	default:
		// Channel already has a notification pending
	}
}

// GetUpdateChannel returns a read-only channel that receives notifications when
// the registry is updated.
//
// Subscribers can use this channel to react to server registrations, deregistrations,
// or capability changes. The channel is buffered with a capacity of 1 to prevent
// blocking the registry operations.
//
// Returns a receive-only channel for registry update notifications.
func (r *ServerRegistry) GetUpdateChannel() <-chan struct{} {
	return r.updateChan
}

// GetServerInfo returns detailed information about a specific registered server.
//
// This method provides access to the complete ServerInfo structure for a given
// server, including its client, cached capabilities, and connection status.
//
// Args:
//   - name: Unique identifier of the server
//
// Returns the ServerInfo pointer and true if the server exists.
// Returns nil and false if the server is not found.
func (r *ServerRegistry) GetServerInfo(name string) (*ServerInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, exists := r.servers[name]
	return info, exists
}

// GetAllServers returns a copy of all registered server information.
//
// This method provides a snapshot of all servers currently registered with
// the registry, including both connected and disconnected servers. The returned
// map is a copy to prevent external modifications to the internal state.
//
// Returns a map of server names to their corresponding ServerInfo structures.
func (r *ServerRegistry) GetAllServers() map[string]*ServerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Create a copy to prevent external modifications
	result := make(map[string]*ServerInfo, len(r.servers))
	for k, v := range r.servers {
		result[k] = v
	}
	return result
}

// refreshServerCapabilities queries a server for its current capabilities and updates the cache.
//
// This method fetches tools, resources, and prompts from the specified server and updates
// the cached information. It handles partial failures gracefully - if one type of capability
// cannot be retrieved, the others are still updated.
//
// Args:
//   - ctx: Context for the capability queries
//   - info: ServerInfo structure to update with fresh capabilities
//
// Returns an error only if the tool query fails (tools are considered mandatory).
// Resource and prompt query failures are logged but not treated as errors.
func (r *ServerRegistry) refreshServerCapabilities(ctx context.Context, info *ServerInfo) error {
	// Get tools (considered mandatory)
	tools, err := info.Client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}
	info.UpdateTools(tools)

	// Get resources (optional - some servers may not support resources)
	resources, err := info.Client.ListResources(ctx)
	if err != nil {
		// Resources might not be supported by this server
		logging.Debug("Aggregator", "Failed to list resources for %s: %v", info.Name, err)
		info.UpdateResources([]mcp.Resource{})
	} else {
		info.UpdateResources(resources)
	}

	// Get prompts (optional - some servers may not support prompts)
	prompts, err := info.Client.ListPrompts(ctx)
	if err != nil {
		// Prompts might not be supported by this server
		logging.Debug("Aggregator", "Failed to list prompts for %s: %v", info.Name, err)
		info.UpdatePrompts([]mcp.Prompt{})
	} else {
		info.UpdatePrompts(prompts)
	}

	return nil
}
