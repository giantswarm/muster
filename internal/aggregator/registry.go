package aggregator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// ServerRegistry manages registered MCP servers
type ServerRegistry struct {
	servers map[string]*ServerInfo
	mu      sync.RWMutex

	// Channel for notifying about changes
	updateChan chan struct{}

	// Name conflict tracking
	nameTracker *NameTracker
}

// NewServerRegistry creates a new server registry
func NewServerRegistry(musterPrefix string) *ServerRegistry {
	return &ServerRegistry{
		servers:     make(map[string]*ServerInfo),
		updateChan:  make(chan struct{}, 1),
		nameTracker: NewNameTracker(musterPrefix),
	}
}

// Register adds a new MCP server to the registry
func (r *ServerRegistry) Register(ctx context.Context, name string, client MCPClient, toolPrefix string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.servers[name]; exists {
		return fmt.Errorf("server %s already registered", name)
	}

	// Check if client is already initialized, if not try to initialize
	if initializer, ok := client.(interface{ Initialize(context.Context) error }); ok {
		// Use a short timeout to avoid blocking
		initCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		if err := initializer.Initialize(initCtx); err != nil {
			return fmt.Errorf("failed to initialize client for %s: %w", name, err)
		}
	}

	// Create server info
	info := &ServerInfo{
		Name:       name,
		Client:     client,
		Connected:  true,
		ToolPrefix: toolPrefix,
	}

	// Set the server prefix in the name tracker
	r.nameTracker.SetServerPrefix(name, toolPrefix)

	// Get initial capabilities
	if err := r.refreshServerCapabilities(ctx, info); err != nil {
		logging.Warn("Aggregator", "Failed to get initial capabilities for %s: %v", name, err)
		// Log more details about what we did get
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

	// No longer needed - we don't track collisions anymore
	// r.nameTracker.RebuildMappings(r.servers)

	logging.Info("Aggregator", "Registered MCP server: %s", name)
	return nil
}

// Deregister removes an MCP server from the registry
func (r *ServerRegistry) Deregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	info, exists := r.servers[name]
	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	// Close the client connection
	if err := info.Client.Close(); err != nil {
		logging.Warn("Aggregator", "Error closing client for %s: %v", name, err)
	}

	delete(r.servers, name)
	r.notifyUpdate()

	// No longer needed - we don't track collisions anymore
	// r.nameTracker.RebuildMappings(r.servers)

	logging.Info("Aggregator", "Deregistered MCP server: %s", name)
	return nil
}

// GetClient returns the client for a specific server
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

// GetAllTools returns all tools from all registered servers with smart prefixing
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
			// Use smart prefixing - only prefix if there are conflicts
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

// GetAllResources returns all resources from all registered servers with smart prefixing
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
			// Use smart prefixing - only prefix if there are conflicts
			exposedResource := resource
			exposedResource.URI = r.nameTracker.GetExposedResourceURI(serverName, resource.URI)
			allResources = append(allResources, exposedResource)
		}
		info.mu.RUnlock()
	}

	return allResources
}

// GetAllPrompts returns all prompts from all registered servers with smart prefixing
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
			// Use smart prefixing - only prefix if there are conflicts
			exposedPrompt := prompt
			exposedPrompt.Name = r.nameTracker.GetExposedPromptName(serverName, prompt.Name)
			allPrompts = append(allPrompts, exposedPrompt)
		}
		info.mu.RUnlock()
	}

	return allPrompts
}

// ResolveToolName resolves an exposed tool name to server and original name
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

// ResolvePromptName resolves an exposed prompt name to server and original name
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

// ResolveResourceName resolves an exposed resource URI to server and original URI
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

// notifyUpdate signals that the registry has been updated
func (r *ServerRegistry) notifyUpdate() {
	select {
	case r.updateChan <- struct{}{}:
	default:
		// Channel already has a notification
	}
}

// GetUpdateChannel returns a channel that receives notifications on registry updates
func (r *ServerRegistry) GetUpdateChannel() <-chan struct{} {
	return r.updateChan
}

// GetServerInfo returns information about a specific server
func (r *ServerRegistry) GetServerInfo(name string) (*ServerInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, exists := r.servers[name]
	return info, exists
}

// GetAllServers returns information about all registered servers
func (r *ServerRegistry) GetAllServers() map[string]*ServerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Create a copy to avoid external modifications
	result := make(map[string]*ServerInfo, len(r.servers))
	for k, v := range r.servers {
		result[k] = v
	}
	return result
}

// refreshServerCapabilities updates the tools, resources, and prompts for a server
func (r *ServerRegistry) refreshServerCapabilities(ctx context.Context, info *ServerInfo) error {
	// Get tools
	tools, err := info.Client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}
	info.UpdateTools(tools)

	// Get resources
	resources, err := info.Client.ListResources(ctx)
	if err != nil {
		// Resources might not be supported
		logging.Debug("Aggregator", "Failed to list resources for %s: %v", info.Name, err)
		info.UpdateResources([]mcp.Resource{})
	} else {
		info.UpdateResources(resources)
	}

	// Get prompts
	prompts, err := info.Client.ListPrompts(ctx)
	if err != nil {
		// Prompts might not be supported
		logging.Debug("Aggregator", "Failed to list prompts for %s: %v", info.Name, err)
		info.UpdatePrompts([]mcp.Prompt{})
	} else {
		info.UpdatePrompts(prompts)
	}

	return nil
}
