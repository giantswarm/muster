// Package agent provides comprehensive MCP (Model Context Protocol) client and server
// implementations for debugging, testing, and integrating with the muster aggregator.
//
// The agent package enables multiple interaction modes with MCP servers, from interactive
// debugging to AI assistant integration, with built-in caching, notifications, and
// comprehensive error handling.
//
// # Quick Start
//
// For immediate MCP server interaction:
//
//	logger := agent.NewLogger(true, true, false)
//	client := agent.NewClient("http://localhost:8090/sse", logger, agent.TransportSSE)
//	repl := agent.NewREPL(client, logger)
//	ctx := context.Background()
//	repl.Run(ctx) // Interactive REPL
//
// For programmatic tool execution:
//
//	client := agent.NewClient("http://localhost:8090/sse", nil, agent.TransportSSE)
//	defer client.Close()
//	if err := client.Connect(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	result, err := client.CallToolSimple(ctx, "core_service_list", nil)
//
// # Architecture Overview
//
// The agent package follows a modular architecture with clear separation of concerns:
//
//	┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
//	│   REPL Mode     │    │   Agent Mode    │    │ MCP Server Mode │
//	│  (Interactive)  │    │  (Monitoring)   │    │ (AI Assistant)  │
//	└─────────────────┘    └─────────────────┘    └─────────────────┘
//	         │                       │                       │
//	         └───────────────────────┼───────────────────────┘
//	                                 │
//	                    ┌─────────────────┐
//	                    │     Client      │
//	                    │   (Core MCP)    │
//	                    └─────────────────┘
//	                                 │
//	                    ┌─────────────────┐
//	                    │   Transport     │
//	                    │ (SSE/HTTP-S)    │
//	                    └─────────────────┘
//
// # Core Components
//
// ## Client
//
// The foundational MCP client handles protocol communication, connection management,
// caching, and notification processing. It supports both SSE and Streamable HTTP
// transports with automatic selection and graceful degradation.
//
// Key features:
//   - Thread-safe caching with diff tracking
//   - Configurable timeouts and retry logic
//   - Multiple output formats (text, JSON, structured)
//   - Real-time notification handling
//
// ## REPL
//
// Interactive Read-Eval-Print Loop with a modular command system, tab completion,
// command history, and real-time notification display. Commands are self-contained
// and extensible through the Command interface.
//
// Available commands:
//   - help (?): Command documentation and usage
//   - list (ls): List tools, resources, prompts with filtering
//   - describe (desc): Detailed capability information
//   - call: Execute tools with argument validation
//   - get: Retrieve resources and execute prompts
//   - filter: Advanced pattern-based tool filtering
//   - notifications: Toggle and manage real-time updates
//   - prompt: Template-based prompt execution
//   - exit (quit): Graceful session termination
//
// ## MCP Server
//
// Exposes all agent functionality as MCP tools for AI assistant integration.
// Uses stdio transport for seamless integration with AI systems like Claude Desktop.
//
// Exposed tools:
//   - list_tools, list_resources, list_prompts: Capability discovery
//   - describe_tool, describe_resource, describe_prompt: Detailed schemas
//   - call_tool: Execute any available tool with validation
//   - get_resource, get_prompt: Content retrieval and template execution
//   - filter_tools, list_core_tools: Advanced filtering and categorization
//
// ## Logger
//
// Structured logging system with multiple output modes:
//   - Simple mode: User-friendly status messages
//   - JSON-RPC mode: Complete protocol debugging
//   - Verbose mode: Detailed operation tracking
//   - Custom writers: Flexible output routing
//
// ## Formatters
//
// Consistent formatting utilities for MCP data with support for both
// human-readable console output and structured JSON responses.
//
// # Transport Support
//
// ## SSE (Server-Sent Events) - Recommended
//
// Real-time bidirectional communication with full notification support:
//
//   - Persistent connection for continuous monitoring
//
//   - Event streaming for immediate capability updates
//
//   - Ideal for interactive use and AI assistant integration
//
//     client := agent.NewClient("http://localhost:8090/sse", logger, agent.TransportSSE)
//
// ## Streamable HTTP - Compatibility
//
// Request-response pattern for restricted environments:
//
//   - No persistent connection or real-time notifications
//
//   - Stateless operations suitable for automation
//
//   - Ideal for CLI scripts and batch processing
//
//     client := agent.NewClient("http://localhost:8090/streamable-http", logger, agent.TransportStreamableHTTP)
//
// # Operation Modes
//
// ## Agent Mode (Monitoring)
//
// Connects to an MCP aggregator and monitors for real-time changes:
//
//	logger := agent.NewLogger(true, true, false)
//	client := agent.NewClient("http://localhost:8090/sse", logger, agent.TransportSSE)
//	if err := client.Run(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
// Use cases:
//   - Debugging aggregator behavior
//   - Monitoring tool availability changes
//   - Development and testing workflows
//   - Real-time capability tracking
//
// ## REPL Mode (Interactive)
//
// Provides an interactive command-line interface:
//
//	logger := agent.NewLogger(true, true, false)
//	client := agent.NewClient("http://localhost:8090/sse", logger, agent.TransportSSE)
//	repl := agent.NewREPL(client, logger)
//	if err := repl.Run(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
// Features:
//   - Tab completion for commands and tool names
//   - Persistent command history
//   - Real-time notification display
//   - Flexible output formats
//   - Graceful error recovery
//
// ## MCP Server Mode (AI Assistant Integration)
//
// Exposes agent functionality as MCP tools:
//
//	logger := agent.NewLogger(true, true, false)
//	client := agent.NewClient("http://localhost:8090/sse", logger, agent.TransportSSE)
//	server, err := agent.NewMCPServer(client, logger, false)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if err := server.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
// AI assistant configuration:
//
//	{
//	  "mcpServers": {
//	    "muster": {
//	      "command": "muster",
//	      "args": ["agent", "--mcp-server", "--endpoint", "http://localhost:8090/sse"]
//	    }
//	  }
//	}
//
// ## CLI Mode (Programmatic)
//
// Direct client usage for automation:
//
//	client := agent.NewClient("http://localhost:8090/sse", nil, agent.TransportSSE)
//	defer client.Close()
//	if err := client.Connect(ctx); err != nil {
//	    return err
//	}
//
//	// Execute multiple tools
//	tools := []string{"core_service_list", "core_serviceclass_list"}
//	for _, tool := range tools {
//	    result, err := client.CallToolSimple(ctx, tool, nil)
//	    if err != nil {
//	        log.Printf("Tool %s failed: %v", tool, err)
//	        continue
//	    }
//	    fmt.Printf("Tool %s result: %s\n", tool, result)
//	}
//
// # Caching and Performance
//
// ## Intelligent Caching
//
// The agent implements multi-level caching for optimal performance:
//
//   - Tools, resources, and prompts cached after initial retrieval
//
//   - Diff tracking shows exactly what changed during notifications
//
//   - Thread-safe access with RWMutex protection
//
//   - Optional disable for always-fresh data
//
//     client := agent.NewClient(endpoint, logger, transport)
//     client.SetCacheEnabled(false) // Disable caching for testing
//
// ## Notification System
//
// Real-time updates with intelligent cache management:
//   - Automatic refresh triggered by change notifications
//   - Change visualization shows added/removed/modified items
//   - Background processing keeps UI responsive
//   - Configurable notification filtering
//
// ## Performance Characteristics
//
//   - Lazy loading: Data fetched only when needed
//   - Concurrent operations: Multiple requests handled simultaneously
//   - Connection pooling: Efficient resource utilization
//   - Configurable timeouts: Prevent hanging operations
//
// # Advanced Features
//
// ## Custom Logging
//
//	// Silent mode for automation
//	logger := agent.NewDevNullLogger()
//
//	// Custom output destination
//	var buffer bytes.Buffer
//	logger := agent.NewLoggerWithWriter(true, false, false, &buffer)
//
//	// JSON-RPC protocol debugging
//	logger := agent.NewLogger(true, true, true)
//
// ## Tool Filtering and Search
//
// Advanced filtering capabilities:
//
//	// REPL pattern matching
//	filter_tools pattern:core_* description_filter:service
//
//	// Programmatic filtering
//	tools := client.GetToolCache()
//	filtered := filterTools(tools, "core_*", "service", false)
//
// ## Resource Management
//
// Comprehensive resource handling:
//
//	// Automatic MIME type detection
//	resource, err := client.GetResource(ctx, "file://config.yaml")
//	if err != nil {
//	    return err
//	}
//
//	// Handle different content types
//	for _, content := range resource.Contents {
//	    switch content.MIMEType {
//	    case "application/json":
//	        // Process JSON
//	    case "text/plain":
//	        // Process text
//	    default:
//	        // Handle binary or unknown
//	    }
//	}
//
// ## Prompt Templating
//
// Dynamic prompt execution:
//
//	result, err := client.GetPrompt(ctx, "code_review", map[string]string{
//	    "language": "go",
//	    "style":    "google",
//	    "file":     "client.go",
//	})
//
// # Error Handling and Recovery
//
// ## Connection Recovery
//
//   - Automatic reconnection on connection failures
//   - Graceful degradation when notifications unavailable
//   - Configurable timeout handling
//   - Proper resource cleanup
//
// ## Error Classification
//
//   - Protocol errors: MCP handshake and communication failures
//   - Tool errors: Tool execution failures with context
//   - Network errors: Transport-level failures with retry logic
//   - Validation errors: Arg and schema mismatches
//
// ## Debugging Support
//
//	// Enable comprehensive debugging
//	logger := agent.NewLogger(true, true, true) // verbose, color, jsonRPC
//	client := agent.NewClient(endpoint, logger, transport)
//
//	// Inspect client state
//	tools := client.GetToolCache()
//	resources := client.GetResourceCache()
//	prompts := client.GetPromptCache()
//
// # Testing Patterns
//
// ## Unit Testing
//
//	func TestToolExecution(t *testing.T) {
//	    logger := agent.NewDevNullLogger()
//	    client := agent.NewClient(testServerURL, logger, agent.TransportSSE)
//	    defer client.Close()
//
//	    if err := client.Connect(ctx); err != nil {
//	        t.Fatalf("Connection failed: %v", err)
//	    }
//
//	    result, err := client.CallToolSimple(ctx, "test_tool", nil)
//	    if err != nil {
//	        t.Fatalf("Tool execution failed: %v", err)
//	    }
//
//	    if result != "expected_result" {
//	        t.Errorf("Expected 'expected_result', got '%s'", result)
//	    }
//	}
//
// ## Integration Testing
//
//	func TestREPLIntegration(t *testing.T) {
//	    logger := agent.NewDevNullLogger()
//	    client := agent.NewClient(testServerURL, logger, agent.TransportSSE)
//	    repl := agent.NewREPL(client, logger)
//
//	    // Test command execution
//	    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	    defer cancel()
//
//	    go func() {
//	        if err := repl.Run(ctx); err != nil {
//	            t.Errorf("REPL failed: %v", err)
//	        }
//	    }()
//
//	    // Verify functionality
//	    tools := client.GetToolCache()
//	    if len(tools) == 0 {
//	        t.Error("No tools available")
//	    }
//	}
//
// ## Mock Testing
//
//	func TestWithMockServer(t *testing.T) {
//	    mockServer := startMockMCPServer(t)
//	    defer mockServer.Close()
//
//	    client := agent.NewClient(mockServer.URL, nil, agent.TransportSSE)
//	    defer client.Close()
//
//	    // Test with predictable responses
//	    result, err := client.CallToolSimple(ctx, "mock_tool", nil)
//	    assert.NoError(t, err)
//	    assert.Equal(t, "mock_result", result)
//	}
//
// # Performance Optimization
//
// ## Connection Management
//
//	// Reuse connections for multiple operations
//	client := agent.NewClient(endpoint, logger, transport)
//	defer client.Close()
//
//	if err := client.Connect(ctx); err != nil {
//	    return err
//	}
//
//	// Multiple operations on same connection
//	for _, operation := range operations {
//	    result, err := client.CallToolSimple(ctx, operation.Tool, operation.Args)
//	    // Process result
//	}
//
// ## Batch Operations
//
//	// Concurrent tool execution
//	var wg sync.WaitGroup
//	results := make(chan toolResult, len(tools))
//
//	for _, tool := range tools {
//	    wg.Add(1)
//	    go func(toolName string) {
//	        defer wg.Done()
//	        result, err := client.CallToolSimple(ctx, toolName, nil)
//	        results <- toolResult{tool: toolName, result: result, err: err}
//	    }(tool)
//	}
//
//	wg.Wait()
//	close(results)
//
// ## Memory Management
//
//	// Disable caching for memory-constrained environments
//	client.SetCacheEnabled(false)
//
//	// Use streaming for large resources
//	resource, err := client.GetResource(ctx, "large://dataset.json")
//	if err != nil {
//	    return err
//	}
//	// Process resource content in chunks
//
// # Thread Safety
//
// All operations are designed for concurrent use:
//
// ## Cache Protection
//
//   - RWMutex locks protect cache data during reads and writes
//   - Atomic operations for simple state changes
//   - Consistent lock ordering prevents deadlocks
//
// ## Notification Handling
//
//   - Background goroutines process notifications without blocking
//   - Channel communication for safe cross-goroutine data sharing
//   - Context cancellation for graceful shutdown
//
// ## Resource Management
//
//   - Proper cleanup ensures resources are released
//   - Context propagation enables operation cancellation
//   - Timeout handling prevents resource leaks
//
// # Integration Examples
//
// ## CI/CD Pipeline Integration
//
//	#!/bin/bash
//	# Deploy services using muster agent
//	muster agent --endpoint http://muster:8090/streamable-http --execute <<EOF
//	call core_service_create '{"name": "web-app", "serviceclass": "webapp", "args": {"replicas": 3}}'
//	call core_service_start '{"name": "web-app"}'
//	EOF
//
// ## Monitoring Script
//
//	func monitorServices(ctx context.Context) error {
//	    client := agent.NewClient("http://localhost:8090/sse", logger, agent.TransportSSE)
//	    defer client.Close()
//
//	    if err := client.Connect(ctx); err != nil {
//	        return err
//	    }
//
//	    ticker := time.NewTicker(30 * time.Second)
//	    defer ticker.Stop()
//
//	    for {
//	        select {
//	        case <-ctx.Done():
//	            return ctx.Err()
//	        case <-ticker.C:
//	            services, err := client.CallToolJSON(ctx, "core_service_list", nil)
//	            if err != nil {
//	                log.Printf("Failed to list services: %v", err)
//	                continue
//	            }
//	            // Process services
//	        }
//	    }
//	}
//
// ## Custom AI Assistant Tool
//
//	func createCustomMCPTool() {
//	    client := agent.NewClient("http://localhost:8090/sse", logger, agent.TransportSSE)
//	    server, err := agent.NewMCPServer(client, logger, true)
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//
//	    // Add custom tools
//	    server.AddCustomTool("deploy_stack", func(args map[string]interface{}) (interface{}, error) {
//	        // Custom deployment logic
//	        return client.CallToolJSON(ctx, "core_service_create", args)
//	    })
//
//	    server.Start(context.Background())
//	}
//
// This package provides a comprehensive foundation for MCP client development,
// testing, and integration within the muster ecosystem, with a focus on
// reliability, performance, and developer experience.
package agent
