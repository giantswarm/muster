# Observability — tracing, metrics, structured logs

muster emits OpenTelemetry traces and metrics for every MCP tool call,
plus one structured log line per call carrying the same fields. All
three signals correlate by tool name and span/trace ID so dashboards
can pivot between them.

## What muster contributes to a trace

For every MCP tool call that reaches a backend, muster emits three spans:

```
  span: mcp.tools/call     (Server, mcp-go: the inbound request)
    └── span: tool.<tool>  (Internal, mcp-go: call_tool, x_kubernetes_list_pods, …)
          └── span: mcp.tools/call  (Client, mcp-go: the request to the backend)
```

mcp-go opens all three spans. The attributes that name the call are:

| Attribute | Spans | Value |
|---|---|---|
| `mcp.tool.name` | server, internal | The tool the client invoked, for example `call_tool` |
| `muster.downstream.tool.name` | server, internal | The aggregator-exposed name of the tool that muster dispatched, for example `x_kubernetes_list_pods` |
| `mcpserver.name` | server, internal, client | The MCPServer that the call reached |
| `gen_ai.tool.name` | client | The tool name that the backend knows, for example `list_pods` |

`gen_ai.tool.name` is the key of the OpenTelemetry GenAI semantic conventions for MCP. The conventions have no key for an MCP server name, so muster uses `mcpserver.name`, the same key as its metrics. `mcpserver.name` is also on the client spans of the handshake that a call opens.

A workflow step opens a `workflow.step` span, and the step dispatch labels that span. The server span of a workflow call does not get the downstream attributes, because the steps reach more than one backend.

W3C TraceContext and Baggage propagators are always installed, also when muster has no OTLP endpoint. Thus inbound `traceparent` headers propagate to the backend calls.

## Configuration

All telemetry is off by default. Set the OTLP endpoint via Helm to
turn it on:

```yaml
muster:
  observability:
    otel:
      endpoint: tempo-distributor.tempo.svc:4317
      protocol: grpc                              # or http/protobuf
      headers: "X-Scope-OrgID=giantswarm"         # multi-tenant Tempo/Mimir
      resourceAttributes: "deployment.environment=glean"
```

Underlying env vars (rendered onto the muster container):

| Env var                              | Source                                                                 |
|--------------------------------------|------------------------------------------------------------------------|
| `OTEL_EXPORTER_OTLP_ENDPOINT`        | `muster.observability.otel.endpoint`                                   |
| `OTEL_EXPORTER_OTLP_PROTOCOL`        | `muster.observability.otel.protocol` (default `grpc`)                  |
| `OTEL_EXPORTER_OTLP_HEADERS`         | `muster.observability.otel.headers`                                    |
| `OTEL_RESOURCE_ATTRIBUTES`           | `k8s.namespace.name`/`k8s.pod.name`/`k8s.node.name` from downward API, plus `muster.observability.otel.resourceAttributes` appended |

Setting only `OTEL_EXPORTER_OTLP_ENDPOINT` enables both traces and
metrics. To override per signal use `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`
/ `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`.

### Prometheus pull mode

The metric signal supports a self-hosted `/metrics` endpoint as an
alternative (or addition) to OTLP push. Operators running Mimir or a
Prometheus scraper opt in via:

```yaml
muster:
  observability:
    metrics:
      prometheus:
        port: 9464
        serviceMonitor:
          enabled: true              # ServiceMonitor for Prometheus Operator clusters
          interval: 30s
          labels: {}
```

`serviceMonitor.enabled: true` is sufficient on its own: it implicitly
appends `prometheus` to `metrics.exporter`, so the single toggle serves
`/metrics` and renders the `ServiceMonitor`. Setting
`metrics.exporter: prometheus` explicitly (or `"otlp,prometheus"` for
dual-export) also works — e.g. to scrape the endpoint without a
Prometheus Operator.

