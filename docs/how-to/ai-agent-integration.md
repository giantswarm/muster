# AI Agent Integration Guide

Advanced integration patterns for using Muster with AI agents effectively.

> **Architecture Note:** Muster uses a meta-tools interface where the server exposes only 11 meta-tools (`list_tools`, `call_tool`, etc.). All actual tool execution goes through `call_tool(name="...", arguments={...})`. See [Architecture](../explanation/architecture.md) for details.

## Quick Navigation

### Setup and Configuration
- [Advanced IDE configuration](#advanced-ide-configuration)
- [Multi-environment agent setup](#multi-environment-setup)
- [Team configuration standards](#team-configuration)

### Optimization and Workflows
- [Optimize tool discovery for AI agents](#tool-discovery-optimization)
- [Create AI-friendly workflows](#ai-friendly-workflows)
- [Set up context-aware tooling](#context-aware-tooling)

### Advanced Integration
- [Custom prompt engineering](#prompt-engineering)
- [Workflow automation via agents](#workflow-automation)
- [Integration with development workflows](#development-integration)

### Troubleshooting
- [Debug agent-tool communication](#debugging-communication)
- [Resolve performance issues](#performance-issues)
- [Handle authentication problems](#authentication-issues)

## Advanced IDE Configuration

### Cursor Advanced Setup

Beyond basic configuration, optimize Cursor for infrastructure work:

```json
// .vscode/settings.json or Cursor settings
{
  "mcpServers": {
    "muster-infrastructure": {
      "command": "muster",
      "args": ["standalone"],
      "env": {
        "MUSTER_CONFIG_PATH": "/path/to/infrastructure-config"
      }
    },
    "muster-development": {
      "command": "muster", 
      "args": ["standalone"],
      "env": {
        "MUSTER_CONFIG_PATH": "/path/to/development-config"
      }
    }
  }
}
```

**Benefits:**
- Context-specific tool availability
- Reduced agent confusion from too many tools
- Environment-appropriate capabilities

### VSCode with Multiple Workspaces

Configure workspace-specific Muster integration:

```json
// workspace-infrastructure.code-workspace
{
  "folders": [
    {"path": "./infrastructure"},
    {"path": "./manifests"}
  ],
  "settings": {
    "mcpServers": {
      "muster": {
        "command": "muster",
        "args": ["standalone"]
      }
    }
  }
}
```

### Claude Desktop Configuration

Optimize Claude for infrastructure conversations:

```json
// Claude configuration
{
  "mcpServers": {
    "muster-ops": {
      "command": "muster",
      "args": ["standalone"]
    }
  }
}
```

## AI-Friendly Workflows

### Design Workflows for AI Agents

Create workflows that AI agents can understand and use effectively:

```yaml
# ai-friendly-deployment.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: ai-deploy-webapp
spec:
  description: "Deploy web application with AI agent assistance"
  # Clear, descriptive args that AI can understand
  args:
    app_name:
      type: string
      required: true
      description: "Name of the web application to deploy"
      examples: ["my-webapp", "user-service", "api-gateway"]
    environment:
      type: string
      required: true
      description: "Target environment"
      enum: ["development", "staging", "production"]
      default: "development"
    image_tag:
      type: string
      required: true
      description: "Container image tag to deploy"
      pattern: "^v[0-9]+\\.[0-9]+\\.[0-9]+$"
      examples: ["v1.2.3", "v2.0.1"]
  
  # Steps with clear descriptions for AI understanding
  steps:
    - id: validate_prerequisites
      description: "Ensure deployment prerequisites are met"
      tool: validate_deployment_readiness
      args:
        app_name: "{{.app_name}}"
        environment: "{{.environment}}"
      store: true
      
    - id: deploy_application
      description: "Deploy the application to Kubernetes"
      tool: core_service_create
      args:
        name: "{{.app_name}}-{{.environment}}"
        serviceClassName: "web-application"
        args:
          image: "{{.app_name}}:{{.image_tag}}"
          environment: "{{.environment}}"
          replicas: "{{ if eq .environment \"production\" }}3{{ else }}1{{ end }}"
      store: true
      
    - id: wait_for_deployment
      description: "Wait for deployment to become ready"
      tool: wait_for_service_ready
      args:
        service_name: "{{.results.deploy_application.name}}"
        timeout: "300s"
      
    - id: verify_health
      description: "Verify application health and readiness"
      tool: verify_application_health
      args:
        endpoint: "{{.results.deploy_application.endpoint}}"
        expected_status: 200
        
    - id: notify_completion
      description: "Notify team of successful deployment"
      tool: send_notification
      args:
        message: "✅ {{.app_name}} {{.image_tag}} deployed to {{.environment}}"
        channels: ["#deployments", "#{{.environment}}"]
      allowFailure: true
```

### Workflow Documentation for AI

Add AI-readable documentation:

```yaml
metadata:
  annotations:
    ai.muster.io/purpose: "Web application deployment with validation"
    ai.muster.io/use-cases: |
      - Deploy new application versions
      - Promote between environments  
      - Rollback to previous versions
    ai.muster.io/examples: |
      Basic deployment:
        call_tool(name="workflow_ai_deploy_webapp", arguments={"app_name": "my-app", "environment": "staging", "image_tag": "v1.2.3"})
      
      Production deployment:
        call_tool(name="workflow_ai_deploy_webapp", arguments={"app_name": "critical-service", "environment": "production", "image_tag": "v2.0.1"})
```

## Context-Aware Tooling

### Set Up Environment Context

Help AI agents understand your environment:

```bash
# Configure environment-specific contexts
muster configure context \
  --name "production" \
  --description "Production environment - requires approval for changes" \
  --tools "kubectl-prod,helm-prod,monitoring-prod" \
  --safety-level "high"

muster configure context \
  --name "development" \
  --description "Development environment - full access for experimentation" \
  --tools "kubectl-dev,helm-dev,testing-tools" \
  --safety-level "low"
```

### Smart Tool Suggestions

Configure intelligent tool recommendations:

```yaml
# tool-suggestions.yaml
suggestions:
  deployment_context:
    triggers:
      - keywords: ["deploy", "release", "update"]
      - file_patterns: ["*.yaml", "*.yml", "Dockerfile"]
    suggested_tools:
      - "workflow_deploy_webapp"
      - "core_service_create" 
      - "x_kubernetes_apply_manifest"
    
  debugging_context:
    triggers:
      - keywords: ["debug", "troubleshoot", "error", "failed"]
      - error_patterns: ["pod.*failed", "service.*unavailable"]
    suggested_tools:
      - "x_kubernetes_get_logs"
      - "x_kubernetes_describe_pod"
      - "workflow_debug_service"
```

## Prompt Engineering for Infrastructure

### Effective AI Prompts

Guide users on effective prompting with Muster:

#### ✅ Good Prompts

**Specific and actionable:**
"Deploy my-webapp version v1.2.3 to staging environment with 2 replicas"

**Context-aware:**
"Check the health of all services in the production namespace and show any that are failing"

**Tool-discovery oriented:**
"What tools are available for debugging Kubernetes pod issues?"

#### ❌ Avoid These Prompts

**Too vague:**
"Fix my app" (Agent doesn't know what app or what's wrong)

**Overly complex:**
"Deploy app A to env X with config Y while monitoring Z and alerting W if..." (Break into steps)

**Missing context:**
"Scale to 5 replicas" (Which service? Which environment?)

### Conversation Patterns

Teach effective conversation flows:

#### Investigation Pattern
1. "What Kubernetes clusters do I have access to?"
2. "Show me all pods in the default namespace of the production cluster"
3. "Describe the failing pod and show me its logs"
4. "What tools can help me troubleshoot this specific error?"

#### Deployment Pattern
1. "What workflows are available for web application deployment?"
2. "Deploy my-service version v2.1.0 to staging using the standard workflow"
3. "Monitor the deployment status and notify me when complete"
4. "If successful, what's the process to promote to production?"

## Development Integration

### Git Workflow Integration

Integrate Muster with development workflows:

```yaml
# .github/workflows/ai-assisted-deploy.yml
name: AI-Assisted Deployment
on:
  pull_request:
    types: [opened, synchronize]

jobs:
  ai-analysis:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: AI-Powered Change Analysis
        run: |
          # Use AI agent to analyze changes and suggest deployment strategy
          ai-agent analyze-changes \
            --context "pull-request" \
            --files "${{ github.event.pull_request.changed_files }}" \
            --suggest-deployment
      
      - name: Automated Deployment Suggestion
        run: |
          # AI agent suggests appropriate deployment workflow
          ai-agent suggest-workflow \
            --change-type "${{ steps.analysis.outputs.change_type }}" \
            --target-env "staging"
```

### IDE Integration Patterns

Optimize IDE integration for infrastructure work:

```json
// .vscode/tasks.json
{
  "version": "2.0.0", 
  "tasks": [
    {
      "label": "AI: Analyze Infrastructure",
      "type": "shell",
      "command": "ai-agent",
      "args": [
        "analyze", 
        "--context", "infrastructure",
        "--workspace", "${workspaceFolder}"
      ],
      "group": "build"
    },
    {
      "label": "AI: Suggest Deployment",
      "type": "shell", 
      "command": "ai-agent",
      "args": [
        "suggest-deployment",
        "--files", "${file}",
        "--environment", "${input:environment}"
      ],
      "group": "build"
    }
  ],
  "inputs": [
    {
      "id": "environment",
      "type": "pickString",
      "description": "Target environment",
      "options": ["development", "staging", "production"]
    }
  ]
}
```

### Documentation for AI Agents

Write documentation that AI agents can parse effectively:

#### AI-Readable Documentation Format

Use structured formats that AI can understand:

**Purpose**: Deploy web applications to Kubernetes  
**Usage**: `workflow_deploy_webapp(app_name, environment, image_tag)`  
**Arguments**:
- `app_name` (string, required): Application identifier
- `environment` (string, required): Target environment [development|staging|production]
- `image_tag` (string, required): Container image tag in semver format

**Examples**:
```
call_tool(name="workflow_deploy_webapp", arguments={
  "app_name": "user-service",
  "environment": "staging", 
  "image_tag": "v1.2.3"
})

call_tool(name="workflow_deploy_webapp", arguments={
  "app_name": "api-gateway",
  "environment": "production",
  "image_tag": "v2.0.1"
})
```

**Prerequisites**:
- Kubernetes cluster access
- Application image available in registry
- Target namespace exists

## Related Documentation

- [Getting Started: AI Agent Setup](../getting-started/ai-agent-setup.md)
- [How-To: Workflow Creation](workflow-creation.md)
- [Reference: CLI Commands](../reference/cli/)
- [Troubleshooting: Common Issues](troubleshooting.md)