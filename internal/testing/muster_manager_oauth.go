package testing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"muster/internal/testing/mock"

	"gopkg.in/yaml.v3"
)

// startMockOAuthServers starts mock OAuth servers for a test instance
func (m *musterInstanceManager) startMockOAuthServers(
	ctx context.Context,
	instanceID string,
	config *MusterPreConfiguration,
) (map[string]*MockOAuthServerInfo, error) {
	result := make(map[string]*MockOAuthServerInfo)

	if config == nil || len(config.MockOAuthServers) == 0 {
		return result, nil
	}

	// Initialize the map for this instance
	m.mu.Lock()
	if m.mockOAuthServers[instanceID] == nil {
		m.mockOAuthServers[instanceID] = make(map[string]*mock.OAuthServer)
	}
	m.mu.Unlock()

	for _, oauthCfg := range config.MockOAuthServers {
		tokenLifetime := 1 * time.Hour
		if oauthCfg.TokenLifetime != "" {
			if d, err := time.ParseDuration(oauthCfg.TokenLifetime); err == nil {
				tokenLifetime = d
			}
		}

		serverConfig := mock.OAuthServerConfig{
			Issuer:         oauthCfg.Issuer,
			AcceptedScopes: oauthCfg.Scopes,
			TokenLifetime:  tokenLifetime,
			PKCERequired:   oauthCfg.PKCERequired,
			AutoApprove:    oauthCfg.AutoApprove,
			ClientID:       oauthCfg.ClientID,
			ClientSecret:   oauthCfg.ClientSecret,
			Debug:          m.debug,
		}

		// Use mock clock if configured (enables test_advance_oauth_clock tool)
		if oauthCfg.UseMockClock {
			serverConfig.Clock = mock.NewMockClock(time.Time{})
			if m.debug {
				m.logger.Debug("🕐 Using mock clock for OAuth server %s\n", oauthCfg.Name)
			}
		}

		// Handle simulated errors
		if oauthCfg.SimulateError != "" {
			serverConfig.SimulateErrors = &mock.OAuthErrorSimulation{
				TokenEndpointError: oauthCfg.SimulateError,
			}
		}

		oauthServer := mock.NewOAuthServer(serverConfig)
		port, err := oauthServer.Start(ctx)
		if err != nil {
			// Clean up already started servers
			m.stopMockOAuthServers(ctx, instanceID)
			return nil, fmt.Errorf("failed to start mock OAuth server %s: %w", oauthCfg.Name, err)
		}

		// Wait for server to be ready
		readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := oauthServer.WaitForReady(readyCtx); err != nil {
			cancel()
			oauthServer.Stop(ctx)
			m.stopMockOAuthServers(ctx, instanceID)
			return nil, fmt.Errorf("mock OAuth server %s not ready: %w", oauthCfg.Name, err)
		}
		cancel()

		m.mu.Lock()
		m.mockOAuthServers[instanceID][oauthCfg.Name] = oauthServer
		m.mu.Unlock()

		result[oauthCfg.Name] = &MockOAuthServerInfo{
			Name:      oauthCfg.Name,
			Port:      port,
			IssuerURL: oauthServer.GetIssuerURL(),
		}

		if m.debug {
			m.logger.Debug("🔐 Started mock OAuth server %s on port %d (issuer: %s)\n",
				oauthCfg.Name, port, oauthServer.GetIssuerURL())
		}
	}

	return result, nil
}

// stopMockOAuthServers stops all mock OAuth servers for a given instance
func (m *musterInstanceManager) stopMockOAuthServers(ctx context.Context, instanceID string) {
	m.mu.Lock()
	servers, exists := m.mockOAuthServers[instanceID]
	if exists {
		delete(m.mockOAuthServers, instanceID)
	}
	m.mu.Unlock()

	if !exists || len(servers) == 0 {
		return
	}

	for name, server := range servers {
		if m.debug {
			m.logger.Debug("🔐 Stopping mock OAuth server %s\n", name)
		}

		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := server.Stop(stopCtx); err != nil {
			if m.debug {
				m.logger.Debug("⚠️  Failed to stop mock OAuth server %s: %v\n", name, err)
			}
		}
		cancel()
	}
}

