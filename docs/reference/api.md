# API Reference

Comprehensive reference for Muster's REST API and MCP (Model Context Protocol) interfaces.

## Overview

Muster provides a comprehensive API layer that supports multiple protocols and interfaces:

1. **MCP Aggregator API** - Primary MCP protocol interface for tool execution
2. **Core API Tools** - Built-in tools for managing Muster resources

## Base URLs and Endpoints

### MCP Aggregator Endpoints

| Transport | Endpoint | Purpose |
|-----------|----------|---------|
| **SSE** | `http://localhost:8080/sse` | Server-Sent Events for real-time communication |
| **Streamable HTTP** | `http://localhost:8080/mcp` | HTTP-based streaming protocol (default) |
| **Message Endpoint** | `http://localhost:8080/message` | HTTP message posting for SSE transport |
| **Stdio** | `stdio` | Standard I/O for easy integration |

## MCP Aggregator API

The MCP Aggregator is Muster's primary interface, aggregating tools from multiple MCP servers and exposing them through a unified API.

### Protocol Support

#### Streamable HTTP - Default
HTTP-based streaming for broader compatibility:

```bash
# List available tools
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"method": "tools/list", "params": {}}'

# Execute a tool
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type": application/json" \
  -d '{
    "method": "tools/call",
    "params": {
      "name": "x_kubernetes_get_pods",
      "arguments": {"namespace": "default"}
    }
  }'
```

#### Server-Sent Events (SSE) (old)
Real-time bidirectional communication with persistent connections:

```javascript
// Connect to SSE endpoint
const eventSource = new EventSource('http://localhost:8080/sse');

eventSource.onmessage = function(event) {
  const data = JSON.parse(event.data);
  console.log('Received:', data);
};

// Send messages via HTTP POST
fetch('http://localhost:8080/message', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({
    method: 'tools/list',
    params: {}
  })
});
```

## Core API Tools

Muster provides a comprehensive set of built-in tools for managing all aspects of the platform. These tools are organized into functional categories:

- **[Configuration Tools](#configuration-tools)** - System configuration management
- **[MCP Server Tools](#mcp-server-tools)** - MCP server lifecycle management  
- **[Service Tools](#service-tools)** - Service instance management
- **[ServiceClass Tools](#serviceclass-tools)** - ServiceClass definition management
- **[Workflow Tools](#workflow-tools)** - Workflow definition and execution management

> **Note**: Workflow execution is available through dynamically generated `workflow_<workflow-name>` tools. For example, if you have a workflow named "deploy-app", you can execute it using the `workflow_deploy-app` tool. Use the agent REPL (`muster agent --repl`) or `list tools` to discover available workflow execution tools.

### Configuration Tools

Tools for managing Muster system configuration and aggregator settings.

#### `core_config_get`

Retrieve current system configuration.

**Parameters:** None

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_config_get",
    "arguments": {}
  }
}
```

#### `core_config_get_aggregator`

Retrieve current aggregator configuration.

**Parameters:** None

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_config_get_aggregator",
    "arguments": {}
  }
}
```

#### `core_config_reload`

Reload system configuration from files.

**Parameters:** None

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_config_reload",
    "arguments": {}
  }
}
```

#### `core_config_save`

Save current configuration to files.

**Parameters:** None

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_config_save",
    "arguments": {}
  }
}
```

#### `core_config_update_aggregator`

Update aggregator configuration.

**Parameters:**
- `aggregator` (object, required) - Aggregator configuration object

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_config_update_aggregator",
    "arguments": {
      "aggregator": {
        "port": 8080,
        "host": "localhost"
      }
    }
  }
}
```

### MCP Server Tools

Tools for managing MCP server lifecycle, including creation, configuration, and monitoring.

#### `core_mcpserver_create`

Create a new MCP server configuration.

**Parameters:**
- `name` (string, required) - MCP server name
- `type` (string, required) - MCP server type (`stdio`, `streamable-http`, or `sse`)
- `description` (string, optional) - MCP server description
- `command` (array of strings, optional) - Command and arguments (for stdio type)
- `args` (array of strings, optional) - Command line arguments (for stdio type)
- `url` (string, optional) - Server endpoint URL (for streamable-http and sse types)
- `env` (object, optional) - Environment variables as key-value pairs
- `headers` (object, optional) - HTTP headers (for streamable-http and sse types)
- `timeout` (integer, optional) - Connection timeout in seconds
- `autoStart` (boolean, optional) - Whether server should auto-start

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_mcpserver_create",
    "arguments": {
      "name": "my-mcp-server",
      "type": "stdio",
      "description": "Custom MCP server for project management",
      "command": ["node", "/path/to/server.js"],
      "env": {
        "NODE_ENV": "production",
        "API_KEY": "secret"
      },
      "autoStart": true
    }
  }
}
```

