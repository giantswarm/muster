# MCP Aggregator Component

## Overview

The MCP Aggregator (`internal/aggregator`) is the core component responsible for unifying tools from multiple sources into a single, coherent interface for AI agents. It provides a unified MCP protocol interface that aggregates:

- **36 Core Built-in Tools** across 5 functional categories
- **Dynamic Workflow Tools** generated from workflow definitions  
- **External Tools** from configured MCP servers

The aggregator acts as an intelligent proxy that discovers, registers, filters, and routes tool calls to the appropriate underlying sources while providing consistent tool discovery and execution patterns.

## Architecture

### Core Responsibilities

1. **Core Tool Management**: Provide 36 built-in tools across 5 categories (config, mcpserver, service, serviceclass, workflow)
2. **Dynamic Tool Generation**: Generate workflow execution tools (workflow_*) from workflow definitions
3. **MCP Server Discovery**: Automatically discover and connect to configured external MCP servers
4. **Tool Aggregation**: Collect and register tools from all sources into a unified registry
5. **Unified Interface**: Provide a single MCP protocol interface to AI agents
6. **Tool Filtering**: Apply denylists and access controls to tool availability
7. **Request Routing**: Route tool calls to appropriate handlers (core, workflow engine, or external servers)
8. **Context Management**: Intelligent tool discovery to prevent context pollution

### Component Structure

```
internal/aggregator/
├── server.go          # Main aggregator MCP server implementation
├── registry.go        # Tool registration and management
├── tool_factory.go    # Dynamic tool creation and proxying
├── event_handler.go   # Server lifecycle event processing
├── denylist.go        # Tool filtering and access control
├── name_tracker.go    # Tool name conflict resolution
├── manager.go         # High-level orchestration
└── types.go          # Core data structures
```

## Key Components

### Registry (`registry.go`)

**Purpose**: Central tool registry managing all aggregated tools

**Key Functionality**:
- Tool registration from multiple sources
- Conflict resolution for duplicate tool names
- Tool metadata management
- Dynamic tool discovery

**Implementation Pattern**:
```go
type Registry struct {
    tools     map[string]*ToolEntry
    servers   map[string]*ServerEntry
    denylist  *Denylist
    mutex     sync.RWMutex
}

type ToolEntry struct {
    Name        string
    Description string
    Schema      *ToolSchema
    ServerID    string
    LastSeen    time.Time
}
```

**Key Methods**:
- `RegisterTool(serverID, toolName string, schema *ToolSchema) error`
- `UnregisterTool(serverID, toolName string) error`
- `GetTool(name string) (*ToolEntry, error)`
- `ListTools(filter *ToolFilter) ([]*ToolEntry, error)`

### Tool Factory (`tool_factory.go`)

**Purpose**: Dynamic creation of meta-tools for tool discovery and execution

**Meta-Tools Provided**:
1. `list_tools` - Enumerate available tools with filtering
2. `filter_tools` - Apply filters to tool lists
3. `call_tool` - Execute tools on underlying servers
4. `get_tool_schema` - Retrieve detailed tool schemas

**Implementation Pattern**:
```go
type ToolFactory struct {
    registry  *Registry
    mcpClient MCPClientInterface
}

func (f *ToolFactory) CreateListToolsTool() *Tool {
    return &Tool{
        Name:        "list_tools",
        Description: "List available tools from all connected MCP servers",
        Schema:      listToolsSchema,
        Handler:     f.handleListTools,
    }
}

func (f *ToolFactory) handleListTools(ctx context.Context, args map[string]interface{}) (interface{}, error) {
    filter := parseToolFilter(args)
    tools, err := f.registry.ListTools(filter)
    if err != nil {
        return nil, fmt.Errorf("failed to list tools: %w", err)
    }
    return formatToolList(tools), nil
}
```

### Event Handler (`event_handler.go`)

**Purpose**: Process server lifecycle events and maintain tool registry consistency

**Event Types**:
- Server connected/disconnected
- Tool added/removed/updated
- Server health status changes

**Implementation Pattern**:
```go
type EventHandler struct {
    registry   *Registry
    denylist   *Denylist
    notifier   EventNotifier
}

func (h *EventHandler) HandleServerConnected(serverID string, tools []*ToolDefinition) error {
    h.logger.Info("Server connected", "server_id", serverID, "tool_count", len(tools))
    
    for _, tool := range tools {
        if h.denylist.IsAllowed(tool.Name) {
            if err := h.registry.RegisterTool(serverID, tool.Name, tool.Schema); err != nil {
                h.logger.Error("Failed to register tool", "tool", tool.Name, "error", err)
            }
        }
    }
    
    h.notifier.NotifyServerStateChange(serverID, "connected")
    return nil
}
```

### Denylist (`denylist.go`)

**Purpose**: Implement fine-grained access control for tool availability

**Configuration Format**:
```yaml
denylist:
  # Block all tools from specific servers
  servers:
    - "untrusted-server"
  
  # Block specific tools by name
  tools:
    - "dangerous_command"
    - "system_admin_*"  # Wildcard patterns supported
  
  # Block tools by pattern
  patterns:
    - "^admin_.*"       # Regex patterns
    - ".*_delete$"
```

