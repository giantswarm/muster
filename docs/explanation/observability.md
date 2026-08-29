# Observability — tracing, metrics, structured logs

Muster emits OpenTelemetry traces and metrics for every MCP tool call,
plus one structured log line per call carrying the same fields. All
three signals correlate by tool name and span/trace ID so dashboards
can pivot between them.

## What muster contributes to a trace

For every MCP tool call the aggregator emits two spans:

```
  span: tool.<meta-tool>            (mcp-go middleware: call_tool, list_tools, …)
    └── span: tool.<real-tool>      (CallToolInternal: x_kubernetes_list_pods, workflow_*, …)
```

The outer span comes from the middleware on `mcpserver.NewMCPServer`
— only meta-tools (`call_tool`, `list_tools`, `describe_tool`) reach
that layer. The inner span is opened inside `CallToolInternal` and
carries the actual workload tool name. Both spans set
`mcp.tool.name` and use `SpanKindInternal`.

Anything downstream of `CallToolInternal` — a same-cluster backend MCP
server, an in-line gateway, an external HTTPS upstream — appears as a
sibling/child trace if it itself emits spans. W3C TraceContext +
Baggage propagators are always installed (even when muster has no OTLP
endpoint configured), so inbound `traceparent` headers propagate to
outbound calls regardless of muster's export configuration.

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

### Grafana dashboard

The chart ships a ready-made dashboard built on these metrics
(`helm/muster/dashboards/muster.json`, uid `muster`): MCP server fleet
state, per-server/per-tool usage and latency, aggregator boundary
traffic, and workflow executions. Enable it with
`muster.observability.grafanaDashboard.enabled: true`; on Giant Swarm
clusters additionally set `grafanaDashboard.giantswarm.enabled: true`
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

## Query catalog

### Tempo — find traces for a single tool name

```
{ resource.service.name = "muster" && name = "tool.x_kubernetes_list_pods" }
```

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

### Loki — tool error log lines

```
{namespace="muster", container="muster"} | json | subsystem="MCP-Tool" | outcome=~"error.*"
```

## Verification on a real cluster

After deploying with `muster.observability.otel.endpoint` set:

1. Trigger an MCP tool call from a Claude Code session (any
   `x_kubernetes_*` or `x_prom_*` tool).
2. Tempo: search by `service.name=muster`. Expect a
   `tool.<meta-tool>` parent span with a child `tool.<real-tool>`
   span. Downstream spans (if the backend emits any) join via
   `traceparent`.
3. Mimir:
   `sum(rate(muster_tool_calls_total{outcome="ok"}[1m])) by (tool)` —
   non-zero rate for the called meta-tool.
4. Loki: one row per call in the `MCP-Tool` subsystem with the
   expected `tool`, `outcome`, `duration_s` fields.
