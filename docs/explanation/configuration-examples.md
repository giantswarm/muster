# Configuration Examples from Real Muster Instance

This document showcases real configuration examples from a working muster instance, demonstrating practical implementation patterns for MCP servers, ServiceClasses, and workflows.

## System Configuration

### **Core Configuration** (`.muster/config.yaml`)
```yaml
aggregator:
    port: 8090
    host: localhost
    transport: streamable-http
    enabled: true
```

**Key Points:**
- Aggregator runs on localhost:8090
- Uses HTTP streaming transport for MCP protocol
- Single configuration file manages core system settings

## MCP Server Configurations

This instance demonstrates 8 MCP servers providing specialized platform tools:

### **Kubernetes Management** (`.muster/mcpservers/kubernetes.yaml`)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: kubernetes
  namespace: default
spec:
  type: stdio
  autoStart: true
  command: ["mcp-kubernetes"]
  description: "Kubernetes cluster management MCP server"
```

### **Authentication Provider** (`.muster/mcpservers/teleport.yaml`) 
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: teleport
  namespace: default
spec:
  type: stdio
  autoStart: true
  command: ["mcp-teleport"]
  description: "Teleport authentication and access management"
```

### **Monitoring Stack** (`.muster/mcpservers/prometheus.yaml`)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: prometheus
  namespace: default
spec:
  type: stdio
  autoStart: true
  command: ["mcp-prometheus", "--config", "/etc/prometheus/config.yaml"]
  env:
    PROMETHEUS_URL: "http://localhost:9090"
  description: "Prometheus metrics collection and querying"
```

**Pattern Analysis:**
- **Consistent Structure**: All use `stdio` type with auto-start
- **Simple Commands**: Minimal arguments, delegating configuration to the MCP servers
- **Environment Variables**: Used for runtime configuration (URLs, tokens)
- **Descriptive Metadata**: Clear descriptions for tool discovery

## ServiceClass Templates

ServiceClasses define reusable service patterns. This instance provides 4 production-ready templates:

### **Kubernetes Connection Service** (`.muster/serviceclasses/k8s-connection.yaml`)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: service-k8s-connection
  namespace: default
spec:
  description: "Dynamic service capability for managing Kubernetes cluster connections with authentication"
  args:
    cluster_name:
      type: "string"
      required: true
      description: "Name of the Kubernetes cluster"
    role:
      type: "string"
      required: true
      description: "Role for the connection (management, workload, etc.)"
    region:
      type: "string"
      required: false
      description: "AWS region or cloud region"
    context:
      type: "string"
      required: false
      description: "Kubernetes context name"
    auth_provider:
      type: "string"
      required: false
      description: "Authentication provider (teleport, aws, gcp, etc.)"
      default: "teleport"
  serviceConfig:
    lifecycleTools:
      start:
        tool: "api_kubernetes_connect"
        args:
          clusterName: "{{ .cluster_name }}"
          role: "{{ .role }}"
          authProvider: "{{ .auth_provider | default \"teleport\" }}"
          region: "{{ .region }}"
          context: "{{ .context }}"
        outputs:
          serviceId: "connectionId"
          status: "status"
          auth_provider: "authProvider"
      stop:
        tool: "api_kubernetes_disconnect"
        args:
          connectionId: "{{ .service_id }}"
        outputs:
          status: "status"
      healthCheck:
        tool: "api_kubernetes_connection_status"
        args:
          connectionId: "{{ .service_id }}"
        expect:
          success: true
          jsonPath:
            health: true
      status:
        tool: "api_kubernetes_connection_info"
        args:
          connectionId: "{{ .service_id }}"
    healthCheck:
      enabled: true
      interval: "60s"
      failureThreshold: 3
      successThreshold: 2
    timeout:
      create: "120s"
      delete: "60s"
      healthCheck: "30s"
    outputs:
      connectionId: "{{ .start.serviceId }}"
      status: "{{ .start.status }}"
      authProvider: "{{ .start.auth_provider }}"
```

**Key Patterns:**
- **Rich Argument Schema**: Type validation, required/optional fields, defaults
- **Template Variables**: `{{ .cluster_name }}` syntax for dynamic values
- **Lifecycle Management**: Start, stop, status, healthCheck tools
- **Output Mapping**: Capture service outputs for subsequent use
- **Health Monitoring**: Configurable health checks with thresholds
- **Timeout Configuration**: Reasonable timeouts for all operations

### **Port Forwarding Service** (`.muster/serviceclasses/mimir-port-forward.yaml`)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: mimir-port-forward
  namespace: default
spec:
  description: "Port forwarding service for Mimir monitoring access"
  args:
    cluster:
      type: "string"
      required: true
      description: "Monitoring cluster name"
    localPort:
      type: "string"
      default: "18000"
      description: "Local port for forwarding"
    namespace:
      type: "string"
      default: "mimir"
      description: "Kubernetes namespace"
  serviceConfig:
    lifecycleTools:
      start:
        tool: "x_kubernetes_port_forward"
        args:
          context: "{{ .cluster }}"
          namespace: "{{ .namespace }}"
          service: "mimir-gateway"
          localPort: "{{ .localPort }}"
          remotePort: "8080"
        outputs:
          sessionId: "sessionId"
          localEndpoint: "http://localhost:{{ .localPort }}"
      stop:
        tool: "x_kubernetes_stop_port_forward"
        args:
          sessionId: "{{ .service_session_id }}"
    healthCheck:
      enabled: true
      interval: "30s"
      failureThreshold: 2