#### `core_mcpserver_delete`

Delete an MCP server configuration.

**Parameters:**
- `name` (string, required) - Name of the MCP server to delete

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_mcpserver_delete",
    "arguments": {
      "name": "my-mcp-server"
    }
  }
}
```

#### `core_mcpserver_get`

Retrieve details of a specific MCP server.

**Parameters:**
- `name` (string, required) - Name of the MCP server to retrieve

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_mcpserver_get",
    "arguments": {
      "name": "my-mcp-server"
    }
  }
}
```

#### `core_mcpserver_list`

List all configured MCP servers.

**Parameters:** None

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_mcpserver_list",
    "arguments": {}
  }
}
```

#### `core_mcpserver_update`

Update an existing MCP server configuration.

**Parameters:**
- `name` (string, required) - MCP server name
- `type` (string, optional) - MCP server type (`stdio`, `streamable-http`, or `sse`)
- `description` (string, optional) - MCP server description
- `command` (array of strings, optional) - Command and arguments (for stdio type)
- `args` (array of strings, optional) - Command line arguments (for stdio type)
- `url` (string, optional) - Server endpoint URL (for streamable-http and sse types)
- `env` (object, optional) - Environment variables as key-value pairs
- `headers` (object, optional) - HTTP headers (for streamable-http and sse types)
- `timeout` (integer, optional) - Connection timeout in seconds
- `autoStart` (boolean, optional) - Whether server should auto-start

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_mcpserver_update",
    "arguments": {
      "name": "my-mcp-server",
      "description": "Updated description",
      "autoStart": false
    }
  }
}
```

#### `core_mcpserver_validate`

Validate MCP server configuration without creating it.

**Parameters:**
- `name` (string, required) - MCP server name
- `type` (string, required) - MCP server type (`stdio`, `streamable-http`, or `sse`)
- `description` (string, optional) - MCP server description
- `command` (array of strings, optional) - Command and arguments (for stdio type)
- `args` (array of strings, optional) - Command line arguments (for stdio type)
- `url` (string, optional) - Server endpoint URL (for streamable-http and sse types)
- `env` (object, optional) - Environment variables as key-value pairs
- `headers` (object, optional) - HTTP headers (for streamable-http and sse types)
- `timeout` (integer, optional) - Connection timeout in seconds
- `autoStart` (boolean, optional) - Whether server should auto-start

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_mcpserver_validate",
    "arguments": {
      "name": "test-server",
      "type": "stdio",
      "command": ["node", "server.js"]
    }
  }
}
```

### Service Tools

Tools for managing service instances, including lifecycle operations and status monitoring.

#### `core_service_create`

Create a new service instance from a ServiceClass.

**Parameters:**
- `serviceClassName` (string, required) - Name of the ServiceClass to instantiate
- `name` (string, required) - Unique name for the service instance
- `args` (object, optional) - Arguments for service creation (depends on ServiceClass definition)
- `persist` (boolean, optional) - Whether to persist this service instance definition to YAML files
- `autoStart` (boolean, optional) - Whether this instance should be started automatically on system startup

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_service_create",
    "arguments": {
      "serviceClassName": "database",
      "name": "prod-db",
      "args": {
        "port": 5432,
        "database": "production"
      },
      "persist": true,
      "autoStart": true
    }
  }
}
```

#### `core_service_delete`

Delete a service instance.

**Parameters:**
- `name` (string, required) - Name of the ServiceClass instance to delete

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_service_delete",
    "arguments": {
      "name": "prod-db"
    }
  }
}
```

#### `core_service_get`

Retrieve details of a specific service instance.

**Parameters:**
- `name` (string, required) - Name of the service to get

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_service_get",
    "arguments": {
      "name": "prod-db"
    }
  }
}
```

