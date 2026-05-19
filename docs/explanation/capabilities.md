# Core Capabilities of Muster

Muster provides **comprehensive platform management capabilities** through 36 core built-in tools organized into 5 functional categories, plus dynamic workflow execution tools and external tools from your configured MCP servers.

## 🧰 Core Tool Categories

### **Configuration Tools** (5 tools)
System configuration management and aggregator settings:
```bash
# Get current system configuration
core_config_get

# Update aggregator settings (port, host, transport)
core_config_update_aggregator

# Save configuration changes persistently
core_config_save
```

### **MCP Server Tools** (6 tools)
Manage external MCP server definitions and lifecycle:
```bash
# List all configured MCP servers (like kubernetes, prometheus, grafana)
core_mcpserver_list

# Create new MCP server definitions
core_mcpserver_create

# Validate server configurations before deployment
core_mcpserver_validate
```

### **Service Tools** (deprecated)
Inspect the aggregator's view of upstream MCPServers. Pause/resume moved to
`MCPServer.spec.suspended` (set via `core_mcpserver_update`); the force-reconnect
verb moved to `core_mcpserver_reconnect`. The remaining surface is scheduled
for removal when muster's `/mcp` goes away in Phase 8.
```bash
# Discover what upstream MCPServers the aggregator sees
core_service_list

# Monitor a single MCPServer's aggregator-side dial state
core_service_status
```

### **Workflow Tools** (9 tools)
Define and execute multi-step orchestrated processes:
```bash
# List available workflows
core_workflow_list

# Create complex multi-step workflows
core_workflow_create

# Track workflow execution history
core_workflow_execution_list
core_workflow_execution_get
```

## 🚀 Dynamic Tool Generation

### **Workflow Execution Tools**
For each workflow you define, Muster automatically generates a `workflow_<name>` execution tool. Based on this instance's configuration:

```bash
# Generated from your actual workflows:
workflow_connect-monitoring           # Connect to Giant Swarm monitoring
workflow_check-cilium-health         # Check Cilium network health
workflow_login-management-cluster    # Login to management cluster
workflow_login-workload-cluster      # Login to workload cluster
workflow_discovery                   # Service discovery workflow
workflow_auth                        # Authentication workflow
```

## 🧠 Intelligent Tool Discovery

Your agent can now discover and use tools dynamically:

```bash
# Discover available tools by category
agent: "What configuration tools are available?"
→ Shows core_config_get, core_config_update_aggregator, etc.

# Find tools by functionality
agent: "I need to manage Kubernetes connections"
→ Discovers workflow 'connect-k8s' and related tools

# Execute complex operations
agent: "Connect to monitoring in cluster"
→ workflow_connect-monitoring(cluster="foo-bar.k8s.mydomain.com")
```

## 🛡️ Smart Access Control & Context Optimization

### **Tool Filtering**
- **Denylist Protection**: Block destructive tools by default (override with `--yolo`)
- **Context-Aware Loading**: Only load tools when needed to minimize agent context
- **Project-Based Control**: Different tool sets for different projects

### **Prerequisites Management**
Workflows handle complex prerequisites:
- **Port Forwarding**: Automatically set up when accessing remote services
- **Authentication**: Handle cluster logins and token management
- **Health Checking**: Continuous monitoring of service availability
- **Cleanup**: Automatic resource cleanup when services stop

## 📊 Real-World Integration Example

**Complete End-to-End Scenario:**
1. **Agent Request**: "I need to check Cilium health in the gazelle installation"
2. **Workflow Discovery**: Finds `workflow_check-cilium-health`
3. **Service Creation**: Creates required port-forwarding services
4. **Authentication**: Handles cluster authentication automatically
5. **Health Check**: Executes comprehensive Cilium health verification
6. **Result Delivery**: Returns structured health status and recommendations
7. **Cleanup**: Automatically cleans up temporary resources

This demonstrates how Muster transforms complex, multi-step platform operations into simple, one-command executions for AI agents while maintaining full control and observability.