// startMockHTTPServersWithOAuth starts mock HTTP servers, including OAuth-protected ones
func (m *musterInstanceManager) startMockHTTPServersWithOAuth(
	ctx context.Context,
	instanceID, configPath string,
	config *MusterPreConfiguration,
	oauthServers map[string]*MockOAuthServerInfo,
) (map[string]*MockHTTPServerInfo, error) {
	result := make(map[string]*MockHTTPServerInfo)

	if config == nil || len(config.MCPServers) == 0 {
		return result, nil
	}

	// Create mocks directory for mock configurations
	mocksDir := filepath.Join(configPath, "mocks")
	if err := os.MkdirAll(mocksDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create mocks directory: %w", err)
	}

	// Initialize the maps for this instance
	m.mu.Lock()
	if m.mockHTTPServers[instanceID] == nil {
		m.mockHTTPServers[instanceID] = make(map[string]*mock.HTTPServer)
	}
	if m.protectedMCPServers[instanceID] == nil {
		m.protectedMCPServers[instanceID] = make(map[string]*mock.ProtectedMCPServer)
	}
	m.mu.Unlock()

	for _, mcpServer := range config.MCPServers {
		// Check if this is a mock server that needs HTTP transport
		serverType, _ := mcpServer.Config["type"].(string)
		_, hasMockTools := mcpServer.Config["tools"]

		// Only start HTTP mock servers for URL-based types with mock tools
		if !hasMockTools {
			continue
		}

		// Determine if this should be an HTTP-based mock server
		var transportType mock.HTTPTransportType
		switch serverType {
		case "sse":
			transportType = mock.HTTPTransportSSE
		case "streamable-http":
			transportType = mock.HTTPTransportStreamableHTTP
		default:
			// Default to stdio handling (existing behavior), skip HTTP server startup
			continue
		}

		// Check if this server requires OAuth
		oauthConfig := m.extractOAuthConfig(mcpServer.Config)

		if oauthConfig != nil && oauthConfig.Required {
			// Start as a protected MCP server
			info, err := m.startProtectedMCPServer(ctx, instanceID, mcpServer, transportType, oauthConfig, oauthServers)
			if err != nil {
				return nil, fmt.Errorf("failed to start protected MCP server %s: %w", mcpServer.Name, err)
			}
			result[mcpServer.Name] = info
		} else {
			// Start as a regular mock HTTP server
			info, err := m.startRegularMockHTTPServer(ctx, instanceID, configPath, mcpServer, transportType)
			if err != nil {
				return nil, fmt.Errorf("failed to start mock HTTP server %s: %w", mcpServer.Name, err)
			}
			result[mcpServer.Name] = info
		}
	}

	return result, nil
}

// extractOAuthConfig extracts OAuth configuration from MCP server config
func (m *musterInstanceManager) extractOAuthConfig(config map[string]interface{}) *MCPServerOAuthConfig {
	oauth, exists := config["oauth"]
	if !exists {
		return nil
	}

	oauthMap, ok := toStringMap(oauth)
	if !ok {
		return nil
	}

	result := &MCPServerOAuthConfig{}

	if required, ok := oauthMap["required"].(bool); ok {
		result.Required = required
	}
	if ref, ok := oauthMap["mock_oauth_server_ref"].(string); ok {
		result.MockOAuthServerRef = ref
	}
	if scope, ok := oauthMap["scope"].(string); ok {
		result.Scope = scope
	}

	return result
}

// startProtectedMCPServer starts an OAuth-protected mock MCP server
func (m *musterInstanceManager) startProtectedMCPServer(
	ctx context.Context,
	instanceID string,
	mcpServer MCPServerConfig,
	transportType mock.HTTPTransportType,
	oauthConfig *MCPServerOAuthConfig,
	oauthServers map[string]*MockOAuthServerInfo,
) (*MockHTTPServerInfo, error) {
	// Find the referenced OAuth server
	var oauthServer *mock.OAuthServer
	var issuer string

	if oauthConfig.MockOAuthServerRef != "" {
		oauthInfo, exists := oauthServers[oauthConfig.MockOAuthServerRef]
		if !exists {
			return nil, fmt.Errorf("referenced OAuth server %s not found", oauthConfig.MockOAuthServerRef)
		}
		issuer = oauthInfo.IssuerURL

		// Get the actual OAuth server instance
		m.mu.RLock()
		if servers, ok := m.mockOAuthServers[instanceID]; ok {
			oauthServer = servers[oauthConfig.MockOAuthServerRef]
		}
		m.mu.RUnlock()
	}

	// Extract tools from config
	tools := m.extractToolConfigs(mcpServer.Config)

	config := mock.ProtectedMCPServerConfig{
		Name:          mcpServer.Name,
		OAuthServer:   oauthServer,
		Issuer:        issuer,
		RequiredScope: oauthConfig.Scope,
		Tools:         tools,
		Transport:     transportType,
		Debug:         m.debug,
	}

	protectedServer, err := mock.NewProtectedMCPServer(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create protected MCP server: %w", err)
	}

	port, err := protectedServer.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start protected MCP server: %w", err)
	}

	// Wait for server to be ready
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := protectedServer.WaitForReady(readyCtx); err != nil {
		cancel()
		protectedServer.Stop(ctx)
		return nil, fmt.Errorf("protected MCP server not ready: %w", err)
	}
	cancel()

	// Track the server
	m.mu.Lock()
	m.protectedMCPServers[instanceID][mcpServer.Name] = protectedServer
	m.mu.Unlock()

	if m.debug {
		m.logger.Debug("🔒 Started protected MCP server %s on port %d (issuer: %s)\n",
			mcpServer.Name, port, issuer)
	}

	return &MockHTTPServerInfo{
		Name:      mcpServer.Name,
		Port:      port,
		Transport: string(transportType),
		Endpoint:  protectedServer.Endpoint(),
	}, nil
}

