# Optimizing Workflows

Practical techniques for making muster workflows faster and more reliable. This
guide covers only features the workflow engine actually implements; see
[Workflow Creation](workflow-creation.md) and the
[Workflow CRD reference](../reference/crds.md#workflow) for full details.

> There is no separate "AI optimization" engine, ML caching layer, or
> `muster configure ...` CLI. Optimization is about how you structure the
> workflow itself.

## Cut latency with `parallel`

Independent steps should run concurrently instead of one after another. A
`parallel` group runs its sub-steps at the same time; the workflow continues
once they all finish.

```yaml
steps:
  # Sequential: total time = sum of each deploy
  # Parallel: total time = the slowest deploy
  - id: deploy_all
    parallel:
      - id: deploy_frontend
        tool: deploy_service
        args: { service: "frontend" }
      - id: deploy_backend
        tool: deploy_service
        args: { service: "backend" }
      - id: deploy_database
        tool: deploy_service
        args: { service: "database" }
```

Siblings in a `parallel` group are independent: each resolves its arguments from
the workflow state as it was before the group started, so a sub-step cannot read
another sibling's result. Put dependent work in a later step.

## Skip unnecessary work with conditions

Don't run steps an environment doesn't need. A `condition.template` is the
cheapest gate — it is evaluated in-process with no tool call:

```yaml
- id: production_smoke_test
  tool: run_smoke_tests
  condition:
    template: "{{ eq .input.environment \"production\" }}"
```

Use `fromStep` to branch on an earlier step's result instead of re-running a
check:

```yaml
- id: skip_if_already_current
  tool: deploy
  condition:
    fromStep: version_check
    expectNot:
      jsonPath:
        up_to_date: true
```

## Fan out with `forEach`

When the same operation applies to many items (clusters, namespaces, services),
a `forEach` loop is clearer and less error-prone than copy-pasted steps:

```yaml
- id: roll_out
  forEach:
    items: "{{ .input.clusters }}"
    as: cluster
    steps:
      - id: apply
        tool: apply_manifest
        args:
          cluster: "{{ .vars.cluster.name }}"
```

`forEach` executes sequentially. For independent items where ordering doesn't
matter and latency does, prefer an explicit `parallel` group.

## Store only what you reuse

A step's result is only kept when `store: true`. Storing everything bloats the
execution record and the template context. Store a result only when a later step
references it via `{{ .results.<step_id> }}`.

## Make failures safe

- Mark genuinely optional steps `allowFailure: true` so a non-critical failure
  doesn't abort the whole workflow.
- Add `onFailure` rollback steps for workflows that mutate external state, so a
  failure leaves the system in a known state:

```yaml
spec:
  steps:
    - id: migrate
      tool: run_migration
  onFailure:
    - id: restore
      tool: restore_backup
```

## Keep workflows composable

Smaller, focused workflows are easier to reason about and reuse. A workflow is
exposed as an `action_<name>` tool, so one workflow can call another as a step.
This keeps each workflow short and lets you test pieces independently.

## Inspect execution to find bottlenecks

Use the execution-tracking tools to see where time goes and which steps were
skipped or failed:

- `workflow_execution_list` — recent executions and their status.
- `workflow_execution_get` — per-step detail (`completed`, `skipped`, `failed`)
  for a given execution ID.

Use this to confirm that conditions are skipping the steps you expect and that
parallel groups are actually overlapping.

## Related documentation

- [Workflow Creation](workflow-creation.md)
- [Workflow CRD reference](../reference/crds.md#workflow)
