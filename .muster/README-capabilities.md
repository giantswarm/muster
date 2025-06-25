# Capability Examples

This directory contains example capability definitions that demonstrate how to create user-defined capabilities in muster.

## What are Capabilities?

Capabilities provide a high-level, user-defined abstraction layer over MCP server tools. Instead of calling individual tools directly, you can define domain-specific operations that combine multiple tools into workflows.

## Using These Examples

### 1. Copy Examples to Your Config Directory

To use these examples, copy them to your user configuration directory:

```bash
# Create the capability definitions directory
mkdir -p ~/.config/muster/capability/definitions

# Copy the examples you want to use
cp .muster/capabilities/capability-example-teleport-auth.yaml ~/.config/muster/capabilities/auth.yaml
cp .muster/capabilities/capability-example-portforward.yaml ~/.config/muster/capabilities/portforward.yaml
```

### 2. Customize for Your Environment

Edit the copied files to match your environment:

```bash
# Edit the auth capability
vim ~/.config/muster/capability/definitions/auth.yaml
```

### 3. Restart muster

After adding or modifying capability definitions, restart muster to load them:

```bash
muster connect
```

## Available Examples

### `capability-example-teleport-auth.yaml`
- **Type**: `auth`
- **Purpose**: Authentication via Teleport
- **Operations**: `login`, `logout`, `status`, `list_clusters`
- **Tools Required**: `x_teleport_kube`, `x_teleport_status`, `x_teleport_logout`, `x_teleport_list_clusters`

### `capability-example-portforward.yaml`
- **Type**: `portforward` 
- **Purpose**: Port forwarding management
- **Operations**: `create`, `delete`, `list`, `status`
- **Tools Required**: `x_portforward_create`, `x_portforward_delete`, `x_portforward_list`, `x_portforward_status`

### `capability-example-database.yaml`
- **Type**: `database`
- **Purpose**: Database management operations
- **Operations**: `backup`, `restore`, `query`, `migrate`
- **Tools Required**: `x_database_backup`, `x_database_restore`, `x_database_query`, `x_database_migrate`

### `capability-example-monitoring.yaml`
- **Type**: `monitoring`
- **Purpose**: Monitoring and alerting operations
- **Operations**: `query_metrics`, `create_alert`, `list_dashboards`
- **Tools Required**: `x_prometheus_query`, `x_alertmanager_create`, `x_grafana_dashboards`

## Creating Your Own Capabilities

You can create entirely custom capability types for your specific use cases:

### Example: CI/CD Pipeline Capability

```yaml
name: my_cicd_pipeline
type: cicd  # Your custom type
version: "1.0.0"
description: "CI/CD pipeline operations for my project"

operations:
  deploy:
    description: "Deploy application to environment"
    parameters:
      environment:
        type: string
        required: true
        description: "Target environment (dev, staging, prod)"
      version:
        type: string
        required: true
        description: "Application version to deploy"
    requires:
      - x_kubectl_apply
      - x_argocd_sync
    workflow:
      name: deploy_app
      description: "Deploy application using GitOps"
      steps:
        - id: update_manifests
          tool: x_kubectl_apply
          args:
            namespace: "{{ .environment }}"
            image_tag: "{{ .version }}"
          store: apply_result
        - id: sync_argocd
          tool: x_argocd_sync
          args:
            app_name: "my-app-{{ .environment }}"
          store: sync_result
```

Save this as `~/.config/muster/capability/definitions/cicd.yaml`

## Using Capabilities

Once your capabilities are loaded, they expose operations as API tools:

- `api_auth_login` - Login using the auth capability
- `api_portforward_create` - Create port forward using portforward capability  
- `api_database_backup` - Backup database using database capability
- `api_cicd_deploy` - Deploy using your custom CI/CD capability

## Management Tools

The capability system also provides management tools:

- `capability_list` - List all your defined capabilities
- `capability_info --type=auth` - Get details about the auth capability
- `capability_check --type=auth --operation=login` - Check if auth login is available
- `capability_create` - Create new capability definitions
- `capability_update` - Update existing capability definitions  
- `capability_delete` - Remove capability definitions
- `capability_validate` - Validate capability YAML syntax

## Best Practices

1. **Use descriptive capability types** - Choose names that clearly indicate the domain (e.g., "database", "monitoring", "deployment")

2. **Group related operations** - Put operations that work together in the same capability

3. **Validate your YAML** - Use `capability_validate` to check syntax before using

4. **Document your operations** - Add clear descriptions and parameter documentation

5. **Version your capabilities** - Use semantic versioning to track changes

6. **Test with mock tools** - Verify your workflows work with the required tools available

## Need Help?

- Check the muster documentation for more details on capability workflows
- Look at the existing examples for patterns and best practices
- Use `capability_validate` to debug YAML syntax issues 