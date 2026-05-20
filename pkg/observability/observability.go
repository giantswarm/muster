// Package observability holds the shared OpenTelemetry identifiers that
// every muster package emits under, so a single TracerProvider / MeterProvider
// scope joins spans, metrics, and logs across the codebase. The constants are
// intentionally a leaf package with no internal/* dependencies, so any
// internal/<svc> package can import them without violating the service-locator
// pattern.
package observability

// TracerName is the OpenTelemetry instrumentation scope used for every span
// muster emits — server-side mcp-go spans, the outbound mcp-go client spans on
// the muster → backend leg, and per-step workflow spans. Span role is encoded
// in SpanKind (Server / Client / Internal) and span name (mcp.<method>,
// tool.<name>, workflow.step), so a single scope keeps dashboard filtering
// readable without a per-package suffix.
//
// Used identically as the OTel meter scope; both join on the same identifier
// so spans and metrics correlate in Tempo + Mimir.
const TracerName = "github.com/giantswarm/muster"

// AttrToolName is the OpenTelemetry attribute key carrying the MCP tool name
// dispatched in a request. Shared across every emitter that records tool
// identity so Grafana joins traces, logs, and metrics on a single key.
const AttrToolName = "mcp.tool.name"