#### `core_service_list`

List all service instances.

**Parameters:** None

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_service_list",
    "arguments": {}
  }
}
```

#### `core_service_restart`

Restart a service instance.

**Parameters:**
- `name` (string, required) - Service name to restart

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_service_restart",
    "arguments": {
      "name": "prod-db"
    }
  }
}
```

#### `core_service_start`

Start a service instance.

**Parameters:**
- `name` (string, required) - Service name to start

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_service_start",
    "arguments": {
      "name": "prod-db"
    }
  }
}
```

#### `core_service_status`

Get the status of a service instance.

**Parameters:**
- `name` (string, required) - Service name to get status for

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_service_status",
    "arguments": {
      "name": "prod-db"
    }
  }
}
```

#### `core_service_stop`

Stop a service instance.

**Parameters:**
- `name` (string, required) - Service name to stop

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_service_stop",
    "arguments": {
      "name": "prod-db"
    }
  }
}
```

#### `core_service_validate`

Validate service configuration without creating it.

**Parameters:**
- `name` (string, required) - Service instance name
- `serviceClassName` (string, required) - Name of the ServiceClass to instantiate
- `args` (object, optional) - Arguments for service creation
- `autoStart` (boolean, optional) - Whether this instance should auto-start
- `description` (string, optional) - Service instance description

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_service_validate",
    "arguments": {
      "name": "test-db",
      "serviceClassName": "database",
      "args": {
        "port": 5433
      }
    }
  }
}
```

### ServiceClass Tools

Tools for managing ServiceClass definitions that serve as templates for service instances.

#### `core_serviceclass_available`

Check if a ServiceClass is available and properly configured.

**Parameters:**
- `name` (string, required) - Name of the ServiceClass to check

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_serviceclass_available",
    "arguments": {
      "name": "database"
    }
  }
}
```

#### `core_serviceclass_create`

Create a new ServiceClass definition.

**Parameters:**
- `name` (string, required) - ServiceClass name
- `serviceConfig` (object, required) - ServiceClass service configuration
  - `serviceType` (string, required) - Type of service this configuration manages
  - `lifecycleTools` (object, required) - Tools for managing service lifecycle
    - `start` (object, required) - Tool configuration for start operations
    - `stop` (object, required) - Tool configuration for stop operations
    - `restart` (object, optional) - Tool configuration for restart operations
    - `status` (object, optional) - Tool configuration for status operations
    - `healthCheck` (object, optional) - Tool configuration for health check operations
  - `defaultName` (string, optional) - Default name pattern for service instances
  - `dependencies` (array of strings, optional) - List of ServiceClass names that must be available
  - `outputs` (object, optional) - Template-based outputs for service instances
  - `timeout` (object, optional) - Timeout configuration for service operations
  - `healthCheck` (object, optional) - Health check configuration
- `args` (object, optional) - ServiceClass arguments schema
- `description` (string, optional) - ServiceClass description

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_serviceclass_create",
    "arguments": {
      "name": "web-service",
      "description": "Generic web service template",
      "serviceConfig": {
        "serviceType": "web",
        "lifecycleTools": {
          "start": {
            "tool": "docker_start",
            "args": {
              "image": "{{.image}}"
            }
          },
          "stop": {
            "tool": "docker_stop"
          }
        }
      },
      "args": {
        "image": {
          "type": "string",
          "required": true,
          "description": "Docker image to run"
        },
        "port": {
          "type": "integer",
          "default": 8080,
          "description": "Port to expose"
        }
      }
    }
  }
}
```

#### `core_serviceclass_delete`

Delete a ServiceClass definition.

**Parameters:**
- `name` (string, required) - Name of the ServiceClass to delete

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_serviceclass_delete",
    "arguments": {
      "name": "web-service"
    }
  }
}
```

#### `core_serviceclass_get`

Retrieve details of a specific ServiceClass.

**Parameters:**
- `name` (string, required) - Name of the ServiceClass to retrieve

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_serviceclass_get",
    "arguments": {
      "name": "web-service"
    }
  }
}
```

#### `core_serviceclass_list`

List all ServiceClass definitions.

