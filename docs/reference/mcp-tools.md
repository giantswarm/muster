# Muster Core MCP Tools Reference

Reference guide for AI agents and MCP clients working with Muster's tools. This document covers the meta-tools interface and the built-in tools that Muster provides for managing platform resources.

## Meta-Tools (Primary Interface)

**All tool access goes through these meta-tools.** MCP clients see only these 11 meta-tools when they connect to Muster. All other tools are accessed via `call_tool`.

### Tool Discovery

| Meta-Tool | Description | Arguments |
|-----------|-------------|-----------|
| `list_tools` | List all available tools for the session | `{}` |
| `describe_tool` | Get detailed schema for a specific tool | `{"name": "tool_name"}` |
| `filter_tools` | Search tools by pattern | `{"pattern": "...", "description": "..."}` |
| `list_core_tools` | List only Muster core tools | `{}` |

### Tool Execution

| Meta-Tool | Description | Arguments |
|-----------|-------------|-----------|
| `call_tool` | Execute any tool by name | `{"name": "tool_name", "arguments": {...}}` |

**Example:**
```json
{
  "name": "call_tool",
  "arguments": {
    "name": "core_service_list",
    "arguments": {}
  }
}
```

### Resource Access

| Meta-Tool | Description | Arguments |
|-----------|-------------|-----------|
| `list_resources` | List available MCP resources | `{}` |
| `describe_resource` | Get resource metadata | `{"uri": "resource_uri"}` |
| `get_resource` | Read resource contents | `{"uri": "resource_uri"}` |

### Prompt Access

| Meta-Tool | Description | Arguments |
|-----------|-------------|-----------|
| `list_prompts` | List available prompts | `{}` |
| `describe_prompt` | Get prompt details | `{"name": "prompt_name"}` |
| `get_prompt` | Execute a prompt | `{"name": "prompt_name", "arguments": {...}}` |

---

## Core Tools Overview

Muster provides **36 core built-in tools** organized into 5 functional categories. These are accessed via `call_tool`:

