/*
Package capability provides a fully user-driven capability-based service discovery and management system.

# Overview

The capability system is completely user-driven - users define all capability types and operations
by creating YAML definition files. There are no built-in or hardcoded capabilities, giving users
complete flexibility to organize and discover functionality across different MCP servers.

# User-Driven Capability Definition

Users create capability definition files in their configuration directory:
`~/.config/muster/capability/definitions/`

Each capability is defined as a YAML file with:
- **Custom capability type** (any string, e.g., "auth", "database", "monitoring", "deployment")
- **Operations** that map to tool calls and workflows
- **Args** and validation rules
- **Embedded workflows** for complex operations

# Capability Examples

The system ships with example capability definitions in `.muster/`:
- `capability-example-teleport-auth.yaml` - Authentication via Teleport
- `capability-example-portforward.yaml` - Port forwarding management
- `capability-example-database.yaml` - Database operations
- `capability-example-monitoring.yaml` - Monitoring and metrics

Users copy these examples to their config directory and customize them:

```bash
# Copy example to user config
mkdir -p ~/.config/muster/capability/definitions
cp .muster/capability-example-teleport-auth.yaml ~/.config/muster/capability/definitions/my-auth.yaml
```

# Flexible Capability Types

Users can define any capability type they want - there are no constraints:

```yaml
# Example: Custom "cicd" capability type
name: my_pipeline
type: cicd  # User-defined type
version: "1.0.0"
description: "CI/CD pipeline operations"

operations:

	deploy:
	  description: "Deploy application"
	  args:
	    environment:
	      type: string
	      required: true
	    version:
	      type: string
	      required: true
	  requires:
	    - x_deploy_app
	  workflow:
	    name: deploy_application
	    steps:
	      - id: deploy
	        tool: x_deploy_app
	        args:
	          env: "{{ .environment }}"
	          ver: "{{ .version }}"

```

# Tool Mapping

Capabilities expose operations as API tools with the `api_` prefix:
- `api_<type>_<operation>` format
- Example: `api_cicd_deploy`, `api_auth_login`, `api_database_backup`

# Discovery and Management

The system provides management tools:
- `capability_list` - List all user-defined capabilities
- `capability_info` - Get details about a specific capability type
- `capability_check` - Check if an operation is available
- `capability_create` - Create new capability definitions
- `capability_update` - Update existing definitions
- `capability_delete` - Remove capability definitions
- `capability_validate` - Validate capability YAML

# Integration

Capabilities integrate with the workflow system for complex operations and can require
specific tools from MCP servers. This provides a clean abstraction layer between
high-level user operations and low-level tool implementations.

Example usage:
```go
// Check if auth capability is available
available := adapter.IsCapabilityAvailable("auth", "login")

// Execute capability operation

	result, err := adapter.ExecuteCapability(ctx, "auth", "login", map[string]interface{}{
	    "cluster": "production",
	    "user": "admin",
	})

```

The capability system is the primary interface for organizing and discovering functionality
in muster, allowing users to create domain-specific abstractions over MCP server tools.
*/
package capability