**Parameters:** None

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_serviceclass_list",
    "arguments": {}
  }
}
```

#### `core_serviceclass_update`

Update an existing ServiceClass definition.

**Parameters:**
- `name` (string, required) - ServiceClass name
- `serviceConfig` (object, optional) - ServiceClass service configuration
- `args` (object, optional) - ServiceClass arguments schema  
- `description` (string, optional) - ServiceClass description

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_serviceclass_update",
    "arguments": {
      "name": "web-service",
      "description": "Updated web service template with monitoring"
    }
  }
}
```

#### `core_serviceclass_validate`

Validate ServiceClass configuration without creating it.

**Parameters:**
- `name` (string, required) - ServiceClass name
- `serviceConfig` (object, required) - ServiceClass service configuration
- `args` (object, optional) - ServiceClass arguments schema
- `description` (string, optional) - ServiceClass description

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_serviceclass_validate",
    "arguments": {
      "name": "test-service",
      "serviceConfig": {
        "serviceType": "test",
        "lifecycleTools": {
          "start": {"tool": "echo"},
          "stop": {"tool": "echo"}
        }
      }
    }
  }
}
```

### Workflow Tools

Tools for managing workflow definitions and execution tracking.

#### `core_workflow_available`

Check if a workflow is available and properly configured.

**Parameters:**
- `name` (string, required) - Name of the workflow

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_workflow_available",
    "arguments": {
      "name": "deploy-application"
    }
  }
}
```

#### `core_workflow_create`

Create a new workflow definition.

**Parameters:**
- `name` (string, required) - Name of the workflow
- `steps` (array, required) - Workflow steps (minimum 1 step)
  - Each step object contains:
    - `id` (string, required) - Unique identifier for this step
    - `tool` (string, required) - Name of the tool to execute
    - `description` (string, optional) - Human-readable documentation
    - `args` (object, optional) - Arguments to pass to the tool
    - `outputs` (object, optional) - Output variable assignments
    - `condition` (object, optional) - Conditional execution logic
    - `allow_failure` (boolean, optional) - Whether step can fail without failing workflow
    - `store` (boolean, optional) - Whether to store step result in workflow results
- `args` (object, optional) - Workflow arguments definition
- `description` (string, optional) - Description of the workflow

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_workflow_create",
    "arguments": {
      "name": "deploy-web-service",
      "description": "Deploy a web service with health checks",
      "args": {
        "service_name": {
          "type": "string",
          "required": true,
          "description": "Name of the service to deploy"
        },
        "image": {
          "type": "string",
          "required": true,
          "description": "Docker image to deploy"
        }
      },
      "steps": [
        {
          "id": "create_service",
          "tool": "core_service_create",
          "description": "Create the service instance",
          "args": {
            "serviceClassName": "web-service",
            "name": "{{.service_name}}",
            "args": {
              "image": "{{.image}}"
            }
          }
        },
        {
          "id": "start_service",
          "tool": "core_service_start",
          "description": "Start the service",
          "args": {
            "name": "{{.service_name}}"
          }
        },
        {
          "id": "check_health",
          "tool": "core_service_status",
          "description": "Verify service is healthy",
          "args": {
            "name": "{{.service_name}}"
          }
        }
      ]
    }
  }
}
```

#### `core_workflow_delete`

Delete a workflow definition.

**Parameters:**
- `name` (string, required) - Name of the workflow to delete

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_workflow_delete",
    "arguments": {
      "name": "deploy-web-service"
    }
  }
}
```

#### `core_workflow_execution_get`

Retrieve details of a specific workflow execution.

**Parameters:**
- `execution_id` (string, required) - ID of the execution
- `include_steps` (boolean, optional, default: true) - Include step details
- `step_id` (string, optional) - Get specific step details

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_workflow_execution_get",
    "arguments": {
      "execution_id": "exec_123456",
      "include_steps": true
    }
  }
}
```

#### `core_workflow_execution_list`

List workflow executions with optional filtering.

**Parameters:**
- `limit` (number, optional, default: 50) - Maximum number of executions to return
- `offset` (number, optional, default: 0) - Number of executions to skip
- `status` (string, optional) - Filter by execution status
- `workflow_name` (string, optional) - Filter by workflow name

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_workflow_execution_list",
    "arguments": {
      "limit": 10,
      "status": "completed",
      "workflow_name": "deploy-web-service"
    }
  }
}
```

#### `core_workflow_get`

Retrieve details of a specific workflow definition.

