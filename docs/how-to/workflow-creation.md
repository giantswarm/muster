# Workflow Creation

Build multi-step automations from the tools available in your muster session.
This guide describes only what the workflow engine actually implements. For the
full field reference, see [Workflow CRD](../reference/crds.md#workflow).

## Mental model

A `Workflow` is a list of `steps` executed top to bottom. Each step is exactly
one of:

- a **tool call** (`tool`),
- a **loop** (`forEach`), one item after another or a bounded number at once,
  optionally with one loop nested in its body, or
- a **concurrent group** (`parallel`).

A tool step can also follow the tool's pages (`paginate`) and reshape its
result before it is stored (`jq`); together with nested loops and
`parallelism` they make a [fleet-wide scan](#fleet-wide-scans) one workflow
call.

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
> Read a key that may be absent with `get` (`{{ get .results.scan "error" }}`)
> or test it with `hasKey`; reach a step id with a hyphen through `index`
> (`{{ index .results "per-cluster" }}`).

### Matching strings

The sprig string helpers work everywhere a template does — step arguments,
`condition.template` and the `output` template. For a partial match use
`contains` (substring), `hasPrefix` / `hasSuffix`, or `regexMatch` (a Go
regular expression); `regexFind` returns the matching part:

```yaml
- id: nginx_only
  condition:
    template: '{{ contains "nginx" .input.image }}'
  tool: x_inventory_record
output:
  pinned: '{{ regexMatch ":[0-9][0-9.]*$" .input.image }}'   # true for "nginx:1.25"
  tag: '{{ regexFind "[^:]+$" .input.image }}'              # "1.25"
```

To filter the rows of a list, reshape the step's result with
[`jq`](#reshaping-a-step-result-jq) instead: a template cannot drop list
elements.

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
  to the [output template](#shaping-the-returned-result-output-template) as
  `{{ .results.<step_id>.<field> }}`. No flag is needed. This makes the common
  "take one value out of step 1 and feed it into step 2" pattern cheap.
- **Returning** — `output: true` includes a step's result in the returned
  document (what the caller, e.g. an LLM, receives). Use it only for the few
  steps whose results the caller actually needs, to keep responses small.

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

### What the caller receives

Without an [`output` template](#shaping-the-returned-result-output-template),
a workflow returns the default response
(`{execution_id, workflow, status, input, steps[], ...}`), and the exact shape
depends on the last step:

- each step marked `output: true` contributes its result under `steps[]`; and
- if the **last** step is a plain tool step that is *not* an `output` step, its
  result — what `paginate` and `jq` made of it — is additionally merged onto
  the top level of the response (a convenience so a trailing call's output is
  easy to read).

If you want a predictable, minimal response, declare an `output` template — it
replaces the response entirely (see below).

## Shaping the returned result (output template)

By default a workflow returns a fixed response
(`{execution_id, workflow, status, input, steps[], ...}`) where each `output`
step contributes its whole result. To return a small, shaped document instead,
declare a workflow-level `output` template. It is rendered once after all
steps complete, against `.input` / `.results` / `.vars`, and replaces the
response:

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
arrays in the output template are rendered recursively. When `output` is omitted, the
default response is returned unchanged.

When a workflow declares an `output` template, it **replaces** the response
entirely, so the per-step `output: true` / `store: true` flags no longer affect
the returned document (every step result is still referenceable in the
output template regardless of those flags). Authoring an output template while leaving
per-step `output` flags set is harmless but redundant; the create/validate path
and the CRD reconciler log a one-line warning naming the inert flags.

### Type preservation

A leaf's type comes from the **value it evaluates to**, never from how its
rendered text happens to look — so there is no lossy numeric coercion and no
workaround to remember:

- A **bare reference path** — `"{{ .results.pods.items }}"`, dots and array
  indices only — is resolved directly and keeps its exact JSON type (object,
  array, number, string, boolean) at any depth.
- A **single-action computed leaf** keeps the real type of its result: a numeric
  expression stays a number (so `"{{ len .results.events.items }}"` yields `3`,
  not `"3"`), and a computed string keeps its exact string form.
- A leaf that **mixes literal text with actions** (e.g. `"v{{ .v }}"`) renders to
  a string, since its concatenated form is inherently textual.

This means a computed value whose *string form* matters — a version (`"1.20"`),
a zero-padded value (`"08"`), or a long numeric ID — is preserved as-is:

```yaml
  output:
    version: '{{ printf "%d.%d" (int .results.r.major) (int .results.r.minor) }}'  # stays "1.20"
    padded: '{{ printf "%02d" (int .results.r.build) }}'                            # stays "08"
    backoffCount: "{{ len .results.events.items }}"                                 # number 3
```

Non-finite results (`NaN`, `Inf`) are kept as strings — they are produced by
text-rendering functions like `printf`, never by coercion.

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

Run a body of sub-steps once per item of a list. `items` must resolve to an
array — the workflow input or an earlier step's result, e.g.
`{{ .results.clusters.items }}` — and each element is bound to
`{{ .vars.<as> }}` (default `item`), the zero-based index to
`{{ .vars.<as>_index }}`.

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

A sub-step's result is available within the same iteration as
`{{ .results.<sub_step_id> }}`, and each iteration is also addressable after
the loop by zero-based index as `{{ .results.<sub_step_id>_<index> }}` (e.g.
`{{ .results.deploy_0 }}`). After the loop, the plain
`{{ .results.<sub_step_id> }}` key keeps the result of the last iteration that
ran the sub-step. A sub-step skipped by its `condition` has no result in that
iteration: neither the plain key for the later sub-steps of the iteration nor
the indexed key, so `{{ if hasKey .results "deploy_1" }}` tells a skipped
iteration from one that ran.

> The indexed key is a literal `<id>_<index>` string, so avoid a sub-step id
> that already ends in `_<number>` (e.g. `deploy_0`) inside a `forEach` to keep
> the per-iteration keys unambiguous.

### The loop's records

After the loop, the `forEach` step's own id holds one record per iteration
that ran, in item order:

```json
[
  {"index": 0, "item": "mc-a", "status": "completed", "results": {"deploy": {...}}},
  {"index": 1, "item": "mc-b", "status": "failed", "error": "deploy: cluster mc-b is unreachable", "results": {"deploy": {...}}}
]
```

`results` holds the iteration's sub-step results (a skipped one is absent);
`status` is `failed`, with `error`, when one of its sub-steps failed — also
when that sub-step has `allowFailure`. This is how a loop reports every
target, the failed ones included: `{{ len .results.deploy_to_each }}`, or a
[`jq`](#reshaping-a-step-result-jq) program on the `forEach` step that turns
the records into the rows the caller needs.

### Bounded parallelism

`parallelism: N` (1 to 50) runs up to N iterations at once; unset, they run one
after another. Sub-steps of one iteration still run in order and chain off
each other. Concurrent iterations start from the workflow state as it was
before the loop, so one iteration cannot see another's results, and
everything — the indexed keys, the plain key, the step records, the loop's
records — comes out in item order, exactly as a sequential run reports it.

```yaml
- id: scan
  forEach:
    items: "{{ .input.clusters }}"
    as: cluster
    parallelism: 8
    steps:
      - id: pods
        tool: x_kubernetes_list
        args: { cluster: "{{ .vars.cluster }}", resourceType: pods }
        allowFailure: true   # an unreachable cluster is reported, the others go on
```

A failure that is not allowed stops the loop as it does a sequential one: no
further iteration starts, the ones already running finish and are reported,
and the workflow fails (or goes on, if the `forEach` step has `allowFailure`).

### Nested loops

A sub-step of a `forEach` body may itself be a `forEach`, one level deep — not
inside `parallel` or `onFailure`, and the nested body holds tool calls only.
Both loop variables are in scope in the nested body, so the nested loop
typically iterates over what an earlier sub-step listed for the outer item:

```yaml
- id: fleet
  forEach:
    items: "{{ .input.management_clusters }}"
    as: mc
    steps:
      - id: clusters
        tool: x_kubernetes_list
        args: { management_cluster: "{{ .vars.mc }}", resourceType: clusters.cluster.x-k8s.io, allNamespaces: true }
      - id: per_cluster
        forEach:
          items: "{{ .results.clusters.items }}"
          as: cluster          # must differ from the outer loop's
          steps:
            - id: pods
              tool: x_kubernetes_list
              args:
                management_cluster: "{{ .vars.mc }}"
                cluster: "{{ .vars.cluster.metadata.name }}"
                resourceType: pods
```

A nested sub-step's result is kept per iteration as
`{{ .results.<id>_<outer>_<inner> }}` (`pods_1_0`: the first cluster of the
second management cluster), its step record carries `iteration` and
`outer_iteration`, and the nested `forEach` id holds the nested loop's
records inside each outer iteration's `results`. Each loop takes its own
`parallelism`: the calls in flight at once are at most the outer times the
inner one. A nested id must differ from the outer body's ids and the nested
`as` from the outer one.

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

## Paging through a list (`paginate`)

A list tool returns one page and a token for the next. `paginate` on a tool
step (or sub-step) passes that token back until the tool has none left or
`maxPages` pages were fetched, and stores every page's items as one list:

```yaml
- id: pods
  tool: x_kubernetes_list
  args: { resourceType: pods, allNamespaces: true }
  paginate:
    maxPages: 20          # 1 to 1000, default 10
```

| Field | Default | Meaning |
|-------|---------|---------|
| `itemsField` | `items` | Where a page's list is |
| `tokenField` | `continue` | Where a page's next-page token is |
| `tokenArg` | `continue` | The tool argument the token is passed in |
| `maxPages` | `10` | The most pages fetched |

The defaults match mcp-kubernetes; field names may be dotted paths
(`data.next`) for tools that nest them. The stored result is the last page
with its list holding every page's items, plus a `pagination` object:
`pagination.pages` is how many pages were fetched and `pagination.truncated`
says whether `maxPages` cut the listing short — then the result still carries
the token the next page would be fetched with, so stopping early is never
silent. An error on any page fails the step (`allowFailure` applies).

## Reshaping a step result (`jq`)

A `jq` program on a step reshapes its result before it is stored. Later
steps, the `output` template, the step record and the execution history see
only what it yields, so a large listing is reduced to the few fields the
workflow needs right after the call, instead of being held to the end:

```yaml
- id: pods
  tool: x_kubernetes_list
  args: { resourceType: pods, allNamespaces: true }
  paginate: { maxPages: 20 }
  jq: >-
    [.items[]
     | select(any(.spec.containers[]; .image | contains($input.image)))
     | {namespace: .metadata.namespace, pod: .metadata.name, images: [.spec.containers[].image]}]
```

- The program's input is the step's result: the tool's result (after
  `paginate`) for a tool step, the [loop's records](#the-loops-records) for a
  `forEach` step.
- `$input` is the workflow input and `$vars` the loop variables
  (`$vars.cluster`).
- It must yield **one** value. A program yielding a stream (`.items[]`)
  fails the step, saying how many values it yielded; wrap it in `[ ]` to keep
  them all. Yielding nothing stores `null`.
- A program that fails, or does not compile, fails the step; a program that
  does not compile is rejected when the workflow is created.

The engine is [gojq](https://github.com/itchyny/gojq), the jq implementation
`gh --jq` uses.

## Fleet-wide scans

The options above compose into an inventory over every workload cluster of
every management cluster in one call — no flattened `{mc, cluster}` list
from the caller, a bounded number of calls in flight, every page of every
list, only the matching rows kept, and an unreachable cluster reported without
stopping the others:

```yaml
spec:
  args:
    management_clusters: { type: array, required: true }
    image: { type: string, required: true }
  steps:
    - id: fleet
      forEach:
        items: "{{ .input.management_clusters }}"
        as: mc
        parallelism: 4
        steps:
          - id: clusters
            tool: x_kubernetes_list
            args: { management_cluster: "{{ .vars.mc }}", resourceType: clusters.cluster.x-k8s.io, allNamespaces: true }
            paginate: { maxPages: 10 }
            jq: '[.items[] | {name: .metadata.name, namespace: .metadata.namespace}]'
          - id: per_cluster
            forEach:
              items: "{{ .results.clusters }}"
              as: cluster
              parallelism: 8
              steps:
                - id: pods
                  tool: x_kubernetes_list
                  args:
                    management_cluster: "{{ .vars.mc }}"
                    cluster: "{{ .vars.cluster.name }}"
                    resourceType: pods
                    allNamespaces: true
                  paginate: { maxPages: 20 }
                  allowFailure: true
                  jq: >-
                    [.items[]
                     | select(any(.spec.containers[]; .image | contains($input.image)))
                     | {namespace: .metadata.namespace, pod: .metadata.name}]
      # One row per match and one per unreachable cluster.
      jq: >-
        {
          matches: [.[] | .item as $mc | .results.per_cluster[]
                    | select(.status == "completed") | .item.name as $c
                    | .results.pods[] | {management_cluster: $mc, cluster: $c} + .],
          failures: [.[] | .item as $mc | .results.per_cluster[]
                     | select(.status == "failed")
                     | {management_cluster: $mc, cluster: .item.name, error: .error}]
        }
  output:
    matches: "{{ .results.fleet.matches }}"
    failures: "{{ .results.fleet.failures }}"
```

The complete workflow, with the other workload kinds, is
[`examples/workflow-fleet-image-scan.yaml`](https://github.com/giantswarm/muster/blob/main/examples/workflow-fleet-image-scan.yaml).

## Error handling

### Tolerate a failing step

`allowFailure: true` records the failure but continues the workflow. On a
`forEach` or `parallel` step it tolerates a failure of the **whole group** (it
cannot tolerate one iteration or branch while failing the rest — put
`allowFailure` on the individual sub-step for that; a `forEach` then reports
the iteration as failed in [its records](#the-loops-records) and goes on with
the next one). The step's error is recorded
as its result and is available to later `fromStep` conditions and the output
output template without any extra flag:

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
- Use `forEach` for fan-out over a list (clusters, namespaces, services); set
  `parallelism` when the targets are independent, and nest a loop instead of
  asking the caller to flatten a list of pairs.
- Give list steps `paginate` so a listing beyond one page is never silently
  cut short, and a `jq` program that keeps only the fields later steps need.
- Reference any step's result freely with `{{ .results.<id> }}`; reserve
  `output: true` (or an `output` template) for what the caller actually needs.
- Add `onFailure` rollback steps for workflows that mutate external state.
- Keep workflows focused; compose larger flows by calling one workflow's
  `action_<name>` tool from another.

## Related documentation

- [Workflow CRD reference](../reference/crds.md#workflow)
- [MCP tools reference](../reference/mcp-tools.md)