**Implementation**:
```go
type Denylist struct {
    BlockedServers []string          `yaml:"servers"`
    BlockedTools   []string          `yaml:"tools"`
    BlockedPatterns []string         `yaml:"patterns"`
    compiledPatterns []*regexp.Regexp
}

func (d *Denylist) IsAllowed(toolName, serverID string) bool {
    // Check server blocklist
    for _, blocked := range d.BlockedServers {
        if blocked == serverID {
            return false
        }
    }
    
    // Check tool name blocklist
    for _, blocked := range d.BlockedTools {
        if matched, _ := filepath.Match(blocked, toolName); matched {
            return false
        }
    }
    
    // Check regex patterns
    for _, pattern := range d.compiledPatterns {
        if pattern.MatchString(toolName) {
            return false
        }
    }
    
    return true
}
```

### Name Tracker (`name_tracker.go`)

**Purpose**: Handle tool name conflicts when multiple servers provide tools with the same name

**Conflict Resolution Strategies**:
1. **First-Come-First-Served**: First registered tool takes precedence
2. **Server Priority**: Configure server priority order
3. **Namespacing**: Automatically namespace conflicting tools

**Implementation**:
```go
type NameTracker struct {
    toolOwners    map[string]string  // tool name -> server ID
    conflicts     map[string][]string // tool name -> conflicting server IDs
    resolutionStrategy ConflictResolution
}

func (nt *NameTracker) RegisterTool(toolName, serverID string) (finalName string, conflict bool) {
    existing, exists := nt.toolOwners[toolName]
    
    if !exists {
        nt.toolOwners[toolName] = serverID
        return toolName, false
    }
    
    if existing == serverID {
        return toolName, false // Same server re-registering
    }
    
    // Handle conflict based on strategy
    switch nt.resolutionStrategy {
    case FirstComeFirstServed:
        return nt.createNamespacedName(toolName, serverID), true
    case ServerPriority:
        return nt.resolveByPriority(toolName, existing, serverID), true
    default:
        return nt.createNamespacedName(toolName, serverID), true
    }
}
```

## Integration Patterns

### API Integration

The aggregator integrates with the central API through the adapter pattern:

```go
// internal/aggregator/api_adapter.go
type Adapter struct {
    server *Server
    logger *slog.Logger
}

func (a *Adapter) ListTools(ctx context.Context, filter *ToolFilter) ([]*Tool, error) {
    return a.server.listTools(ctx, filter)
}

func (a *Adapter) CallTool(ctx context.Context, name string, args map[string]interface{}) (interface{}, error) {
    return a.server.callTool(ctx, name, args)
}

func (a *Adapter) Register() {
    api.RegisterAggregatorHandler(a)
}
```

### MCP Server Communication

Communication with underlying MCP servers follows the standard MCP protocol:

```go
type MCPClient interface {
    Connect(ctx context.Context, serverConfig *ServerConfig) error
    ListTools(ctx context.Context) ([]*ToolDefinition, error)
    CallTool(ctx context.Context, name string, args map[string]interface{}) (interface{}, error)
    Subscribe(handler EventHandler) error
    Disconnect(ctx context.Context) error
}
```

## Usage Patterns

### Tool Discovery Flow

1. **Agent Request**: AI agent calls `list_tools` meta-tool
2. **Filter Application**: Apply denylist and access controls
3. **Registry Query**: Query tool registry with filters
4. **Response Generation**: Format and return tool list

```go
func (s *Server) HandleListTools(ctx context.Context, request *ListToolsRequest) (*ListToolsResponse, error) {
    // Apply access controls
    allowed := s.denylist.FilterTools(request.Patterns)
    
    // Query registry
    tools, err := s.registry.ListTools(&ToolFilter{
        Patterns:    allowed,
        ServerIDs:   request.ServerFilter,
        Categories:  request.Categories,
    })
    if err != nil {
        return nil, fmt.Errorf("failed to query tools: %w", err)
    }
    
    // Format response
    return &ListToolsResponse{
        Tools: formatToolDescriptions(tools),
        Total: len(tools),
    }, nil
}
```

### Tool Execution Flow

1. **Agent Request**: AI agent calls `call_tool` with tool name and arguments
2. **Tool Resolution**: Resolve tool name to underlying server
3. **Request Forwarding**: Forward request to appropriate MCP server
4. **Response Processing**: Process and return response to agent

```go
func (s *Server) HandleCallTool(ctx context.Context, request *CallToolRequest) (*CallToolResponse, error) {
    // Resolve tool to server
    tool, err := s.registry.GetTool(request.ToolName)
    if err != nil {
        return nil, fmt.Errorf("tool not found: %w", err)
    }
    
    // Get MCP client for server
    client, err := s.getServerClient(tool.ServerID)
    if err != nil {
        return nil, fmt.Errorf("server not available: %w", err)
    }
    
    // Forward request
    result, err := client.CallTool(ctx, request.ToolName, request.Arguments)
    if err != nil {
        return nil, fmt.Errorf("tool execution failed: %w", err)
    }
    
    return &CallToolResponse{
        Result: result,
        ToolName: request.ToolName,
        ServerID: tool.ServerID,
    }, nil
}
```