**Parameters:**
- `name` (string, required) - Name of the workflow

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_workflow_get",
    "arguments": {
      "name": "deploy-web-service"
    }
  }
}
```

#### `core_workflow_list`

List all workflow definitions.

**Parameters:**
- `include_system` (boolean, optional, default: true) - Include system-defined workflows

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_workflow_list",
    "arguments": {
      "include_system": false
    }
  }
}
```

#### `core_workflow_update`

Update an existing workflow definition.

**Parameters:**
- `name` (string, required) - Name of the workflow to update
- `steps` (array, required) - Workflow steps (minimum 1 step)
- `args` (object, optional) - Workflow arguments definition
- `description` (string, optional) - Description of the workflow

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_workflow_update",
    "arguments": {
      "name": "deploy-web-service",
      "description": "Updated deployment workflow with rollback support",
      "steps": [
        {
          "id": "create_service",
          "tool": "core_service_create",
          "args": {
            "serviceClassName": "web-service",
            "name": "{{.service_name}}"
          }
        }
      ]
    }
  }
}
```

#### `core_workflow_validate`

Validate workflow configuration without creating it.

**Parameters:**
- `name` (string, required) - Name of the workflow
- `steps` (array, required) - Workflow steps (minimum 1 step)
- `args` (object, optional) - Workflow arguments definition
- `description` (string, optional) - Description of the workflow

**Example:**
```json
{
  "method": "tools/call",
  "params": {
    "name": "core_workflow_validate",
    "arguments": {
      "name": "test-workflow",
      "steps": [
        {
          "id": "test_step",
          "tool": "core_service_list"
        }
      ]
    }
  }
}
```

## Error Handling

All tools follow consistent error handling patterns:

### Success Response
```json
{
  "isError": false,
  "content": [
    {
      "type": "text",
      "text": "Operation completed successfully"
    }
  ]
}
```

### Error Response
```json
{
  "isError": true,
  "content": [
    {
      "type": "text", 
      "text": "Error: Resource not found"
    }
  ]
}
```

### Common Error Types

- **Validation Errors** - Invalid parameters or configuration
- **Not Found Errors** - Resource does not exist
- **Conflict Errors** - Resource already exists or conflicts with existing resources
- **Dependency Errors** - Required dependencies not available
- **Timeout Errors** - Operation exceeded configured timeout

## Authentication and Authorization

Currently, Muster operates in a trusted environment without authentication. Future versions may include:

- API key authentication
- Role-based access control (RBAC)
- Service account tokens
- Integration with external identity providers

## Rate Limiting

No rate limiting is currently implemented. Consider implementing appropriate rate limiting for production deployments.

## Versioning

The API follows semantic versioning principles. The current schema version is **1.0.0**.

## Tool Discovery

### Interactive Discovery with Agent REPL
The Muster agent provides powerful interactive capabilities for discovering and executing tools:

```bash
# Start the interactive agent
muster agent --repl

# Discover tools
list tools                           # List all available tools
filter tools core_*                  # Filter tools by pattern
describe core_service_create         # Get detailed tool documentation

# Execute tools
call core_service_list               # Execute without arguments
call core_service_create {           # Execute with JSON arguments
  "serviceClassName": "web-service",
  "name": "test-app"
}
```

### Programmatic Discovery
```bash
# List tools via MCP API
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"method": "tools/list", "params": {}}'

# Get tool schema
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"method": "tools/get_schema", "params": {"name": "core_service_create"}}'
```

## Related Documentation

- **[CLI Reference](../cli/)** - Command-line interface documentation
- **[Agent CLI Reference](../cli/agent.md)** - Interactive agent and REPL usage
- **[Configuration Reference](../configuration.md)** - Detailed configuration options
- **[MCP Tools documentation](reference/mcp-tools.md)** - All the core mcp tools
- **[CRD reference](reference/crds.md/)** - Kubernetes Custom Resource definitions
- **[Getting Started](../../getting-started/)** - Setup and basic usage
- **[How-to Guides](../../how-to/)** - Task-oriented implementation guides
- **[Workflow Creation Guide](../../how-to/workflow-creation.md)** - Step-by-step workflow creation
- **[ServiceClass Patterns](../../how-to/serviceclass-patterns.md)** - Common ServiceClass patterns
