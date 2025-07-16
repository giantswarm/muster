# Muster: Universal Control Plane for AI Agents

[![Go Report Card](https://goreportcard.com/badge/github.com/giantswarm/muster)](https://goreportcard.com/report/github.com/giantswarm/muster)
[![GoDoc](https://godoc.org/github.com/giantswarm/muster?status.svg)](https://godoc.org/github.com/giantswarm/muster)

**In German, _Muster_ means "pattern" or "sample."** This project gives AI agents the building blocks to discover patterns and collect samples from any digital environment, providing them with a universal protocol to interact with the world.

Muster is your go-to meta-server that manages all your MCP servers, making it easy for AI agents to discover and use the right tools without the hassle. It handles starting, stopping, and coordinating servers so you can focus on building awesome stuff. Built on the Model Context Protocol, it's designed to make platform engineering smoother and more efficient.

---

## Quick Start for Different Users

### 🤖 AI Agent Users (Cursor, VSCode, Claude)
- **Goal**: Hook up Muster to your IDE for smart tool access
- **Time**: About 5 minutes
- **Guide**: [AI Agent Setup](docs/getting-started/ai-agent-integration.md)

### 🏗️ Platform Engineers
- **Goal**: Get Muster running for your infrastructure tasks
- **Time**: 15 minutes or so
- **Guide**: [Platform Setup Guide](docs/getting-started/platform-engineering.md)

### 👩‍💻 Contributors
- **Goal**: Set up your dev environment to hack on Muster
- **Time**: 10 minutes
- **Guide**: [Development Setup](docs/contributing/development-setup.md)

## MCP Tools Reference

Muster exposes different sets of MCP tools depending on how you're interacting with it:

### 🔧 Agent MCP Tools (IDE Integration)
When you configure `muster agent --mcp-server` in your IDE (Cursor, VSCode, Claude), you get access to all tools through a unified interface:

- **Core Management Tools**: All `core_*` tools for managing services, workflows, and configuration
- **Workflow Execution Tools**: `workflow_<workflow-name>` tools for executing specific workflows  
- **Aggregated External Tools**: All tools from configured MCP servers with prefixed names

**Key Benefits**:
- Single MCP server configuration in your IDE
- Unified tool discovery across all managed servers
- Automatic tool availability based on server status

### ⚙️ Core API Tools (Aggregator Server)
The `muster serve` aggregator exposes these core platform management tools via MCP protocol:

**Service Instance Management**
- `core_service_create` - Create new service instances from ServiceClasses
- `core_service_delete` - Remove service instances
- `core_service_get` - Get detailed service instance information
- `core_service_list` - List all service instances
- `core_service_start` - Start a stopped service instance
- `core_service_stop` - Stop a running service instance  
- `core_service_restart` - Restart a service instance
- `core_service_status` - Get current status of a service instance
- `core_service_validate` - Validate service instance configuration before creation

**ServiceClass Management**  
- `core_serviceclass_create` - Define new service templates
- `core_serviceclass_delete` - Remove ServiceClass definitions
- `core_serviceclass_get` - Get ServiceClass details and schema
- `core_serviceclass_list` - List all available ServiceClasses
- `core_serviceclass_update` - Modify existing ServiceClass definitions
- `core_serviceclass_validate` - Validate ServiceClass configuration
- `core_serviceclass_available` - Check if ServiceClass dependencies are available

**Workflow Management**
- `core_workflow_create` - Define new workflow templates
- `core_workflow_delete` - Remove workflow definitions  
- `core_workflow_get` - Get workflow details and steps
- `core_workflow_list` - List all available workflows
- `core_workflow_update` - Modify existing workflow definitions
- `core_workflow_validate` - Validate workflow configuration
- `core_workflow_available` - Check if workflow dependencies are available
- `core_workflow_execution_get` - Get details of workflow execution
- `core_workflow_execution_list` - List workflow execution history

**MCP Server Management**
- `core_mcpserver_create` - Register new MCP servers
- `core_mcpserver_delete` - Remove MCP server registrations
- `core_mcpserver_get` - Get MCP server details and status
- `core_mcpserver_list` - List all registered MCP servers
- `core_mcpserver_update` - Modify MCP server configuration
- `core_mcpserver_validate` - Validate MCP server configuration

**Configuration Management**
- `core_config_get` - Get current system configuration
- `core_config_save` - Persist configuration changes
- `core_config_reload` - Reload configuration from disk
- `core_config_get_aggregator` - Get aggregator-specific configuration
- `core_config_update_aggregator` - Update aggregator configuration

### 🔗 Aggregated External Tools
Muster automatically aggregates tools from all configured MCP servers and exposes them with a consistent naming pattern:

**Naming Convention**: `<prefix>_<mcpserver-name>_<original-tool-name>`

**Common Prefixes**:
- `x_` - General external tools prefix for discoverability

**Examples by Tool Category**:
- **Filesystem Operations**: `x_filesystem_read_file`, `x_filesystem_write_file`, `x_filesystem_list_directory`
- **Version Control**: `x_github_create_issue`, `x_github_list_prs`, `x_github_create_branch`  
- **Container Platforms**: `x_kubernetes_get_pods`, `x_kubernetes_apply_yaml`, `x_kubernetes_get_logs`
- **Cloud Infrastructure**: `x_aws_list_instances`, `x_gcp_create_vm`, `x_azure_get_resources`
- **Databases**: `x_postgres_query`, `x_mongo_find`, `x_redis_get`

**Dynamic Tool Discovery**: The exact tools available depend on which MCP servers are currently registered and running. Use `core_mcpserver_list` to see active servers and their tool inventories.

**Workflow vs Action Tools**: 
- In your IDE: Workflows appear as `workflow_<workflow-name>` tools
- In the aggregator API: These same workflows are internally called `action_<workflow-name>`
- The agent automatically maps between these naming conventions

## Core Concepts (Quick Peek)
- **MCP Aggregation**: [How Muster brings everything together](docs/explanation/mcp-aggregation.md)
- **Tool Discovery**: [Finding the right tool for the job](docs/explanation/tool-discovery.md)
- **Workflows & Services**: [Automating your tasks](docs/explanation/orchestration.md)

## Documentation Navigation

### Learning (Tutorials)
- [Getting Started Guides](docs/getting-started/) - Step-by-step introductions
- [Interactive Tutorials](docs/getting-started/tutorials/) - Hands-on learning experiences

### Problem-Solving (How-To Guides)
- [Common Tasks](docs/how-to/) - Solution-focused procedures
- [Integration Patterns](docs/how-to/integrations/) - System integration approaches
- [Troubleshooting](docs/how-to/troubleshooting/) - Problem resolution guides

### Reference Information
- [CLI Commands](docs/reference/cli/) - Complete command reference
- [Configuration](docs/reference/configuration/) - Configuration schemas and options
- [API Reference](docs/reference/api/) - Comprehensive API documentation

### Understanding (Explanation)
- [Architecture](docs/explanation/architecture.md) - System design and principles
- [Design Decisions](docs/explanation/decisions/) - Architecture Decision Records
- [Core Concepts](docs/explanation/) - Conceptual foundations

### Operations and Deployment
- [Installation](docs/operations/installation.md) - Deployment procedures
- [Deployment Patterns](docs/operations/deployment/) - Production deployment strategies
- [Monitoring](docs/operations/monitoring.md) - Observability and alerting

### Contributing
- [Development Setup](docs/contributing/development-setup.md) - Environment configuration
- [Code Guidelines](docs/contributing/code-style.md) - Development standards
- [Testing Framework](docs/contributing/testing/) - Testing procedures and standards

## Community and Support

- **[Contributing Guide](CONTRIBUTING.md)**: How to contribute to Muster
- **[Issue Tracker](https://github.com/giantswarm/muster/issues)**: Bug reports and feature requests
- **[Discussions](https://github.com/giantswarm/muster/discussions)**: Community Q&A and use cases

---

*Muster is a [Giant Swarm](https://giantswarm.io) project, built to empower platform engineers and AI agents with intelligent infrastructure control.*