## Configuration

### Server Configuration

```yaml
aggregator:
  # Server binding configuration
  bind:
    address: "127.0.0.1"
    port: 8080
    protocol: "stdio"  # or "http", "websocket"
  
  # Tool filtering configuration
  denylist:
    enabled: true
    config_file: "/etc/muster/denylist.yaml"
  
  # Server discovery configuration
  discovery:
    auto_discovery: true
    discovery_interval: "30s"
    health_check_interval: "10s"
  
  # Performance tuning
  performance:
    max_concurrent_calls: 100
    tool_cache_ttl: "5m"
    connection_pool_size: 10
```

### Tool Filter Configuration

```yaml
# Example denylist configuration
denylist:
  servers:
    - "development-server"    # Block entire server
  
  tools:
    - "admin_*"              # Block admin tools (wildcard)
    - "dangerous_command"     # Block specific tool
  
  patterns:
    - "^system_.*"           # Block system tools (regex)
    - ".*_delete$"           # Block delete operations
  
  categories:
    - "administrative"       # Block by category
    - "destructive"
```

## Error Handling

### Error Categories

1. **Connection Errors**: MCP server unavailable or disconnected
2. **Tool Errors**: Tool not found or execution failure
3. **Configuration Errors**: Invalid configuration or missing dependencies
4. **Authorization Errors**: Tool blocked by denylist or access controls

### Error Response Format

```go
type AggregatorError struct {
    Type      string `json:"type"`
    Message   string `json:"message"`
    ServerID  string `json:"server_id,omitempty"`
    ToolName  string `json:"tool_name,omitempty"`
    Timestamp string `json:"timestamp"`
}

func (e *AggregatorError) Error() string {
    return fmt.Sprintf("[%s] %s", e.Type, e.Message)
}
```

## Performance Considerations

### Caching Strategy

- **Tool Metadata**: Cache tool schemas and descriptions
- **Server Status**: Cache server health and availability
- **Response Caching**: Cache responses for idempotent operations

### Connection Management

- **Connection Pooling**: Maintain persistent connections to MCP servers
- **Health Monitoring**: Regular health checks for all connected servers
- **Graceful Degradation**: Continue operation when servers are unavailable

### Concurrency Handling

- **Request Concurrency**: Handle multiple simultaneous tool calls
- **Registry Locking**: Use read/write locks for registry access
- **Server Communication**: Async communication with MCP servers

## Monitoring and Observability

### Metrics

- **Tool Call Rate**: Number of tool calls per second
- **Server Health**: Connected/disconnected server count
- **Error Rate**: Failed tool calls and connection errors
- **Response Time**: Tool call latency distribution

### Logging

```go
func (s *Server) logToolCall(ctx context.Context, toolName, serverID string, duration time.Duration, err error) {
    fields := []slog.Attr{
        slog.String("tool_name", toolName),
        slog.String("server_id", serverID),
        slog.Duration("duration", duration),
    }
    
    if err != nil {
        fields = append(fields, slog.String("error", err.Error()))
        s.logger.LogAttrs(ctx, slog.LevelError, "Tool call failed", fields...)
    } else {
        s.logger.LogAttrs(ctx, slog.LevelInfo, "Tool call completed", fields...)
    }
}
```

## Testing Strategy

### Unit Testing

```go
func TestToolRegistration(t *testing.T) {
    registry := NewRegistry()
    
    tool := &ToolDefinition{
        Name:        "test_tool",
        Description: "Test tool",
        Schema:      &ToolSchema{},
    }
    
    err := registry.RegisterTool("server1", tool.Name, tool.Schema)
    assert.NoError(t, err)
    
    retrieved, err := registry.GetTool("test_tool")
    assert.NoError(t, err)
    assert.Equal(t, "test_tool", retrieved.Name)
    assert.Equal(t, "server1", retrieved.ServerID)
}
```

### Integration Testing

```go
func TestAggregatorIntegration(t *testing.T) {
    // Setup mock MCP servers
    mockServer1 := &MockMCPServer{
        Tools: []*ToolDefinition{
            {Name: "server1_tool", Description: "Tool from server 1"},
        },
    }
    
    // Create aggregator
    aggregator := NewAggregator()
    aggregator.AddServer("server1", mockServer1)
    
    // Test tool discovery
    tools, err := aggregator.ListTools(context.Background(), nil)
    assert.NoError(t, err)
    assert.Len(t, tools, 1)
    assert.Equal(t, "server1_tool", tools[0].Name)
    
    // Test tool execution
    result, err := aggregator.CallTool(context.Background(), "server1_tool", map[string]interface{}{})
    assert.NoError(t, err)
    assert.NotNil(t, result)
}
```

The MCP Aggregator serves as the intelligent hub that makes Muster's unified tool interface possible, providing seamless integration between AI agents and multiple underlying MCP servers while maintaining security, performance, and reliability. 