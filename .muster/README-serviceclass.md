# ServiceClass Examples

This directory contains example ServiceClass definitions that demonstrate how to create dynamic service lifecycle management in muster.

## What are ServiceClasses?

ServiceClasses provide YAML-based blueprints for creating and managing dynamic service instances. Unlike capabilities which define operations, ServiceClasses define how to create, manage, and destroy service instances with automatic lifecycle management, health checking, and dependency handling.

## Using These Examples

### 1. Copy Examples to Your Config Directory

To use these examples, copy them to your user configuration directory:

```bash
# Create the serviceclass definitions directory
mkdir -p ~/.config/muster/serviceclasses

# Copy the examples you want to use
cp .muster/serviceclasses/serviceclass-example-portforward.yaml ~/.config/muster/serviceclasses/portforward.yaml
cp .muster/serviceclasses/serviceclass-example-k8s-connection.yaml ~/.config/muster/serviceclasses/k8s-connection.yaml
```

### 2. Customize for Your Environment

Edit the copied files to match your environment:

```bash
# Edit the portforward serviceclass
vim ~/.config/muster/serviceclasses/portforward.yaml
```

### 3. Restart muster

After adding or modifying ServiceClass definitions, restart muster to load them:

```bash
muster connect
```

## Available Examples

### `serviceclass-example-portforward.yaml`
- **Type**: `portforward`
- **Purpose**: Basic port forwarding capability
- **Operations**: `create`, `stop`
- **Tools Required**: `api_kubernetes_port_forward`, `api_kubernetes_stop_port_forward`
- **Features**: Simple capability-style operations

### `serviceclass-example-service-portforward.yaml`
- **Type**: `service_portforward`
- **Purpose**: Dynamic port forward service instances with lifecycle management
- **Service Features**: Auto health checking, service instance tracking, lifecycle tools
- **Tools Required**: `api_kubernetes_port_forward`, `api_kubernetes_stop_port_forward`, `api_service_orchestrator_create_service`
- **Operations**: `create_service`, `list_services`

### `serviceclass-example-k8s-connection.yaml`
- **Type**: `service_k8s_connection`
- **Purpose**: Dynamic Kubernetes cluster connection management
- **Service Features**: Connection health monitoring, authentication provider support
- **Tools Required**: `api_kubernetes_connect`, `api_kubernetes_disconnect`, `api_service_orchestrator_create_service`
- **Operations**: `create_connection`, `switch_context`, `list_connections`

### `serviceclass-example-database.yaml`
- **Type**: `database`
- **Purpose**: Database management operations
- **Operations**: `backup`, `restore`, `migrate`
- **Tools Required**: `api_database_backup`, `api_database_restore`, `api_database_migrate`

### `serviceclass-example-monitoring.yaml`
- **Type**: `monitoring`
- **Purpose**: Monitoring and observability operations
- **Operations**: `query_metrics`, `create_alert_rule`, `create_dashboard`
- **Tools Required**: `api_prometheus_query`, `api_alertmanager_create_rule`, `api_grafana_create_dashboard`

## ServiceClass Types

ServiceClasses come in two main types:

### 1. Capability-Style ServiceClasses

Simple operation definitions similar to capabilities:

```yaml
name: portforward
type: portforward
operations:
  create:
    description: "Create a port forward"
    workflow:
      steps:
        - tool: api_kubernetes_port_forward
```

### 2. Service-Style ServiceClasses

Advanced service lifecycle management with dynamic instances:

```yaml
name: service_portforward
type: service_portforward
serviceConfig:
  serviceType: "DynamicPortForward"
  lifecycleTools:
    create:
      tool: "api_kubernetes_port_forward"
      responseMapping:
        serviceId: "$.id"
    delete:
      tool: "api_kubernetes_stop_port_forward"
  healthCheck:
    enabled: true
    interval: "30s"
```

## Creating Your Own ServiceClasses

You can create custom ServiceClass types for your specific use cases:

### Example: Application Deployment ServiceClass

