# Workflow Creation

Build multi-step automations from the tools available in your muster session.
This guide describes only what the workflow engine actually implements. For the
full field reference, see [Workflow CRD](../reference/crds.md#workflow).

## Mental model

A `Workflow` is a list of `steps` executed top to bottom. Each step is exactly
one of:

- a **tool call** (`tool`),
- a **sequential loop** (`forEach`), or
- a **concurrent group** (`parallel`).

A step may be gated by a `condition`. A step that sets `store: true` makes its
result available to later steps as `{{ .results.<step_id> }}`. If a step fails
and is not marked `allowFailure`, the workflow stops and its `onFailure`
handlers run.

## Templating

Step arguments are Go templates (with [sprig](https://masterminds.github.io/sprig/)
functions) rendered at execution time. The template context has exactly these
top-level keys:

| Key | Description |
|-----|-------------|
| `.input.<arg>` | Workflow arguments |
| `.results.<step_id>` | Result of an earlier step that set `store: true` |
| `.vars.<name>` | Loop variables inside `forEach` (`.vars.item`, `.vars.item_index`) |
| `.context.<step_id>` | Alias for `.results` |

> Always use `{{ .input.<arg> }}` — there is no bare `{{ .<arg> }}`. Rendering
> uses `missingkey=error`, so a reference to an undefined key fails the step.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deploy-service
  namespace: default
spec:
  description: "Deploy a service to an environment"
  args:
    app_name:
      type: string
      required: true
    environment:
      type: string
      required: true
  steps:
    - id: deploy
      tool: x_deployment_deploy
      args:
        app: "{{ .input.app_name }}"
        env: "{{ .input.environment }}"
```

## Storing and reusing step results

Set `store: true` to keep a step's result. Subsequent steps reference fields of
the parsed JSON result by step ID:

```yaml
steps:
  - id: get_cluster_info
    tool: get_kubernetes_cluster_info
    args:
      cluster: "{{ .input.target_cluster }}"
    store: true

  - id: deploy_to_cluster
    tool: deploy_application
    args:
      cluster_endpoint: "{{ .results.get_cluster_info.endpoint }}"
      cluster_version: "{{ .results.get_cluster_info.version }}"
```

## Conditions

A `condition` decides whether a step runs. Specify **exactly one** of
`template`, `tool`, or `fromStep`.

### Template gate

A boolean Go template. The step runs only when it renders to `true`:

```yaml
- id: production_only
  tool: x_security_run_production_checks
  condition:
    template: "{{ eq .input.environment \"production\" }}"
```

```yaml
- id: scale_up
  tool: x_deployment_scale
  condition:
    template: "{{ gt .input.replicas 1 }}"
```

### Tool-based condition

Run a tool and check its outcome against `expect` / `expectNot`:

```yaml
- id: deploy
  tool: x_deployment_deploy
  condition:
    tool: x_health_check
    args:
      service: "{{ .input.app_name }}"
    expect:
      success: true
      jsonPath:
        status: "healthy"
```

### Referencing an earlier step

`fromStep` evaluates the stored result of a previous step:

```yaml
- id: rollback
  tool: x_deployment_rollback
  condition:
    fromStep: "verify"
    expectNot:
      success: true
```

> There are no `and` / `or` combinators. To express AND, chain conditional
> steps; for richer logic, use a single `condition.template`.

## Loops with `forEach`

Run a flat body of sub-steps once per item of a list. `items` must resolve to
an array; each element is bound to `{{ .vars.<as> }}` (default `item`), and the
zero-based index to `{{ .vars.<as>_index }}`.

```yaml
spec:
  args:
    clusters:
      type: array
      required: true
  steps:
    - id: deploy_to_each
      forEach:
        items: "{{ .input.clusters }}"
        as: cluster
        steps:
          - id: deploy
            tool: deploy_application
            args:
              name: "{{ .vars.cluster.name }}"
              version: "{{ .vars.cluster.version }}"
```

The body is non-recursive: sub-steps are plain tool calls and cannot themselves
contain `forEach` or `parallel`. A sub-step that sets `store: true` is available
within the same iteration as `{{ .results.<sub_step_id> }}`, and each iteration
is also addressable after the loop by zero-based index as
`{{ .results.<sub_step_id>_<index> }}` (e.g. `{{ .results.deploy_0 }}`). The
plain `{{ .results.<sub_step_id> }}` key keeps the last iteration's result.

## Concurrency with `parallel`

Run a group of sub-steps concurrently to cut total latency. Each sub-step
resolves its arguments from the workflow state as it was **before** the group
started — siblings cannot reference each other's results.

```yaml
steps:
  - id: deploy_all
    parallel:
      - id: deploy_frontend
        tool: deploy_service
        args:
          service: "frontend"
      - id: deploy_backend
        tool: deploy_service
        args:
          service: "backend"
      - id: deploy_database
        tool: deploy_service
        args:
          service: "database"

  - id: verify
    tool: verify_deployment
```

Sub-step results that set `store: true` are available to later steps after the
group completes.

## Error handling

### Tolerate a failing step

`allowFailure: true` records the failure but continues the workflow. The step's
error is available to later `fromStep` conditions when combined with
`store: true`:

```yaml
- id: optional_migration
  tool: x_database_run_migration
  args:
    version: "{{ .input.version }}"
  allowFailure: true
  store: true
```

### Rollback with `onFailure`

`onFailure` lists best-effort cleanup/rollback sub-steps that run when the
workflow fails on a step that does **not** allow failure. Their own failures are
tolerated.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deployment-with-rollback
  namespace: default
spec:
  args:
    app_name:
      type: string
      required: true
    environment:
      type: string
      required: true
  steps:
    - id: deploy
      tool: deploy_application
      args:
        app: "{{ .input.app_name }}"
        env: "{{ .input.environment }}"
  onFailure:
    - id: rollback
      tool: rollback_to_previous_version
      args:
        service: "{{ .input.app_name }}"
        environment: "{{ .input.environment }}"
    - id: notify
      tool: send_alert
      args:
        message: "Deployment of {{ .input.app_name }} failed and was rolled back"
```

## Managing and inspecting workflows

Workflows are namespaced CRDs and can be managed with `kubectl` or the muster
workflow tools.

```bash
# List / inspect
kubectl get workflows           # or: kubectl get wf
kubectl describe workflow deploy-service

# Apply from a file
kubectl apply -f workflow.yaml
```

Each workflow is exposed as an `action_<name>` tool once its referenced tools
are available in the session. Execution history is available through the
`workflow_execution_list` and `workflow_execution_get` tools, which include
per-step status (`completed`, `skipped`, `failed`).

## Best practices

- Use `condition.template` to skip work that an environment does not need.
- Use `parallel` for independent steps; keep dependent steps sequential.
- Use `forEach` for fan-out over a list (clusters, namespaces, services).
- Only `store` results you actually reference later.
- Add `onFailure` rollback steps for workflows that mutate external state.
- Keep workflows focused; compose larger flows by calling one workflow's
  `action_<name>` tool from another.

## Related documentation

- [Workflow CRD reference](../reference/crds.md#workflow)
- [MCP tools reference](../reference/mcp-tools.md)