```

**Usage Example:**
```bash
# Create port-forwarding service instance
core_service_create {
  "serviceClassName": "mimir-port-forward",
  "name": "mimir-my-monitoring-cluster",
  "args": {
    "managementCluster": "my-monitoring-cluster",
    "localPort": "18001"
  }
}

# Service automatically:
# 1. Sets up port forwarding to mimir-gateway:8080
# 2. Maps to localhost:18001
# 3. Monitors connection health
# 4. Provides clean stop mechanism
```

## Workflow Orchestrations

This instance provides 8 workflows covering common platform operations:

### **Monitoring Connection Workflow** (`.muster/workflows/connect-monitoring.yaml`)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: connect-monitoring
spec:
  description: "Connect to monitoring in a Giant Swarm installation"
  args:
    cluster:
      type: "string"
      description: "Cluster domain (e.g., 'my-k8s.my-domain.com')"
      required: true
    localPort:
      type: "string"
      default: "18000"
      description: "Local port for forwarding"
  steps:
    - id: "login-cluster"
      tool: "x_teleport_kube_login"
      args:
        kubeCluster: "{{.input.cluster}}"
    - id: "setup-prometheus-access"
      tool: "core_service_create"
      args:
        serviceClassName: "prometheus-port-forward"
        name: "prometheus-port-forward-{{.input.cluster}}"
        args:
          cluster: "{{.input.cluster}}"
          localPort: "{{.input.localPort}}"
```

### **Health Check Workflow** (`.muster/workflows/check-cilium-health.yaml`)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: check-cilium-health
  namespace: default
spec:
  name: "check-cilium-health"
  description: "Comprehensive Cilium network health check"
  args:
    installation:
      type: "string"
      description: "Installation name"
      required: true
    workloadCluster:
      type: "string"
      description: "Workload cluster name"
      required: true
    namespace:
      type: "string"
      default: "kube-system"
      description: "Cilium namespace"
  steps:
    - id: "login-cluster"
      description: "Login to workload cluster"
      tool: "x_teleport_kube_login"
      args:
        kubeCluster: "{{.input.installation}}-{{.input.workloadCluster}}"
    - id: "check-cilium-pods"
      description: "Check Cilium pod status"
      tool: "x_kubernetes_get_pods"
      args:
        namespace: "{{.input.namespace}}"
        labelSelector: "k8s-app=cilium"
    - id: "run-connectivity-test"
      description: "Run Cilium connectivity test"
      tool: "x_kubernetes_exec"
      args:
        namespace: "{{.input.namespace}}"
        pod: "cilium-test"
        command: ["cilium", "connectivity", "test"]
      condition:
        tool: "x_kubernetes_get_pods"
        args:
          namespace: "{{.input.namespace}}"
          name: "cilium-test"
        expect:
          success: true
      allow_failure: true
    - id: "check-network-policies"
      description: "Check network policy status"
      tool: "x_kubernetes_get"
      args:
        resource: "networkpolicies"
        namespace: "{{.input.namespace}}"
```

**Key Workflow Patterns:**
- **Step Dependencies**: Login before accessing cluster resources
- **Template Composition**: `{{.input.installation}}-{{.input.workloadCluster}}`
- **Conditional Execution**: Run tests only if test pods exist
- **Error Handling**: `allow_failure: true` for optional steps
- **Output Chaining**: Use outputs from previous steps
- **Comprehensive Coverage**: Multiple validation steps for thorough checking

## Common Usage Patterns

### **Service Creation Pattern**
```bash
# 1. Check available service templates
core_serviceclass_list

# 2. Create service instance
core_service_create {
  "serviceClassName": "service-k8s-connection",
  "name": "prod-cluster",
  "args": {
    "cluster_name": "production",
    "role": "admin"
  }
}

# 3. Monitor service status
core_service_status("prod-cluster")
```

### **Workflow Execution Pattern**
```bash
# 1. Discover available workflows
core_workflow_list

# 2. Execute workflow
workflow_connect-monitoring {
  "cluster": "my-cluster.my-domain.com",
  "localPort": "18001"
}

# 3. Track execution
core_workflow_execution_list(workflow_name="connect-monitoring")
```

### **Debugging Pattern**
```bash
# 1. Check system health
core_service_list

# 2. Check MCP server status
core_mcpserver_list

# 3. Run diagnostic workflow
workflow_check-cilium-health {
  "installation": "foobar", 
  "workloadCluster": "prod"
}
```

## Configuration Best Practices

### **Naming Conventions**
- **MCP Servers**: Simple names (kubernetes, teleport, prometheus)
- **ServiceClasses**: Prefix with purpose (service-k8s-connection, mimir-port-forward)
- **Workflows**: Action-oriented names (connect-monitoring, check-cilium-health)

### **Argument Design**
- **Required Arguments**: Essential parameters only
- **Sensible Defaults**: Common values as defaults
- **Clear Descriptions**: Help AI agents understand purpose
- **Type Validation**: Proper type definitions (string, integer, boolean)

### **Error Handling**
- **Health Checks**: Regular interval monitoring
- **Timeouts**: Reasonable limits for all operations
- **Graceful Degradation**: `allow_failure` for optional steps
- **Resource Cleanup**: Proper stop mechanisms

### **Documentation**
- **Rich Descriptions**: Clear purpose statements
- **Example Values**: Concrete examples in descriptions  
- **Tool Dependencies**: Clear tool requirements
- **Output Schemas**: Document expected outputs

This configuration demonstrates how muster transforms complex platform operations into simple, discoverable, and reliable automation patterns suitable for AI agent execution. 