```yaml
name: app_deployment
type: app_deployment
version: "1.0.0"
description: "Application deployment with lifecycle management"

serviceConfig:
  serviceType: "ApplicationDeployment"
  defaultLabel: "app-{{ .app_name }}-{{ .environment }}"
  
  lifecycleTools:
    create:
      tool: "api_kubectl_apply"
      arguments:
        namespace: "{{ .environment }}"
        app_name: "{{ .app_name }}"
        image_tag: "{{ .version }}"
      responseMapping:
        serviceId: "$.deployment.metadata.name"
        status: "$.deployment.status.readyReplicas"
    
    delete:
      tool: "api_kubectl_delete"
      arguments:
        namespace: "{{ .environment }}"
        name: "{{ .service_id }}"
      responseMapping:
        status: "$.status"
    
    healthCheck:
      tool: "api_kubectl_get"
      arguments:
        namespace: "{{ .environment }}"
        name: "{{ .service_id }}"
      responseMapping:
        health: "$.status.readyReplicas"
  
  healthCheck:
    enabled: true
    interval: "60s"
    failureThreshold: 3
  
  createParameters:
    app_name:
      toolParameter: "app_name"
      required: true
    environment:
      toolParameter: "environment"
      required: true
    version:
      toolParameter: "image_tag"
      required: true

operations:
  deploy:
    description: "Deploy application as a managed service"
    requires:
      - api_service_orchestrator_create_service
    workflow:
      steps:
        - tool: api_service_orchestrator_create_service
          args:
            capability_name: "app_deployment"
            parameters:
              app_name: "{{ .app_name }}"
              environment: "{{ .environment }}"
              version: "{{ .version }}"
```

Save this as `~/.config/muster/serviceclasses/app-deployment.yaml`

## Using ServiceClasses

ServiceClasses expose their operations as API tools:

- `api_portforward_create` - Create port forward
- `api_service_portforward_create_service` - Create managed port forward service
- `api_service_k8s_connection_create_connection` - Create managed K8s connection
- `api_app_deployment_deploy` - Deploy using your custom deployment ServiceClass

## Service Instance Management

For service-style ServiceClasses, the orchestrator provides instance management:

- `core_service_create` - Create service instance
- `core_service_list` - List service instances  
- `core_service_get` - Get instance details
- `core_service_delete` - Delete service instance

## Management Tools

The ServiceClass system provides management tools:

- `core_serviceclass_list` - List all ServiceClass definitions
- `core_serviceclass_get` - Get details about a specific ServiceClass
- `core_serviceclass_available` - Check ServiceClass availability
- `core_serviceclass_refresh` - Refresh ServiceClass definitions

## Key Features

### Lifecycle Management
- **Automatic Creation**: Services are created using defined lifecycle tools
- **Health Monitoring**: Continuous health checking with configurable intervals
- **Graceful Shutdown**: Proper cleanup when services are destroyed
- **Dependency Management**: Handle service dependencies automatically

### Service Instance Tracking
- **Unique Instances**: Each service gets a unique ID and label
- **State Management**: Track service state (running, stopped, failed, etc.)
- **Metadata Storage**: Store service metadata and runtime information
- **Label-based Access**: Easy service discovery via labels

### Response Mapping
- **Extract Service Data**: Pull relevant information from tool responses
- **Health Status**: Map tool responses to health indicators
- **Error Handling**: Capture and report service errors
- **Metadata Collection**: Store additional service information

## Best Practices

1. **Choose the Right Type**: Use capability-style for simple operations, service-style for managed instances

2. **Design Clear Labels**: Use descriptive label templates for service identification

3. **Configure Health Checks**: Set appropriate intervals and thresholds for your services

4. **Handle Failures**: Configure failure thresholds and recovery strategies

5. **Map Responses Correctly**: Ensure response mapping extracts the right data for service tracking

6. **Version ServiceClasses**: Use semantic versioning to track changes

7. **Test Lifecycle**: Verify create, health check, and delete operations work correctly

## Need Help?

- Check the muster documentation for more details on ServiceClass lifecycles
- Look at the existing examples for patterns and best practices
- Use the management tools to debug ServiceClass issues
- Test with required tools available before deploying 