- **[Configuration Tools](#configuration-tools)** (5 tools) - System configuration management
- **[MCP Server Tools](#mcp-server-tools)** (6 tools) - MCP server lifecycle management
- **[Service Tools](#service-tools)** (9 tools) - Service instance management  
- **[ServiceClass Tools](#serviceclass-tools)** (7 tools) - ServiceClass definition management
- **[Workflow Tools](#workflow-tools)** (9 tools) - Workflow definition and execution management

### Additional Tool Types

Beyond the 36 core tools, Muster also provides access to:

- **[Dynamic Workflow Execution Tools](#dynamic-workflow-execution-tools)** - `workflow_<name>` tools generated from your workflow definitions
- **[External Tools](#external-tools)** - Tools provided by your configured MCP servers (varies by installation)

> **Important**: All tools below are accessed via `call_tool(name="...", arguments={...})`. They are not directly visible to MCP clients.

## Quick Start

### Basic Discovery Pattern
```bash
# Use meta-tools to discover
list_tools()                    # See all available tools
filter_tools(pattern="core_*")  # Filter to core tools only

# Execute tools via call_tool
call_tool(name="core_service_list", arguments={})
call_tool(name="core_serviceclass_list", arguments={})
call_tool(name="core_workflow_list", arguments={})
call_tool(name="workflow_<name>", arguments={...})
```

### Common Operations
```bash
# Check system status
call_tool(name="core_service_list", arguments={})
call_tool(name="core_mcpserver_list", arguments={})

# Create and manage services
call_tool(name="core_service_create", arguments={...})
call_tool(name="core_service_start", arguments={"name": "my-service"})
call_tool(name="core_service_status", arguments={"name": "my-service"})

# Execute workflows
call_tool(name="workflow_<your-workflow>", arguments={...})
```

---

## Configuration Tools

Manage Muster system configuration and aggregator settings. These tools allow you to read, modify, and persist configuration changes.

### `core_config_get`
Get the complete current Muster system configuration including aggregator, services, and other settings.

**Arguments:** None

**Returns:** Complete configuration object with all system settings

**Example Request:**
```json
{
  "name": "core_config_get",
  "arguments": {}
}
```

**Example Response:**
```json
{
  "Aggregator": {
    "Port": 8090,
    "Host": "localhost", 
    "Transport": "streamable-http",
    "Enabled": true,
    "MusterPrefix": ""
  }
}
```

### `core_config_get_aggregator`
Get aggregator-specific configuration details only.

**Arguments:** None

**Returns:** Aggregator configuration object

**Example Request:**
```json
{
  "name": "core_config_get_aggregator",
  "arguments": {}
}
```

**Example Response:**
```json
{
  "Port": 8090,
  "Host": "localhost",
  "Transport": "streamable-http", 
  "Enabled": true,
  "MusterPrefix": ""
}
```

**Use Cases:**
- Check current aggregator endpoint and settings
- Verify aggregator is enabled before tool operations
- Get connection details for external clients

### `core_config_reload`
Reload configuration from configuration files, discarding any in-memory changes.

**Arguments:** None

**Returns:** Operation status and any errors encountered

**Example Request:**
```json
{
  "name": "core_config_reload",
  "arguments": {}
}
```

**Use Cases:**
- Refresh configuration after manual file edits
- Revert in-memory changes to last saved state
- Reload after external configuration updates

**⚠️ Warning:** This discards any unsaved configuration changes.

### `core_config_save`
Save the current in-memory configuration to configuration files.

**Arguments:** None

**Returns:** Save operation status and file paths written

**Example Request:**
```json
{
  "name": "core_config_save",
  "arguments": {}
}
```

**Use Cases:**
- Persist configuration changes made through API
- Create backup of current configuration
- Ensure changes survive system restarts

### `core_config_update_aggregator`
Update aggregator configuration settings.

**Arguments:**
- `aggregator` (object, required) - Aggregator configuration object with the following properties:
  - `Port` (integer, optional) - Aggregator listen port (default: 8090)
  - `Host` (string, optional) - Aggregator bind host (default: "localhost")
  - `Transport` (string, optional) - Transport type ("streamable-http", "sse", "stdio")
  - `Enabled` (boolean, optional) - Whether aggregator is enabled
  - `MusterPrefix` (string, optional) - Prefix for Muster core tools

**Returns:** Updated aggregator configuration

**Example Request:**
```json
{
  "name": "core_config_update_aggregator",
  "arguments": {
    "aggregator": {
      "Port": 8080,
      "Host": "0.0.0.0",
      "Transport": "streamable-http",
      "Enabled": true
    }
  }
}
```

**Use Cases:**
- Change aggregator listen port or host
- Switch transport protocols (HTTP/SSE/stdio)
- Enable/disable aggregator functionality
- Configure tool prefixes

**⚠️ Note:** Changes take effect immediately but are not persisted until `core_config_save` is called.

---

## MCP Server Tools

Manage MCP server definitions and lifecycle. These tools control the external MCP servers that provide additional capabilities like Kubernetes, Prometheus, or custom tooling.

> **Note**: MCP servers are user-defined and not part of Muster's core functionality. They are external processes that provide specialized tools and capabilities.

### `core_mcpserver_list`
List all configured MCP servers with their definitions and metadata.

**Arguments:** None

**Returns:** Object containing array of MCP server definitions with configuration storage information

**Example Request:**
```json
{
  "name": "core_mcpserver_list",
  "arguments": {}
}
```

**Example Response:**
```json
{
  "mcpServers": [
    {
      "name": "my-custom-server",
      "type": "stdio",
      "autoStart": true,
      "description": "Custom MCP server for specialized tools",
      "command": ["my-server", "serve"],
      "env": {
        "API_KEY": "secret123"
      }
    }
  ],
  "mode": "filesystem",
  "total": 1
}
```

### `core_mcpserver_create`
Create a new MCP server definition that can be started as a service.

**Arguments:**
- `name` (string, required) - Unique server name (used as service identifier)
- `type` (string, required) - Server type (`stdio`, `streamable-http`, or `sse`)
- `description` (string, optional) - Human-readable description of server purpose
- `command` (array of strings, optional) - Command executable and arguments (for stdio servers)
- `args` (array of strings, optional) - Command line arguments (for stdio servers)
- `url` (string, optional) - Server endpoint URL (for streamable-http and sse servers)
- `env` (object, optional) - Environment variables as key-value pairs
- `headers` (object, optional) - HTTP headers (for streamable-http and sse servers)
- `timeout` (integer, optional) - Connection timeout in seconds
- `autoStart` (boolean, optional) - Whether to start automatically on system startup

**Returns:** Created MCP server definition

**Example Request:**
```json
{
  "name": "core_mcpserver_create",
  "arguments": {
    "name": "my-tools",
    "type": "stdio",
    "description": "Custom tool server for project management",
    "command": ["my-mcp-server", "--port", "3000", "--verbose"],
    "env": {
      "API_KEY": "abc123",
      "LOG_LEVEL": "debug"
    },
    "autoStart": true
  }
}
```

**Use Cases:**
- Add custom MCP servers with specialized tools
- Configure third-party MCP server integrations
- Set up development or testing tool servers

### `core_mcpserver_get`
Get detailed information about a specific MCP server definition.

**Arguments:**
- `name` (string, required) - Name of the MCP server to retrieve

**Returns:** Complete MCP server definition object

**Example Request:**
```json
{
  "name": "core_mcpserver_get",
  "arguments": {
    "name": "my-tools"
  }
}
```

**Example Response:**
```json
{
  "name": "my-tools",
  "type": "stdio",
  "autoStart": true,
  "description": "Custom tool server for project management",
  "command": ["my-mcp-server", "--port", "3000", "--verbose"],
  "env": {
    "API_KEY": "abc123",
    "LOG_LEVEL": "debug"
  }
}
```

### `core_mcpserver_update`
Update an existing MCP server definition. Only provided fields are updated.

**Arguments:**
- `name` (string, required) - Name of the MCP server to update
- `type` (string, optional) - Server type
- `description` (string, optional) - Updated description
- `command` (array of strings, optional) - New command and arguments
- `env` (object, optional) - Updated environment variables (replaces existing)
- `autoStart` (boolean, optional) - Auto-start setting

**Returns:** Updated MCP server definition

**Example Request:**
```json
{
  "name": "core_mcpserver_update",
  "arguments": {
    "name": "my-tools",
    "description": "Updated: Custom tool server with monitoring",
    "env": {
      "API_KEY": "new-key-456",
      "LOG_LEVEL": "info",
      "METRICS_ENABLED": "true"
    }
  }
}
```

**Use Cases:**
- Update server configurations without recreating
- Modify environment variables or command arguments
- Change auto-start behavior

### `core_mcpserver_delete`
Delete an MCP server definition. Server must be stopped before deletion.

**Arguments:**
- `name` (string, required) - Name of the MCP server to delete

**Returns:** Deletion confirmation

**Example Request:**
```json
{
  "name": "core_mcpserver_delete",
  "arguments": {
    "name": "my-tools"
  }
}
```

**Use Cases:**
- Remove unused or deprecated MCP servers
- Clean up test server configurations
- Decommission replaced servers

**⚠️ Warning:** Ensure server is stopped before deletion. Use `core_service_stop` first if needed.

### `core_mcpserver_validate`
Validate MCP server configuration without creating or modifying the server.

**Arguments:**
- `name` (string, required) - Server name to validate
- `type` (string, required) - Server type to validate
- `description` (string, optional) - Description to validate
- `command` (array of strings, optional) - Command to validate
- `env` (object, optional) - Environment variables to validate
- `autoStart` (boolean, optional) - Auto-start setting to validate

**Returns:** Validation result with any errors or warnings

**Example Request:**
```json
{
  "name": "core_mcpserver_validate",
  "arguments": {
    "name": "test-server",
    "type": "stdio",
    "autoStart": true,
    "command": ["echo", "test"],
    "description": "Test server configuration"
  }
}
```

**Use Cases:**
- Test server configurations before creation
- Validate command existence and permissions
- Check environment variable formats
- Ensure name uniqueness

---

## Service Tools

Manage service instances throughout their lifecycle. Services can be static system services (aggregator, MCP servers) or dynamic ServiceClass-based instances.

> **Service Types**: Muster manages three types of services:
> - **Aggregator**: Core tool aggregation service
> - **MCPServer**: External MCP server processes  
> - **ServiceClass**: User-defined service instances from templates

### `core_service_list`
List all services with their current status and metadata.

**Arguments:** None

**Returns:** Object containing array of all services with detailed status information

**Example Request:**
```json
{
  "name": "core_service_list",
  "arguments": {}
}
```

**Example Response:**
```json
{
  "services": [
    {
      "name": "mcp-aggregator",
      "service_type": "Aggregator",
      "state": "running",
      "health": "healthy",
      "metadata": {
        "port": 8090,
        "tools": 95,
        "servers_connected": 6
      }
    },
    {
      "name": "kubernetes",
      "service_type": "MCPServer",
      "state": "running", 
      "health": "healthy",
      "metadata": {
        "autoStart": true,
        "command": ["mcp-kubernetes"]
      }
    },
    {
      "name": "my-database",
      "service_type": "ServiceClass",
      "state": "stopped", 
      "health": "unknown",
      "metadata": {
        "serviceClassName": "database-service",
        "autoStart": false
      }
    }
  ],
  "total": 3
}
```

### `core_service_create`
Create a new ServiceClass-based service instance.

**Arguments:**
- `serviceClassName` (string, required) - Name of ServiceClass template to instantiate
- `name` (string, required) - Unique name for the service instance
- `args` (object, optional) - Arguments for service creation (schema depends on ServiceClass)
- `persist` (boolean, optional) - Whether to persist service definition to YAML files (default: false)
- `autoStart` (boolean, optional) - Whether to auto-start on system startup (only if persist=true)

**Returns:** Created service instance definition

**Example Request:**
```json
{
  "name": "core_service_create",
  "arguments": {
    "serviceClassName": "database-service",
    "name": "prod-database",
    "args": {
      "name": "production",
      "port": 5432,
      "replicas": 3
    },
    "persist": true,
    "autoStart": true
  }
}
```

**Use Cases:**
- Create database connections with port forwarding
- Set up monitoring service instances
- Deploy temporary development environments
- Create persistent production services

**⚠️ Note:** Only ServiceClass-based services can be created via this tool. MCP servers use `core_mcpserver_create`.

### `core_service_get`
Get detailed information about any service (static or ServiceClass-based).

**Arguments:**
- `name` (string, required) - Name of the service to retrieve

**Returns:** Complete service definition with status and metadata

**Example Request:**
```json
{
  "name": "core_service_get",
  "arguments": {
    "name": "mcp-aggregator"
  }
}
```

**Example Response:**
```json
{
  "name": "mcp-aggregator",
  "serviceClassName": "",
  "args": null,
  "enabled": false,
  "state": "running",
  "health": "healthy",
  "serviceData": {
    "service_type": "Aggregator",
    "port": 8090,
    "tools": 95,
    "servers_connected": 6,
    "endpoint": "http://localhost:8090/mcp"
  }
}
```

### `core_service_start`
Start a specific service (works for both static and ServiceClass-based services).

**Arguments:**
- `name` (string, required) - Name of the service to start

**Returns:** Operation status and service state

**Example Request:**
```json
{
  "name": "core_service_start",
  "arguments": {
    "name": "my-database"
  }
}
```

**Use Cases:**
- Start stopped services
- Restart failed services
- Manually start services with autoStart=false

**⚠️ Note:** Static services (aggregator) may not support start operations.

### `core_service_stop`
Stop a specific service (works for both static and ServiceClass-based services).

**Arguments:**
- `name` (string, required) - Name of the service to stop

**Returns:** Operation status and service state

**Example Request:**
```json
{
  "name": "core_service_stop",
  "arguments": {
    "name": "my-database"
  }
}
```

**Use Cases:**
- Stop services for maintenance
- Temporarily disable resource-intensive services
- Clean shutdown before updates

**⚠️ Warning:** Stopping critical services (aggregator, MCP servers) may disrupt tool availability.

### `core_service_restart`
Restart a specific service (stop then start operation).

**Arguments:**
- `name` (string, required) - Name of the service to restart

**Returns:** Operation status and final service state

**Example Request:**
```json
{
  "name": "core_service_restart",
  "arguments": {
    "name": "kubernetes"
  }
}
```

**Use Cases:**
- Apply configuration changes that require restart
- Recover from service errors or hangs
- Refresh connections or reinitialize state

### `core_service_status`
Get current status information for a specific service.

**Arguments:**
- `name` (string, required) - Name of the service to check

**Returns:** Detailed status including state, health, and runtime information

**Example Request:**
```json
{
  "name": "core_service_status",
  "arguments": {
    "name": "mcp-aggregator"
  }
}
```

**Use Cases:**
- Monitor service health and performance
- Troubleshoot service issues
- Get real-time status for dashboards

### `core_service_delete`
Delete a ServiceClass-based service instance (static services cannot be deleted).

**Arguments:**
- `name` (string, required) - Name of the ServiceClass instance to delete

**Returns:** Deletion confirmation

**Example Request:**
```json
{
  "name": "core_service_delete",
  "arguments": {
    "name": "temp-database"
  }
}
```

**Use Cases:**
- Remove temporary or test services
- Clean up unused service instances
- Decommission obsolete services

**⚠️ Warning:** Service must be stopped before deletion. Static services (aggregator, MCP servers) cannot be deleted.

### `core_service_validate`
Validate a service instance definition without creating the service.

**Arguments:**
- `name` (string, required) - Service instance name to validate
- `serviceClassName` (string, required) - ServiceClass to validate against
- `args` (object, optional) - Arguments to validate (schema depends on ServiceClass)
- `autoStart` (boolean, optional) - Auto-start setting to validate
- `description` (string, optional) - Service description to validate

**Returns:** Validation result with errors, warnings, and required tools check

**Example Request:**
```json
{
  "name": "core_service_validate",
  "arguments": {
    "name": "test-database",
    "serviceClassName": "database-service",
    "args": {
      "name": "testdb",
      "port": "invalid-port"
    }
  }
}
```

**Use Cases:**
- Test service configurations before creation
- Validate argument schemas and types
- Check ServiceClass availability and dependencies
- Ensure required tools are available

---

## ServiceClass Tools

Manage ServiceClass definitions that serve as templates for creating service instances. ServiceClasses define how to start, stop, monitor, and manage services using external tools.

> **ServiceClass Concept**: A ServiceClass is a reusable template that defines how to manage a specific type of service (database, web server, monitoring, etc.) using MCP tools for lifecycle operations.

### `core_serviceclass_list`
List all ServiceClass definitions with their availability status and configuration details.

**Arguments:** None

**Returns:** Object containing array of ServiceClass definitions with tool availability information

**Example Request:**
```json
{
  "name": "core_serviceclass_list",
  "arguments": {}
}
```

**Example Response:**
```json
{
  "mode": "filesystem",
  "serviceClasses": [
    {
      "name": "database-service",
      "description": "Database service template with health checks",
      "available": true,
      "args": {
        "port": {
          "type": "integer",
          "required": false,
          "default": 5432,
          "description": "Database port"
        },
        "name": {
          "type": "string", 
          "required": true,
          "description": "Database name"
        }
      },
      "requiredTools": ["x_kubernetes_create", "x_kubernetes_delete"],
      "missingTools": []
    }
  ],
  "total": 1
}
```

### `core_serviceclass_create`
Create a new ServiceClass definition with complete lifecycle tool configuration.

**Arguments:**
- `name` (string, required) - Unique ServiceClass name
- `serviceConfig` (object, required) - Core service configuration:
  - `serviceType` (string, required) - Type identifier for the service
  - `lifecycleTools` (object, required) - Tool configurations for service management:
    - `start` (object, required) - Tool configuration for starting services
    - `stop` (object, required) - Tool configuration for stopping services  
    - `status` (object, optional) - Tool configuration for status checks
    - `restart` (object, optional) - Tool configuration for restart operations
    - `healthCheck` (object, optional) - Tool configuration for health verification
  - `healthCheck` (object, optional) - Health check settings:
    - `enabled` (boolean) - Whether health checks are active
    - `interval` (string) - Check interval (e.g., "30s", "1m")
    - `failureThreshold` (integer) - Failures before marking unhealthy
    - `successThreshold` (integer) - Successes to mark healthy
  - `timeout` (object, optional) - Operation timeout configuration
  - `dependencies` (array, optional) - Required ServiceClass dependencies
  - `outputs` (object, optional) - Template-based output generation
- `args` (object, optional) - Argument schema with validation rules
- `description` (string, optional) - Human-readable description

**Returns:** Created ServiceClass definition

**Example Request:**
```json
{
  "name": "core_serviceclass_create",
  "arguments": {
    "name": "port-forward-service",
    "description": "Kubernetes port forwarding service template",
    "serviceConfig": {
      "serviceType": "port-forward",
      "lifecycleTools": {
        "start": {
          "tool": "x_kubernetes_port_forward",
          "args": {
            "namespace": "{{.namespace}}",
            "resourceName": "{{.podName}}",
            "ports": ["{{.localPort}}:{{.remotePort}}"]
          },
          "outputs": {
            "sessionID": "result.sessionID",
            "endpoint": "result.endpoint"
          }
        },
        "stop": {
          "tool": "x_kubernetes_stop_port_forward_session",
          "args": {
            "sessionID": "{{.sessionID}}"
          }
        },
        "status": {
          "tool": "x_kubernetes_list_port_forward_sessions"
        }
      },
      "healthCheck": {
        "enabled": true,
        "interval": "30s",
        "failureThreshold": 3
      }
    },
    "args": {
      "namespace": {
        "type": "string",
        "required": true,
        "description": "Kubernetes namespace"
      },
      "podName": {
        "type": "string", 
        "required": true,
        "description": "Pod name to forward to"
      },
      "localPort": {
        "type": "integer",
        "required": true,
        "description": "Local port number"
      },
      "remotePort": {
        "type": "integer",
        "default": 8080,
        "description": "Remote port number"
      }
    }
  }
}
```

**Use Cases:**
- Define reusable service templates
- Standardize service management patterns
- Create complex multi-tool workflows
- Implement health checking and monitoring

### `core_serviceclass_get`
Get detailed information about a specific ServiceClass definition.

**Arguments:**
- `name` (string, required) - Name of the ServiceClass to retrieve

**Returns:** Complete ServiceClass definition with all configuration details

**Example Request:**
```json
{
  "name": "core_serviceclass_get",
  "arguments": {
    "name": "port-forward-service"
  }
}
```

**Use Cases:**
- Inspect ServiceClass configurations
- Debug service creation issues
- Understand required arguments and dependencies

### `core_serviceclass_available`
Check if a ServiceClass is available for use (all required tools are present).

**Arguments:**
- `name` (string, required) - Name of the ServiceClass to check

**Returns:** Availability status with details about missing tools or dependencies

**Example Request:**
```json
{
  "name": "core_serviceclass_available",
  "arguments": {
    "name": "database-service"
  }
}
```

**Use Cases:**
- Verify ServiceClass dependencies before creating services
- Troubleshoot unavailable ServiceClasses
- Check tool availability after MCP server changes

### `core_serviceclass_update`
Update an existing ServiceClass definition. Only provided fields are updated.

**Arguments:**
- `name` (string, required) - Name of the ServiceClass to update
- `serviceConfig` (object, optional) - Updated service configuration
- `args` (object, optional) - Updated argument schema
- `description` (string, optional) - Updated description

**Returns:** Updated ServiceClass definition

**Example Request:**
```json
{
  "name": "core_serviceclass_update",
  "arguments": {
    "name": "port-forward-service",
    "description": "Updated: Enhanced port forwarding with health checks",
    "serviceConfig": {
      "healthCheck": {
        "enabled": true,
        "interval": "15s",
        "failureThreshold": 2
      }
    }
  }
}
```

**Use Cases:**
- Modify ServiceClass behavior without recreation
- Update tool configurations or arguments
- Enhance existing templates with new features

### `core_serviceclass_delete`
Delete a ServiceClass definition. All service instances must be stopped first.

**Arguments:**
- `name` (string, required) - Name of the ServiceClass to delete

**Returns:** Deletion confirmation

**Example Request:**
```json
{
  "name": "core_serviceclass_delete",
  "arguments": {
    "name": "obsolete-service"
  }
}
```

**Use Cases:**
- Remove obsolete or deprecated ServiceClasses
- Clean up test templates
- Reorganize service architecture

**⚠️ Warning:** Ensure no active service instances exist before deletion.

### `core_serviceclass_validate`
Validate a ServiceClass definition without creating it.

**Arguments:**
- `name` (string, required) - ServiceClass name to validate
- `serviceConfig` (object, required) - Service configuration to validate
- `args` (object, optional) - Argument schema to validate
- `description` (string, optional) - Description to validate

**Returns:** Validation result with errors, warnings, and tool availability check

**Example Request:**
```json
{
  "name": "core_serviceclass_validate",
  "arguments": {
    "name": "test-service",
    "serviceConfig": {
      "serviceType": "test",
      "lifecycleTools": {
        "start": {
          "tool": "nonexistent_tool",
          "args": {}
        },
        "stop": {
          "tool": "x_kubernetes_delete"
        }
      }
    }
  }
}
```

**Use Cases:**
- Test ServiceClass configurations before creation
- Validate tool availability and argument schemas
- Check for configuration errors and dependencies
- Ensure ServiceClass compatibility with current environment

---

## Workflow Tools

Manage workflow definitions and track executions. Workflows orchestrate multi-step processes with advanced features like templating, conditional execution, and output chaining.

> **Workflow Concept**: Workflows are reusable, multi-step processes that execute tools in sequence, with support for conditional logic, variable passing between steps, and comprehensive execution tracking.

### `core_workflow_list`
List all workflow definitions with their availability status.

**Arguments:** None

**Returns:** Object containing array of workflow definitions

**Example Request:**
```json
{
  "name": "core_workflow_list",
  "arguments": {}
}
```

**Example Response:**
```json
{
  "workflows": [
    {
      "name": "deploy-application",
      "description": "Deploy application with monitoring setup",
      "available": true
    },
    {
      "name": "backup-database", 
      "description": "Backup database to remote storage",
      "available": false
    }
  ]
}
```

### `core_workflow_create`
Create a new workflow definition with advanced step configuration.

**Arguments:**
- `name` (string, required) - Unique workflow name
- `steps` (array, required) - Array of workflow steps (minimum 1):
  - `id` (string, required) - Unique step identifier within workflow
  - `tool` (string, required) - Tool name to execute for this step
  - `description` (string, optional) - Human-readable step documentation
  - `args` (object, optional) - Tool arguments with templating support (`{{.argName}}`, `{{stepId.field}}`)
  - `outputs` (object, optional) - Output variable assignments for subsequent steps
  - `condition` (object, optional) - Conditional execution configuration:
    - `tool` (string, required) - Tool to call for condition evaluation
    - `args` (object, optional) - Arguments for condition tool
    - `expect` (object, optional) - Expected results for condition success
  - `allow_failure` (boolean, optional) - Whether step failure should not fail entire workflow
  - `store` (boolean, optional) - Whether to store step result in workflow results
- `args` (object, optional) - Workflow argument schema with validation:
  - Each argument has: `type`, `required`, `default`, `description`
  - Supported types: `string`, `integer`, `boolean`, `number`, `object`, `array`
- `description` (string, optional) - Workflow description

**Returns:** Created workflow definition

**Example Request:**
```json
{
  "name": "core_workflow_create",
  "arguments": {
    "name": "deploy-with-monitoring",
    "description": "Deploy application and setup monitoring with health checks",
    "args": {
      "app_name": {
        "type": "string",
        "required": true,
        "description": "Application name to deploy"
      },
      "environment": {
        "type": "string", 
        "default": "development",
        "description": "Target environment"
      },
      "health_check": {
        "type": "boolean",
        "default": true,
        "description": "Enable health checking"
      }
    },
    "steps": [
      {
        "id": "create_service",
        "tool": "core_service_create",
        "description": "Create the main application service",
        "args": {
          "serviceClassName": "web-application",
          "name": "{{.app_name}}-{{.environment}}",
          "args": {
            "replicas": 3,
            "environment": "{{.environment}}"
          }
        },
        "outputs": {
          "service_name": "{{.app_name}}-{{.environment}}",
          "endpoint": "http://{{.app_name}}-{{.environment}}.local"
        }
      },
      {
        "id": "start_service",
        "tool": "core_service_start",
        "description": "Start the application service",
        "args": {
          "name": "{{create_service.service_name}}"
        }
      },
      {
        "id": "setup_monitoring",
        "tool": "core_service_create",
        "description": "Setup monitoring service", 
        "condition": {
          "tool": "core_serviceclass_available",
          "args": {
            "name": "monitoring-service"
          },
          "expect": {
            "success": true
          }
        },
        "args": {
          "serviceClassName": "monitoring-service",
          "name": "{{.app_name}}-monitoring",
          "args": {
            "target": "{{create_service.endpoint}}"
          }
        },
        "allow_failure": true
      },
      {
        "id": "health_check",
        "tool": "core_service_status",
        "description": "Verify service is healthy",
        "condition": {
          "tool": "echo",
          "args": {
            "value": "{{.health_check}}"
          },
          "expect": {
            "json_path": {
              "value": true
            }
          }
        },
        "args": {
          "name": "{{create_service.service_name}}"
        },
        "store": true
      }
    ]
  }
}
```

**Use Cases:**
- Automate complex deployment processes
- Create reusable operational procedures
- Implement conditional logic workflows
- Chain multiple service operations

### `core_workflow_get`
Get detailed information about a specific workflow definition.

**Arguments:**
- `name` (string, required) - Name of the workflow to retrieve

**Returns:** Complete workflow definition with all steps and configuration

**Example Request:**
```json
{
  "name": "core_workflow_get",
  "arguments": {
    "name": "deploy-with-monitoring"
  }
}
```

**Use Cases:**
- Inspect workflow configurations
- Debug workflow step definitions
- Understand argument requirements and step dependencies

### `core_workflow_available`
Check if a workflow is available for execution (all required tools are present).

**Arguments:**
- `name` (string, required) - Name of the workflow to check

**Returns:** Availability status with details about missing tools or dependencies

**Example Request:**
```json
{
  "name": "core_workflow_available",
  "arguments": {
    "name": "deploy-with-monitoring"
  }
}
```

**Use Cases:**
- Verify workflow dependencies before execution
- Troubleshoot unavailable workflows
- Check tool availability after system changes

### `core_workflow_update`
Update an existing workflow definition. Only provided fields are updated.

**Arguments:**
- `name` (string, required) - Name of the workflow to update
- `steps` (array, optional) - Updated workflow steps (replaces all existing steps)
- `args` (object, optional) - Updated argument schema
- `description` (string, optional) - Updated description

**Returns:** Updated workflow definition

**Example Request:**
```json
{
  "name": "core_workflow_update",
  "arguments": {
    "name": "deploy-with-monitoring",
    "description": "Updated: Enhanced deployment with advanced monitoring and rollback",
    "steps": [
      {
        "id": "validate_environment",
        "tool": "core_serviceclass_available",
        "description": "Validate deployment environment",
        "args": {
          "name": "web-application"
        }
      },
      {
        "id": "create_service",
        "tool": "core_service_create",
        "description": "Create the main application service",
        "args": {
          "serviceClassName": "web-application",
          "name": "{{.app_name}}-{{.environment}}"
        }
      }
    ]
  }
}
```

**Use Cases:**
- Modify workflow behavior without recreation
- Add new steps to existing workflows
- Update step configurations or arguments

### `core_workflow_delete`
Delete a workflow definition. Active executions are not affected.

**Arguments:**
- `name` (string, required) - Name of the workflow to delete

**Returns:** Deletion confirmation

**Example Request:**
```json
{
  "name": "core_workflow_delete",
  "arguments": {
    "name": "obsolete-workflow"
  }
}
```

**Use Cases:**
- Remove obsolete or deprecated workflows
- Clean up test workflows
- Reorganize workflow library

**⚠️ Note:** Deleting a workflow does not affect running executions or execution history.

### `core_workflow_validate`
Validate a workflow definition without creating it.

**Arguments:**
- `name` (string, required) - Workflow name to validate
- `steps` (array, required) - Workflow steps to validate
- `args` (object, optional) - Argument schema to validate
- `description` (string, optional) - Description to validate

**Returns:** Validation result with errors, warnings, and tool availability check

**Example Request:**
```json
{
  "name": "core_workflow_validate",
  "arguments": {
    "name": "test-workflow",
    "steps": [
      {
        "id": "invalid_step",
        "tool": "nonexistent_tool",
        "args": {
          "invalid_template": "{{.nonexistent_arg}}"
        }
      }
    ]
  }
}
```

**Use Cases:**
- Test workflow configurations before creation
- Validate step dependencies and tool availability
- Check template syntax and argument references
- Ensure workflow compatibility with current environment

### `core_workflow_execution_list`
List workflow execution history with filtering options.

**Arguments:**
- `limit` (number, optional, default: 50) - Maximum number of executions to return
- `offset` (number, optional, default: 0) - Number of executions to skip (pagination)
- `status` (string, optional) - Filter by execution status (`running`, `completed`, `failed`, `cancelled`)
- `workflow_name` (string, optional) - Filter by specific workflow name

**Returns:** Array of workflow executions with metadata

**Example Request:**
```json
{
  "name": "core_workflow_execution_list",
  "arguments": {
    "workflow_name": "deploy-with-monitoring",
    "status": "completed",
    "limit": 10
  }
}
```

**Use Cases:**
- Monitor workflow execution history
- Debug failed executions
- Track deployment activities
- Generate workflow usage reports

### `core_workflow_execution_get`
Get detailed information about a specific workflow execution.

**Arguments:**
- `execution_id` (string, required) - ID of the execution to retrieve
- `include_steps` (boolean, optional, default: true) - Whether to include detailed step information
- `step_id` (string, optional) - Get details for a specific step only

**Returns:** Complete execution details including step results, timing, and status

**Example Request:**
```json
{
  "name": "core_workflow_execution_get",
  "arguments": {
    "execution_id": "exec_123456789",
    "include_steps": true
  }
}
```

**Use Cases:**
- Debug workflow execution issues
- Analyze step performance and timing
- Retrieve execution outputs and results
- Monitor long-running workflow progress

---

## Dynamic Workflow Execution Tools

**Important:** For each workflow definition you create, Muster automatically generates a corresponding execution tool named `workflow_<workflow-name>`. These tools accept the workflow's defined arguments and execute the workflow.

> **Note**: Workflow execution tools depend on your workflow definitions and are **not built into Muster**. Different Muster installations will have different workflow execution tools based on their configured workflows.

### How Workflow Execution Tools Work

1. **Workflow Definition**: You create workflows using `core_workflow_create` or by placing YAML files in `.muster/workflows/`
2. **Tool Generation**: Muster automatically creates a corresponding `workflow_<name>` tool
3. **Tool Discovery**: The workflow tool appears in `list_tools()` output
4. **Tool Execution**: Execute via `call_tool(name="workflow_<name>", arguments={...})`

### Example

If you create a workflow named `deploy-webapp`:

```yaml
# .muster/workflows/deploy-webapp.yaml
name: deploy-webapp
description: "Deploy web application to Kubernetes"
args:
  app_name:
    type: string
    required: true
  environment:
    type: string
    default: "staging"
steps:
  - id: create-service
    tool: core_service_create
    args:
      name: "{{.app_name}}-{{.environment}}"
      serviceClassName: "web-app"
```

This generates a `workflow_deploy-webapp` tool that you execute via:

```json
{
  "name": "call_tool",
  "arguments": {
    "name": "workflow_deploy-webapp",
    "arguments": {
      "app_name": "my-service",
      "environment": "production"
    }
  }
}
```

### Workflow Tool Naming Convention

| Workflow Name | Generated Tool Name |
|---------------|---------------------|
| `deploy-webapp` | `workflow_deploy-webapp` |
| `connect-monitoring` | `workflow_connect-monitoring` |
| `auth-kubernetes` | `workflow_auth-kubernetes` |

---

## External Tools

External MCP tools come from your configured MCP servers (Kubernetes, Prometheus, Teleport, etc.). These are accessed the same way as core tools - via `call_tool`.

### Naming Convention

External tools follow the pattern: `x_<mcpserver-name>_<tool-name>`

| MCP Server | Tool | Full Name |
|------------|------|-----------|
| `kubernetes` | `get_pods` | `x_kubernetes_get_pods` |
| `prometheus` | `query` | `x_prometheus_query` |
| `teleport` | `kube_login` | `x_teleport_kube_login` |

### Example: Kubernetes Tool

```json
{
  "name": "call_tool",
  "arguments": {
    "name": "x_kubernetes_get_pods",
    "arguments": {
      "namespace": "default",
      "labelSelector": "app=my-service"
    }
  }
}
```

### Discovering External Tools

Use meta-tools to discover what external tools are available:

```bash
# List all tools including external
list_tools()

# Filter to specific MCP server
filter_tools(pattern="x_kubernetes_*")

# Get details about a specific external tool
describe_tool(name="x_kubernetes_get_pods")
```

---

## Migration Note

> **For users familiar with previous Muster versions:** The tool access model has changed. Previously, tools like `core_service_list` could be called directly. Now, **all tool calls must go through the `call_tool` meta-tool**. The server exposes only meta-tools; actual tools are accessed via `call_tool(name="...", arguments={...})`.
>
> See the [CHANGELOG](../../CHANGELOG.md) for migration details and [ADR-010](../explanation/decisions/010-server-side-meta-tools.md) for the architectural rationale
