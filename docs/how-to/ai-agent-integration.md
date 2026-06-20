# AI Agent Integration Guide

Advanced integration patterns for using Muster with AI agents effectively.

> **Architecture Note:** Muster uses a meta-tools interface where the server exposes only 11 meta-tools (`list_tools`, `call_tool`, etc.). All actual tool execution goes through `call_tool(name="...", arguments={...})`. See [Architecture](../explanation/architecture.md) for details.

## Quick Navigation

- [Advanced IDE configuration](#advanced-ide-configuration)
- [Create AI-friendly workflows](#ai-friendly-workflows)
- [Prompt engineering for infrastructure](#prompt-engineering-for-infrastructure)
- [Documentation for AI agents](#documentation-for-ai-agents)

For troubleshooting, see the [AI Agent Troubleshooting Guide](ai-troubleshooting.md).

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
  # Clear names and descriptions help an agent pick the right arguments.
  # Supported arg keys are: type, required, default, description.
  args:
    app_name:
      type: string
      required: true
      description: "Name of the web application to deploy (e.g. user-service)"
    environment:
      type: string
      required: true
      default: "development"
      description: "Target environment: development, staging, or production"
    image_tag:
      type: string
      required: true
      description: "Container image tag to deploy in semver form (e.g. v1.2.3)"

  # Reference workflow inputs as {{ .input.<arg> }} (the engine renders with
  # missingkey=error, so a bare {{ .app_name }} would fail at runtime).
  steps:
    - id: validate_prerequisites
      description: "Ensure deployment prerequisites are met"
      tool: validate_deployment_readiness
      args:
        app_name: "{{ .input.app_name }}"
        environment: "{{ .input.environment }}"
      store: true

    - id: wait_for_deployment
      description: "Wait for deployment to become ready"
      tool: wait_for_service_ready
      args:
        service_name: "{{ .input.app_name }}-{{ .input.environment }}"
        timeout: "300s"

    - id: verify_health
      description: "Verify application health and readiness"
      tool: verify_application_health
      args:
        app_name: "{{ .input.app_name }}"
        environment: "{{ .input.environment }}"
        expected_status: 200

    - id: notify_completion
      description: "Notify team of successful deployment"
      tool: send_notification
      args:
        message: "{{ .input.app_name }} {{ .input.image_tag }} deployed to {{ .input.environment }}"
      allowFailure: true
```

A clear `description` on the workflow and each step is what an agent reads to
decide when and how to call it — that is the only "AI documentation" mechanism;
the `description` fields are surfaced in the generated tool schema. See
[Workflow Creation](workflow-creation.md) for the full set of step features
(`condition`, `forEach`, `parallel`, `onFailure`).

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

### Scripting and CI

Outside an interactive agent, drive muster from scripts and CI with `muster call`
(invoke any aggregated tool) and `muster list`/`muster check` (discovery and
availability). These connect to a running aggregator via `--endpoint` /
`MUSTER_ENDPOINT` and honor `--output json` for machine parsing:

```yaml
# .github/workflows/deploy.yml
name: Deploy
on:
  workflow_dispatch:
    inputs:
      environment:
        type: choice
        options: [development, staging, production]

jobs:
  deploy:
    runs-on: ubuntu-latest
    env:
      MUSTER_ENDPOINT: ${{ secrets.MUSTER_ENDPOINT }}
    steps:
      - uses: actions/checkout@v4

      # Fail early if the workflow's required tools aren't available
      - run: muster check workflow deploy-webapp

      # Run the workflow as a tool, passing arguments as JSON
      - run: |
          muster call workflow_deploy_webapp --output json --json '{
            "app_name": "user-service",
            "environment": "${{ inputs.environment }}",
            "image_tag": "${{ github.sha }}"
          }'
```

Authenticate non-interactively where needed with `muster auth login --endpoint <url>`
(it exits non-zero — `2` if auth is required, `3` if the flow fails).

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
