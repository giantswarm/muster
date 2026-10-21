# Optimizing Workflows

Practical techniques for making muster workflows faster and more reliable. This
guide covers only features the workflow engine actually implements; see
[Workflow Creation](workflow-creation.md) and the
[Workflow CRD reference](../reference/crds.md#workflow) for full details.

> Optimization is purely about how you structure the workflow — composing
> steps, conditions, and concurrency. There is no separate optimization engine,
> caching layer, or tuning CLI to configure.

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

`forEach` runs one item after another unless it sets `parallelism: N`, which
runs up to N at once and still reports every iteration in item order. For a
list of lists — the clusters of each management cluster — nest a second
`forEach` in the body instead of asking the caller to flatten it. See
[Loops with forEach](workflow-creation.md#loops-with-foreach) and
[Fleet-wide scans](workflow-creation.md#fleet-wide-scans).

## Keep results small

Every step result is held for later steps and lands in the execution record.
Give a list step a [`jq`](workflow-creation.md#reshaping-a-step-result-jq)
program that keeps only the fields later steps need, and
[`paginate`](workflow-creation.md#paging-through-a-list-paginate) so a long
list is read completely instead of being cut at one page. Return only what the
caller needs, with `output: true` or an `output` template.

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