When `prometheus` is in the effective exporter list, the muster
container exposes port 9464 with the OTel SDK's self-hosted `/metrics`
handler. The Service forwards the port; a `ServiceMonitor` is rendered
when `serviceMonitor.enabled` is true. Histogram exemplars are emitted in
the Prometheus exposition format (Prometheus 2.26+ / Mimir 2.6+
ingest natively).

## Metrics

Every muster instrument is emitted under the single OTel scope
`github.com/giantswarm/muster` (`observability.TracerName`), shared with
the tracer so spans and metrics correlate. There is no per-package
suffix; the Prometheus exporter surfaces it as the `otel_scope_name`
label.

The aggregator emits two instruments:

| OTel name                    | Type                  | Attributes        | Prometheus export name              |
|------------------------------|-----------------------|-------------------|-------------------------------------|
| `muster.tool_calls`          | `Int64Counter`        | `tool`, `outcome` | `muster_tool_calls_total`           |
| `muster.tool_call.duration`  | `Float64Histogram`/s  | `tool`, `outcome` | `muster_tool_call_duration_seconds` |

`outcome` is one of `ok`, `error` (handler returned a Go error), or
`error_result` (handler returned a `CallToolResult` with `IsError=true`).

Both instruments are recorded by the meta-tool layer middleware — they
attribute to the meta-tool name (`call_tool`, `list_tools`, …), not
the underlying workload tool.

### Downstream dispatch metrics

The dispatch layer (`CallToolInternal` → `dispatchResolvedTool` and the
session-capability path) records a second pair of instruments once the
`(server, tool)` pair is resolved — this is where meta-tool wrapping is
unwrapped, so a `call_tool` invocation is attributed to the real tool
and the backend server it went to:

| OTel name                              | Type                  | Attributes                          | Prometheus export name                          |
|----------------------------------------|-----------------------|-------------------------------------|--------------------------------------------------|
| `muster.downstream_tool_calls`         | `Int64Counter`        | `mcpserver.name`, `tool`, `outcome` | `muster_downstream_tool_calls_total`             |
| `muster.downstream_tool_call.duration` | `Float64Histogram`/s  | `mcpserver.name`, `tool`, `outcome` | `muster_downstream_tool_call_duration_seconds`   |

`tool` carries the aggregator-exposed name (`x_<server>_<tool>` or the
family name) so the label matches what `list_tools` shows. The server
attribute is `mcpserver.name` — the same key the reconciler's
`muster_mcpserver_state` uses, so fleet state and usage join on
`mcpserver_name`. Core tools (`core_*`, `workflow_*`) are handled
internally, never dispatched, and therefore only appear in the
boundary metrics above.

### Progress relay metrics

| OTel name                                  | Type           | Attributes | Prometheus export name                              |
|--------------------------------------------|----------------|------------|-----------------------------------------------------|
| `muster.downstream_progress_notifications` | `Int64Counter` | `outcome`  | `muster_downstream_progress_notifications_total`    |