// startRegularMockHTTPServer starts a regular (non-OAuth) mock HTTP server
func (m *musterInstanceManager) startRegularMockHTTPServer(
	ctx context.Context,
	instanceID, configPath string,
	mcpServer MCPServerConfig,
	transportType mock.HTTPTransportType,
) (*MockHTTPServerInfo, error) {
	serverType, _ := mcpServer.Config["type"].(string)

	if m.debug {
		m.logger.Debug("🌐 Starting mock HTTP server for %s (transport: %s)\n", mcpServer.Name, serverType)
	}

	// Write mock config file
	mocksDir := filepath.Join(configPath, "mocks")
	mockConfigFile := filepath.Join(mocksDir, mcpServer.Name+".yaml")
	mockConfig := map[string]interface{}{
		"tools": mcpServer.Config["tools"],
	}

	yamlData, err := yaml.Marshal(mockConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal mock config: %w", err)
	}

	if err := os.WriteFile(mockConfigFile, yamlData, 0644); err != nil {
		return nil, fmt.Errorf("failed to write mock config: %w", err)
	}

	// Create and start the mock HTTP server
	httpServer, err := mock.NewHTTPServerFromConfig(mockConfigFile, transportType, m.debug)
	if err != nil {
		return nil, fmt.Errorf("failed to create mock HTTP server: %w", err)
	}

	port, err := httpServer.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start mock HTTP server: %w", err)
	}

	// Wait for server to be ready
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := httpServer.WaitForReady(readyCtx); err != nil {
		cancel()
		httpServer.Stop(ctx)
		return nil, fmt.Errorf("mock HTTP server not ready: %w", err)
	}
	cancel()

	// Track the server
	m.mu.Lock()
	m.mockHTTPServers[instanceID][mcpServer.Name] = httpServer
	m.mu.Unlock()

	if m.debug {
		m.logger.Debug("✅ Mock HTTP server %s started on port %d (endpoint: %s)\n",
			mcpServer.Name, port, httpServer.Endpoint())
	}

	return &MockHTTPServerInfo{
		Name:      mcpServer.Name,
		Port:      port,
		Transport: serverType,
		Endpoint:  httpServer.Endpoint(),
	}, nil
}

// extractToolConfigs extracts tool configurations from MCP server config
func (m *musterInstanceManager) extractToolConfigs(config map[string]interface{}) []mock.ToolConfig {
	var tools []mock.ToolConfig

	toolsInterface, exists := config["tools"]
	if !exists {
		return tools
	}

	toolsList, ok := toolsInterface.([]interface{})
	if !ok {
		return tools
	}

	for _, toolInterface := range toolsList {
		toolMap, ok := toStringMap(toolInterface)
		if !ok {
			continue
		}

		tool := mock.ToolConfig{}

		if name, ok := toolMap["name"].(string); ok {
			tool.Name = name
		}
		if desc, ok := toolMap["description"].(string); ok {
			tool.Description = desc
		}
		if schema, ok := toolMap["input_schema"].(map[string]interface{}); ok {
			tool.InputSchema = schema
		}

		// Extract responses
		if responses, ok := toolMap["responses"].([]interface{}); ok {
			for _, respInterface := range responses {
				respMap, ok := toStringMap(respInterface)
				if !ok {
					continue
				}

				resp := mock.ToolResponse{}
				if condition, ok := respMap["condition"].(map[string]interface{}); ok {
					resp.Condition = condition
				}
				if response, ok := respMap["response"]; ok {
					resp.Response = response
				}
				if errMsg, ok := respMap["error"].(string); ok {
					resp.Error = errMsg
				}
				if delay, ok := respMap["delay"].(string); ok {
					resp.Delay = delay
				}

				tool.Responses = append(tool.Responses, resp)
			}
		}

		tools = append(tools, tool)
	}

	return tools
}

// stopProtectedMCPServers stops all protected MCP servers for a given instance
func (m *musterInstanceManager) stopProtectedMCPServers(ctx context.Context, instanceID string) {
	m.mu.Lock()
	servers, exists := m.protectedMCPServers[instanceID]
	if exists {
		delete(m.protectedMCPServers, instanceID)
	}
	m.mu.Unlock()

	if !exists || len(servers) == 0 {
		return
	}

	for name, server := range servers {
		if m.debug {
			m.logger.Debug("🔒 Stopping protected MCP server %s\n", name)
		}

		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := server.Stop(stopCtx); err != nil {
			if m.debug {
				m.logger.Debug("⚠️  Failed to stop protected MCP server %s: %v\n", name, err)
			}
		}
		cancel()
	}
}

// GetMockOAuthServer returns a mock OAuth server by instance ID and server name
func (m *musterInstanceManager) GetMockOAuthServer(instanceID, serverName string) *mock.OAuthServer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if servers, ok := m.mockOAuthServers[instanceID]; ok {
		return servers[serverName]
	}
	return nil
}
