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

A step may be gated by a `condition`. **Every** step's result is available to
later steps as `{{ .results.<step_id> }}` — referencing no longer requires any
flag. The `output: true` flag only controls whether a step's result is included
in the document returned to the caller (see
[Referencing vs. returning results](#referencing-vs-returning-results)). If a
step fails and is not marked `allowFailure`, the workflow stops and its
`onFailure` handlers run.

## Templating

Step arguments are Go templates (with [sprig](https://masterminds.github.io/sprig/)
functions) rendered at execution time. The template context has exactly these
top-level keys:

| Key | Description |
|-----|-------------|
| `.input.<arg>` | Workflow arguments |
| `.results.<step_id>` | Result of any earlier step (no flag required) |
| `.vars.<name>` | Loop variables inside `forEach` (`.vars.item`, `.vars.item_index`) |
| `.context.<step_id>` | Legacy alias for `.results`; prefer `.results` |

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

## Referencing vs. returning results

Two independent concerns used to be conflated into the single `store` flag; they
are now separate:

- **Referencing** — every step's result is always available to later steps and
  to the [output projection](#shaping-the-returned-result-output-projection) as
  `{{ .results.<step_id>.<field> }}`. No flag is needed. This makes the common
  "take one value out of step 1 and feed it into step 2" pattern cheap.
- **Returning** — `output: true` includes a step's result in the document
  returned to the caller (the LLM-facing payload). Use it only for the few steps
  whose results the caller actually needs, to keep responses small.

`store: true` is a **deprecated alias** for `output: true` and keeps working for
backwards compatibility; prefer `output`.

```yaml
steps:
  - id: get_cluster_info
    tool: get_kubernetes_cluster_info
    args:
      cluster: "{{ .input.target_cluster }}"
    # No flag needed — the result is referenceable below.

  - id: deploy_to_cluster
    tool: deploy_application
    args:
      cluster_endpoint: "{{ .results.get_cluster_info.endpoint }}"
      cluster_version: "{{ .results.get_cluster_info.version }}"
    output: true   # include this step's result in the returned document
```

## Shaping the returned result (output projection)

By default a workflow returns a fixed envelope
(`{execution_id, workflow, status, input, steps[], ...}`) where each `output`
step contributes its whole result. To return a small, shaped document instead,
declare a workflow-level `output` projection. It is rendered once after all
steps complete, against `.input` / `.results` / `.vars`, and replaces the
envelope:

```yaml
spec:
  steps:
    - id: pods
      tool: x_kubernetes_list
      args: { kind: Pod }
    - id: events
      tool: x_kubernetes_list
      args: { kind: Event }
  output:
    cluster: "{{ .input.management_cluster }}"
    notRunning: "{{ .results.pods.items }}"
    backoffCount: "{{ len .results.events.items }}"
```

Each leaf is a Go-template/sprig expression. JSON structure is preserved:
`notRunning` stays an array and `backoffCount` stays a number. Nested objects and
arrays in the projection are rendered recursively. When `output` is omitted, the
default envelope is returned unchanged.

## Conditions

A `condition` decides whether a step runs. Specify **exactly one** of
`template`, `tool`, or `fromStep`. A `tool` or `fromStep` condition must also
declare an `expect` or `expectNot` block — without one the engine falls back to
"expect the call to fail", which is rarely what you mean. Both rules are
enforced at `kubectl apply` time (CEL) and by `workflow_create`/`workflow_validate`.

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

`jsonPath` keys use the same expression language as step args. A key may be a
dotted/bracketed path navigated from the tool result (now including array
indexing, e.g. `items[0].name`), or a full Go-template expression where the
result is exposed as `.result`:

```yaml
    expect:
      success: true
      jsonPath:
        "items[0].state": "running"                       # bracketed path
        "{{ (index .result.items 0).name }}": "primary"   # template form
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
contain `forEach` or `parallel`. A sub-step's result is available within the same
iteration as `{{ .results.<sub_step_id> }}`, and each iteration is also
addressable after the loop by zero-based index as
`{{ .results.<sub_step_id>_<index> }}` (e.g. `{{ .results.deploy_0 }}`). The
plain `{{ .results.<sub_step_id> }}` key keeps the last iteration's result.

> The indexed key is a literal `<id>_<index>` string, so avoid a sub-step id
> that already ends in `_<number>` (e.g. `deploy_0`) inside a `forEach` to keep
> the per-iteration keys unambiguous.

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

Sub-step results are available to later steps after the group completes.

## Error handling

### Tolerate a failing step

`allowFailure: true` records the failure but continues the workflow. On a
`forEach` or `parallel` step it tolerates a failure of the **whole group** (it
cannot tolerate one iteration or branch while failing the rest — put
`allowFailure` on the individual sub-step for that). The step's error is recorded
as its result and is available to later `fromStep` conditions and the output
projection without any extra flag:

```yaml
- id: optional_migration
  tool: x_database_run_migration
  args:
    version: "{{ .input.version }}"
  allowFailure: true
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
- Reference any step's result freely with `{{ .results.<id> }}`; reserve
  `output: true` (or an `output` projection) for what the caller actually needs.
- Add `onFailure` rollback steps for workflows that mutate external state.
- Keep workflows focused; compose larger flows by calling one workflow's
  `action_<name>` tool from another.

## Related documentation

- [Workflow CRD reference](../reference/crds.md#workflow)
- [MCP tools reference](../reference/mcp-tools.md)