It counts the `notifications/progress` downstream servers sent, by
`outcome`: `relayed` to the caller, `unknown_token` (no call in flight
carries the token), `not_increasing` (not above the progress the caller
last got) and `send_failed` (the caller's session could not take it).

### Approval metrics

Tool approval ([Tool approval](../how-to/mcp-server-management.md#tool-approval))
emits these instruments, at the points where it writes its `[AUDIT]` log events
(`approval_requested`, `approval_used`, `approval_refused`, `call_refused`) and
on the approval page (`approval_decision`). No label carries a person, a
session, an approval id, a tool name or an argument; the values of every label
are a small fixed set or the name of an MCPServer.

| OTel name                          | Type                   | Attributes                                  | Prometheus export name                         |
|------------------------------------|------------------------|---------------------------------------------|------------------------------------------------|
| `muster.approval.requests`         | `Int64Counter`         | `mcpserver.name`, `caller_kind`, `source`   | `muster_approval_requests_total`               |
| `muster.approval.decisions`        | `Int64Counter`         | `outcome`                                   | `muster_approval_decisions_total`              |
| `muster.approval.refusals`         | `Int64Counter`         | `reason`, `caller_kind`                     | `muster_approval_refusals_total`               |
| `muster.approval.cap_hits`         | `Int64Counter`         | `scope`, `caller_kind`                      | `muster_approval_cap_hits_total`               |
| `muster.approval.time_to_decision` | `Float64Histogram`/s   | `outcome`                                   | `muster_approval_time_to_decision_seconds`     |
| `muster.approval.pending`          | `Int64ObservableGauge` |                                             | `muster_approval_pending`                      |
| `muster.approval.store_up`         | `Int64ObservableGauge` |                                             | `muster_approval_store_up`                     |
| `muster.approval.last_observed_fallbacks` | `Int64Counter`  | `mcpserver.name`, `reason`                  | `muster_approval_last_observed_fallbacks_total` |
| `muster.approval.settings_refusals` | `Int64Counter`        | `mcpserver.name`, `reason`                  | `muster_approval_settings_refusals_total`      |

- **Asks** (`requests`): calls answered with an approval request. `mcpserver.name`
  is the server called, or `core` or `workflow` for muster's own tools and
  workflows. `caller_kind` is `person`, `actor` (an agent) or `delegated`.
  `source` says what made the call ask: `connector` (the server's approval
  settings), `agent` (the agent's), `governance` (a platform floor or the
  governed default), `builtin` (muster's own tools), `tool`, `unreviewed`,
  `unavailable`, `agent-exception` or `other`.
- **Decisions**: `approved` and `declined` count the person's decision on the
  approval page, `used` an approved call that ran (once), `expired` a request
  nobody decided or used in time. A declined request retried by the caller is
  not counted again.
- **Refusals** by `reason`: `blocked` (the effective approval is Block),
  `settings_unavailable` (the server's settings could not be read or resolved;
  `settings_refusals` below says why), `unavailable` (muster cannot ask: no
  Valkey store, no browser sign-in, no public URL, no identified person),
  `store_unavailable` (the approval store did not answer), `cap_reached` (a cap
  on pending approvals), `foreign` (another person tried to decide a request)
  and `page_check` (the approval page refused a decision on its origin, cookie,
  anti-forgery token or expired flow). Every refusal fails closed.
- **Cap hits**: `scope` is `person` (10 pending per person, across clients) or
  `session` (3 pending per client). A cap hit is also a `cap_reached` refusal.
- **Time to decision**: seconds from the request to the decision on the page.
- **Pending** and **expired** are per replica: each replica tracks the requests
  it made and reconciles them with the store every 15 seconds, so the gauge
  lags by that much and a request decided on another replica in the last
  seconds before it expires can be counted `expired` here. Sum over replicas.
  Only a request this replica made is counted.
- **Store up**: 1 when the replica's last check of the approval store (a Valkey
  `PING`, cached for 10 seconds) succeeded, 0 when it failed. The series is
  absent while no store is registered, that is when muster's shared store is
  not on Valkey; calls that ask are then refused with `reason="unavailable"`.
- **Fallbacks** rise only during a Kubernetes API server outage (`reason` is
  `api_server_unreachable`): the call was decided by the server's last observed
  definition. `settings_refusals` counts the reads that had no usable fallback,
  with `reason` `last_observed_too_old` (older than `approval.lastObservedMaxAge`),
  `never_observed` or `throttled` (HTTP 429, not a fallback case). The same
  reasons are in the warning log lines. Each such refusal is also a
  `settings_unavailable` refusal.

#### Approval alerts

With `muster.observability.metrics.prometheus.prometheusRule.enabled: true` the
chart's PrometheusRule carries a second group, `muster-approval`:

| Alert | Fires when | For |
|---|---|---|
| `MusterApprovalUnavailable` | refusals with `reason` `unavailable`, `store_unavailable` or `settings_unavailable` keep coming: calls that ask cannot run, among them the calls refused once the last observed fallback ended | 10m |
| `MusterApprovalStoreDown` | a replica's `muster_approval_store_up` is 0 | 5m |
| `MusterApprovalHighDeclineOrExpiryRatio` | more than half of the last hour's decided or expired requests were declined or expired, with at least 10 of them | 30m |
| `MusterApprovalOutageFallbackInUse` | calls keep being decided by a server's last observed definition because the API server cannot be reached; the window stays well below `approval.lastObservedMaxAge` (30m by default), which ends the fallback | 10m |

The alerts carry `severity: warning` and `topic: muster`, no team or routing
labels and no runbook links: route them in your Alertmanager. They aggregate by
`cluster_id`, `installation`, `pipeline`, `provider` and `namespace` (the
labels the first group groups by, and `reason` where it is the alert's
dimension), never by pod, so a muster rollout does not resolve and re-fire
them, and one ruler evaluating several clusters keeps one alert per cluster. Unit tests with promtool are in
`helm/muster/tests/promtool/` (run `helm/muster/tests/promtool/run.sh`).

### Grafana dashboard

The chart ships two ready-made dashboards built on these metrics, in one
ConfigMap. `helm/muster/dashboards/muster.json` (uid `muster`): MCP server
fleet state, per-server/per-tool usage and latency, aggregator boundary
traffic, and workflow executions. `helm/muster/dashboards/muster-approval.json`
(uid `muster-approval`, "Muster / Tool approval"): asks by source and by
server, decisions by outcome, time to decision, refusals by reason, cap hits,
pending requests, the approval store and the outage fallback. Enable them with
`muster.observability.grafanaDashboard.enabled: true` (off by default) and
add the label your Grafana sidecar selects on through
`muster.observability.grafanaDashboard.labels` (for the upstream sidecar,
`grafana_dashboard: "1"`); the folder is `grafanaDashboard.folder`. On Giant
Swarm clusters additionally set `grafanaDashboard.giantswarm.enabled: true`
to have the observability platform pick it up.

### Workflow execution metrics

The workflow execution tracker emits three instruments, recorded once
per finished workflow run:

| OTel name                              | Type                  | Attributes          | Prometheus export name                       |
|----------------------------------------|-----------------------|---------------------|----------------------------------------------|
| `muster.workflow_executions`           | `Int64Counter`        | `workflow`, `status`| `muster_workflow_executions_total`           |
| `muster.workflow_execution.duration`   | `Float64Histogram`/s  | `workflow`, `status`| `muster_workflow_execution_duration_seconds` |
| `muster.workflow_execution.store_errors`| `Int64Counter`       | `workflow`          | `muster_workflow_execution_store_errors_total`|

`status` is the terminal execution state (`completed` or `failed`).
These are cheap workflow-level signals; per-step metrics are not wired.
`muster_workflow_execution_store_errors_total` counts records that
failed to persist — a non-zero rate means execution history is being
lost (and dashboards built on the `WorkflowExecution` CRD will be
incomplete).

## Structured logs

The aggregator emits one info-level line per tool call from the
subsystem `MCP-Tool`:

```
msg=tool call subsystem=MCP-Tool tool=call_tool outcome=ok duration_s=0.042
```

On error:

```
msg=tool call subsystem=MCP-Tool tool=call_tool outcome=error duration_s=2.118 error="upstream timeout"
```

The line carries the final post-handler outcome the client sees.

A second line of the same subsystem is written once the call ran, by the
dispatch layer that resolved it (`CallToolInternal`, or the handler of a
core tool a client calls directly): who called which tool on which server.
For an agent session the boundary line's `tool` is always `call_tool`, so
this is the line an audit or a proof counts:

```
msg="tool dispatched" subsystem=MCP-Tool subject=CiQwOGE4... server=kubernetes tool=x_kubernetes_list_pods outcome=ok duration_s=0.041 transportSessionID=mcp-session-ffa8afa1-eb56-4692-98d2-...
```

`subject` is the caller's subject truncated to its first eight characters,
like every identity in the log. `server` is the MCPServer the call reached;
it is absent for muster's own core and workflow tools (`core_*`,
`workflow_*`), which reach no backend. `tool` is the aggregator-exposed
name, the one `list_tools` shows. A workflow's steps each write their own
line; `call_tool` itself writes none, the tool it runs does. Arguments and
results never appear on the line, nor does the error: the boundary line
carries it, and a backend's error can quote what the call carried.

It sits next to the connection line of the subsystem `Connection`, written
once when a session connects a server:

```
msg="User CiQwOGE4... connected to kubernetes with 42 tools, 3 resources, 0 prompts" subsystem=Connection
```

Together they say which subject connected which server and what it ran
there.

When an OTLP endpoint is set, the log records go to the OTLP exporter and are also written as JSON to stderr, with `trace_id` and `span_id`. Thus `kubectl logs` shows the log, and the log stays available when the collector is down. `muster serve --silent` stops the stderr copy.

## Query catalog

### Tempo — find traces for a single backend tool

```
{ resource.service.name = "muster" && span.mcpserver.name = "kubernetes" && span.gen_ai.tool.name = "list_pods" }
```

This query also finds calls made through `call_tool`. To get the server spans of those calls, filter on `span.muster.downstream.tool.name = "x_kubernetes_list_pods"`.

### Mimir — tool-call rate by outcome

```
sum by (tool, outcome) (rate(muster_tool_calls_total[5m]))
```

### Mimir — usage per backend server and real tool

```
sum by (mcpserver_name) (rate(muster_downstream_tool_calls_total[5m]))
topk(10, sum by (tool) (increase(muster_downstream_tool_calls_total[24h])))
```

### Mimir — p95 tool latency

```
histogram_quantile(0.95,
  sum by (tool, le) (rate(muster_tool_call_duration_seconds_bucket[5m]))
)
```

Every duration histogram muster emits is recorded in seconds and shares
one explicit bucket set, applied by a `sdkmetric.View` matched on unit
`s`. The boundaries live in `pkg/observability/histogram.go`; they span
local meta-tool calls in the low milliseconds, remote backend calls
dispatched through `call_tool` in the seconds, and workflow executions
running for minutes. Quantiles are interpolated within a bucket, so
resolution past the last boundary is "greater than that boundary" only.
The OTel SDK defaults (`0` ... `10000`) are spaced for milliseconds and
would put every observation in the first bucket.

### Loki — tool error log lines

```
{namespace="muster", container="muster"} | json | subsystem="MCP-Tool" | outcome=~"error.*"
```

### Loki — one subject's calls on one server

```
{namespace="muster", container="muster"} | json | msg="tool dispatched" | subject="CiQwOGE4..." | server="kubernetes"
```

`count_over_time` of that stream is the number of calls the subject ran
on the server.

## Verification on a real cluster

After deploying with `muster.observability.otel.endpoint` set:

1. Trigger an MCP tool call from a Claude Code session (any
   `x_kubernetes_*` or `x_prom_*` tool).
2. Tempo: search by `service.name=muster`. Expect an
   `mcp.tools/call` server span, a `tool.<tool>` child and an
   `mcp.tools/call` client span with `gen_ai.tool.name` and
   `mcpserver.name`. Backend spans (if the backend emits any) join
   through `traceparent`.
3. Mimir:
   `sum(rate(muster_tool_calls_total{outcome="ok"}[1m])) by (tool)` —
   non-zero rate for the called meta-tool.
4. Loki: one row per call in the `MCP-Tool` subsystem with the
   expected `tool`, `outcome`, `duration_s` fields.
