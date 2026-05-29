# Stateless protocol (MCP 2026-07-28)

The headline change in the MCP `2026-07-28` release candidate is that the
protocol is now **stateless at the protocol layer**. Six Specification
Enhancement Proposals (SEPs), plus the W3C Trace Context conventions
documented in SEP-414, together remove the `initialize` handshake, the
`Mcp-Session-Id` header, the long-lived GET SSE stream, and the
free-floating server-to-client request channel, and replace them with
per-request metadata in `_meta`, header-level routing, an explicit
discovery RPC, an inline multi-round-trip mechanism, response-side TTLs,
and standardised distributed-tracing keys.

This document covers the four subsections of the announcement section "A
Stateless Protocol" — handshake / session removal, server-to-client
restructuring, headers and caching, and tracing — and ends with the
concrete muster-side work that follows from each.

## 1. What the spec says

### 1.1 The handshake and session are gone

**Before (`2025-11-25`).** A Streamable HTTP client first POSTs an
`initialize` request, the server replies with capabilities and an
`Mcp-Session-Id`, and every subsequent request must carry that header.
Routing is pinned to whichever server instance issued the ID, and
shared session storage is needed for any deployment that scales
horizontally. The announcement renders this contrast directly in code
samples in the "A Stateless Protocol" section
([blog post](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)).

**After (`2026-07-28`).** SEP-2575 ("Make MCP Stateless")
[PR #2575](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2575)
removes the initialization handshake. The protocol version, client
software identity, and client capabilities that used to be exchanged
once now travel in `_meta` on **every** request, keyed under the
`io.modelcontextprotocol/protocolVersion`,
`io.modelcontextprotocol/clientInfo`, and
`io.modelcontextprotocol/clientCapabilities` fields, with the protocol
version also pinned in the `MCP-Protocol-Version` HTTP header. A new
`server/discover` RPC lets clients fetch `supportedVersions`,
`ServerCapabilities`, `serverInfo`, and `instructions` up front when
they need them (for example, for UI hydration), but calling it is
optional — any RPC can be invoked directly, with version mismatches
surfaced as a new `UnsupportedProtocolVersionError` containing the
server's `supported` list so the client can retry on a mutually
supported version. Per-request capability declarations are mandatory,
not inferred from earlier traffic, and a missing required capability
is surfaced by a new
`MissingRequiredClientCapabilityError` (`-32003`,
`400 Bad Request` on HTTP).

SEP-2575 also removes the Streamable HTTP GET endpoint and the
resumable SSE / `Last-Event-ID` reconnection path. A new
`subscriptions/listen` RPC takes its place as the long-lived
notification channel; clients opt into individual notification streams
(`toolsListChanged`, `promptsListChanged`, `resourcesListChanged`, …)
on that single POST, and the server **MUST NOT** push notification
types the client didn't ask for. Workloads that need durability or
resumability are pushed onto the Tasks extension instead, since
resuming an arbitrary request would require server-side per-request
state — the very thing SEP-2575 sets out to remove.

SEP-2567 ("Sessionless MCP via Explicit State Handles")
[PR #2567](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2567)
removes the `Mcp-Session-Id` header and the protocol-level session
concept that came with it. With sessions gone, results of
`tools/list`, `resources/list`, and `prompts/list` **MUST NOT** vary
per-connection or as a side effect of other requests on a connection;
they may still vary by the authorization presented on the request,
since credentials are carried on each request, and may change over
time for other reasons (deployments, plan changes), but the connection
itself is no longer a source of variation. SEP-2567 also re-scopes the
JSON-RPC request-ID uniqueness rule ("MUST NOT match the ID of any
other request the sender has issued and not yet received a response
for") and the SSE event-ID uniqueness rule (no longer per-session),
and drops the "do not persist cursors across sessions" advice.

The SEP is explicit that **application state is not removed** — only
the protocol-level abstraction is. Servers that need cross-call state
do what HTTP APIs have always done: a `create_basket()` tool returns a
`basket_id`, and subsequent tools accept it as an ordinary string
argument. Handles are a tool-design pattern, not a protocol primitive;
SEP-2567 documents guidance (opaque handles, `(handle, auth_context)`
validation, tool-described durability, useful expiry errors, optional
`destroy_*` / `list_*` cleanup) but ships no `handles/*` method or
schema type. The migration tax is asymmetric: stdio servers using
process-lifetime state are unaffected mechanically and the change is
purely advisory for them, while HTTP servers using `Mcp-Session-Id`
for application state, telemetry keys, sticky routing, or auth
bindings need a designed replacement (typically the authenticated
principal or a server-minted handle).

### 1.2 Server-to-client requests, restructured

A stateless protocol still needs a way for the server to ask the
client for something mid-call — an elicitation prompt, a sampling
request, a `roots/list` lookup. Two SEPs rebuild that flow so it works
without a persistent connection.

SEP-2260 ("Require Server requests to be associated with a Client
request")
[PR #2260](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2260)
makes it normative (was previously `SHOULD`) that
`roots/list`, `sampling/createMessage`, and `elicitation/create`
**MUST** be issued only while the server is actively processing a
client-originated request such as `tools/call`, `resources/read`, or
`prompts/get`. Free-floating server-initiated requests "outside
notifications" **MUST NOT** be implemented; the operational
server-to-client `ping` is the only exception. The user never sees an
elicitation appear out of nowhere, and every prompt traces back to
something the user (or their agent) started.

SEP-2322 ("Multi Round-Trip Requests" / MRTR)
[PR #2322](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2322)
changes how those prompts are delivered. Instead of holding an SSE
stream open while waiting for a `sampling/createMessage` or
`elicitation/create` response on a separate request that has to be
matched back to the original tool call (via either sticky load
balancing or a shared store), the server returns an
`InputRequiredResult` — a new `resultType: "inputRequired"` envelope —
inline as the result of the original request:

```json
{
  "resultType": "inputRequired",
  "inputRequests": {
    "confirm": {
      "type": "elicitation",
      "message": "Delete 3 files?",
      "schema": { "type": "boolean" }
    }
  },
  "requestState": "eyJzdGVwIjoxLCJmaWxlcyI6WyJhIiwiYiIsImMiXX0="
}
```

`inputRequests` is a server-keyed map whose values are the existing
`CreateMessageRequest` / `ElicitRequest` / `ListRootsRequest` shapes;
`requestState` is opaque server-encoded state (the SEP's preferred
pattern is for the server to encode all the state it needs to resume
into that string and treat the retry as a fresh request, removing any
need for sticky routing or shared session storage). The client
collects answers from the user or its sampler, then re-issues the
**original** tool/resource/prompt request, passing the original
arguments plus `inputResponses` (a map keyed the same way as
`inputRequests`) and the echoed `requestState`. Any server replica can
pick up the retry because everything it needs is in the payload. The
SEP is explicit that this is a breaking change and replaces the prior
server-initiated-request flow entirely; persistent tools that need
true cross-call durability are expected to move to the Tasks
extension.

### 1.3 Routable, cacheable traffic

Three smaller changes make the resulting traffic easier to operate on
plain HTTP infrastructure.

SEP-2243 ("HTTP Standardization")
[PR #2243](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2243)
requires every Streamable HTTP POST to carry `Mcp-Method` (mirroring
the JSON-RPC `method`) and, for `tools/call`, `resources/read`, and
`prompts/get`, `Mcp-Name` (mirroring `params.name` or `params.uri`).
Servers (and any intermediary that processes the body) **MUST** reject
requests where the headers and body disagree, with JSON-RPC error code
`-32001` (`HeaderMismatch`) and HTTP `400 Bad Request`. SEP-2243 also
introduces an `x-mcp-header` JSON-Schema extension on tool input
parameters that lets servers mark primitive parameters to be mirrored
into `Mcp-Param-{Name}` headers (for example, a `region` parameter on
a Spanner `execute_sql` tool becomes `Mcp-Param-Region: us-west1`);
clients **MUST** support `x-mcp-header` even though servers use it
optionally, and **MUST** Base64-encode values that contain non-ASCII
characters, control characters, or leading/trailing whitespace using
the `=?base64?…?=` framing. The headers reject requests with character
violations and any client/body mismatch.

SEP-2549 ("TTL for List Results")
[PR #2549](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2549)
adds a new `CacheableResult` interface with two **required** fields,
`ttlMs` (integer milliseconds, `>= 0`) and
`cacheScope` (`"public"` or `"private"`), to the results of
`tools/list`, `prompts/list`, `resources/list`, `resources/read`, and
`resources/templates/list`. Semantics are modeled on HTTP
`Cache-Control`: `ttlMs = 0` means immediately stale, a positive value
is a freshness hint counted from receipt, and `cacheScope: "private"`
forbids shared intermediaries from serving a cached copy to a
different user. The TTL **supplements** rather than replaces
`notifications/*/list_changed`: a server may emit both, and an
incoming list-changed notification invalidates the cache regardless of
remaining TTL. Pagination is page-by-page (each page carries its own
`ttlMs`, all pages share the same `cacheScope`). Together with
SEP-2567's removal of per-session variation, list responses now have a
stable, deployment-wide cache key (deployment plus authenticated
principal) instead of needing to be re-fetched on every new
connection.

### 1.4 Traceable across SDKs and gateways

SEP-414 ("Document OpenTelemetry Trace Context Propagation
Conventions")
[PR #414](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/414)
documents what several SDKs (C#, Python, the Logfire instrumentation,
the Envoy AI Gateway, ToolHive, …) were already doing: the `_meta`
field is the carrier for W3C Trace Context, and the keys
`traceparent`, `tracestate`, and `baggage` are exempt from the
otherwise-mandatory reverse-DNS prefix on `_meta` keys. Values follow
the [W3C Trace Context](https://www.w3.org/TR/trace-context/) and W3C
Baggage formats (for example,
`traceparent: 00-0af7651916cd43dd8448eb211c80319c-00f067aa0ba902b7-01`).
The exception exists specifically so that traces correlate across
implementations and so the OpenTelemetry semantic conventions for MCP
(which already specify `_meta` as the carrier) line up with the spec
text. With the key names fixed, a trace started in a host application
follows a tool call through the client SDK, the MCP server, and
whatever the server calls downstream as a single span tree in any
[OpenTelemetry](https://opentelemetry.io/)-compatible backend.

## 2. Linked SEPs and PRs

- [SEP-2575 — Make MCP Stateless](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2575)
- [SEP-2567 — Sessionless MCP via Explicit State Handles](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2567)
- [SEP-2260 — Require Server requests to be associated with a Client request](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2260)
- [SEP-2322 — Multi Round-Trip Requests (MRTR)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2322)
- [SEP-2243 — HTTP Header Standardization for Streamable HTTP Transport](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2243)
- [SEP-2549 — TTL for List Results](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2549)
- [SEP-414 — Document OpenTelemetry Trace Context Propagation Conventions](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/414)
- Announcement, section "A Stateless Protocol": [2026-07-28 release candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
- Roadmap context: [The Future of MCP Transports (Dec 2025)](https://blog.modelcontextprotocol.io/posts/2025-12-19-mcp-transport-future/)

## 3. Muster impact

Muster is, by design, both an inbound MCP server (for clients like
Cursor, Claude Code, the muster agent itself) and an outbound MCP
client (for every aggregated upstream server). The stateless rework
hits both surfaces, and it is the section of the 2026-07-28 release
with the largest blast radius on this codebase.

### 3.1 Session layer

The protocol-level "MCP session" is the abstraction muster's session
layer is built around today.
[internal/aggregator/session_connection_pool.go](../../../internal/aggregator/session_connection_pool.go)
keys its `poolKey` on `(SessionID, ServerName)` and exposes
`Get/Put/Evict/EvictSession(sessionID, …)` for every pooled MCP
client. The `CapabilityStore` interface in
[internal/aggregator/capability_store.go](../../../internal/aggregator/capability_store.go)
caches tools/resources/prompts per `(sessionID, serverName)` with a
30-day session-level TTL, plus a `Valkey` variant in
`capability_store_valkey.go` so that the cache survives across pods.
The `SessionAuthStore` interface in
[internal/aggregator/session_auth_store.go](../../../internal/aggregator/session_auth_store.go)
(and `session_auth_store_valkey.go`) answers "may this session call
tools on this server?" again keyed on `sessionID`. The session ID is
threaded through every tool handler via `requireSessionContext` in
[internal/aggregator/connection_helper.go](../../../internal/aggregator/connection_helper.go),
which pulls `getSessionIDFromContext(ctx)` and `getUserSubjectFromContext(ctx)`.
The session-scoped tool visibility model is documented in
[docs/explanation/decisions/006-session-scoped-tool-visibility.md](../decisions/006-session-scoped-tool-visibility.md)
and the connection pool design in
[docs/explanation/decisions/011-session-connection-pool.md](../decisions/011-session-connection-pool.md);
both ADRs predate SEP-2567 and need re-evaluation.

With SEP-2567's removal of the protocol-level session, muster must
decide which of these structures correspond to a muster-internal
concept (the authenticated principal, an SSO binding, a pooled
network connection) and which only existed because the protocol used
to hand us a session ID. The session ID we get from inbound clients on
`2026-07-28` will, for transport-level purposes, be one-shot: clients
no longer have to send `Mcp-Session-Id` and shouldn't. SEP-2567
explicitly calls out muster's category — "Proxies and gateways using
session ID for sticky routing" — and says the migration is to drop
sticky routing if the upstream is stateless (or migrates to explicit
handles whose state key is in the tool arguments), and to fall back to
the authenticated principal where a correlation key is still needed.

Concretely:

- The `(sessionID, serverName)` keys on the pool, capability store,
  and auth store are likely to become `(authPrincipal, serverName)`
  (or similar) for inbound, while outbound MCP connections from muster
  to upstreams no longer have a "session" to participate in at all.
- The `EvictSession` / `Delete(sessionID)` / `RevokeSession` /
  `RevokeServer` API surfaces and the `subjectSessionTracker` in
  [internal/aggregator/server.go](../../../internal/aggregator/server.go)
  still describe useful muster-internal concepts (per-user logout,
  per-server deregistration), but their keying needs to be rebased.
- The 30-day capability-store TTL becomes the right TTL to compare
  against the per-result `ttlMs` from SEP-2549 — see §3.4.

### 3.2 Server transport

Muster's inbound Streamable HTTP server lives in
[internal/aggregator/server.go](../../../internal/aggregator/server.go),
`server_options.go`, and `connection_helper.go`. Adopting the
`2026-07-28` transport means:

- **Drop the inbound `initialize` / `initialized` handshake** and stop
  treating the absence of a session ID as an error. Per SEP-2575,
  the first request muster sees may be any RPC; the per-request
  `_meta` is the new source of truth for protocol version and client
  capabilities, with `MCP-Protocol-Version` mirrored in the HTTP
  header.
- **Implement `server/discover`** as a first-class RPC that returns
  `supportedVersions`, the aggregator's `ServerCapabilities`,
  `serverInfo`, and `instructions`. The aggregator already assembles
  capability views from the connected upstreams; this becomes the
  on-demand replacement for what initialization used to push.
- **Surface the new errors** — `UnsupportedProtocolVersionError`
  (with `supported`) and `MissingRequiredClientCapabilityError`
  (`-32003`, HTTP `400`) — instead of failing the handshake.
- **Route on `Mcp-Method` / `Mcp-Name`** rather than parsing the body,
  and reject requests where the headers and body disagree
  (`-32001 HeaderMismatch`, HTTP `400`). For tool authors who want
  region/tenant/priority routing, the aggregator must honour
  `x-mcp-header` annotations on tool input schemas and emit the
  corresponding `Mcp-Param-*` headers when forwarding outbound calls.
- **Replace the GET SSE / resumable stream path** with
  `subscriptions/listen`. The current aggregator already dispatches
  `notifications/tools/list_changed`,
  `notifications/resources/list_changed`, and
  `notifications/prompts/list_changed` (see
  `internal/agent/repl.go` and the matcher in
  `internal/aggregator/notification_subscriber_test.go`); under
  `2026-07-28` the wire-level subscription is opt-in per
  notification type, and the resumable / `Last-Event-ID` reconnection
  is removed. Anything that currently relies on long-lived SSE for
  the inbound side needs to migrate to the request-scoped MRTR pattern
  or to the Tasks extension.

### 3.3 Outbound MCP clients

The outbound MCP clients in
[internal/mcpserver/client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go),
`client_sse.go`, `client_stdio.go`, `client_dynamic_auth.go`, and the
shared `client_interface.go` all sit on top of `mark3labs/mcp-go` and
its `transport.HTTPHeaderFunc` plumbing. They currently call an
`Initialize(ctx)` method that performs the protocol handshake (see
`StreamableHTTPClient.Initialize` and the equivalents on the other
transports) and rely on the upstream issuing an `Mcp-Session-Id` to
maintain the connection. For 2026-07-28:

- The clients must stop performing an `initialize` handshake against
  upstreams that advertise `2026-07-28`; protocol version and
  capabilities go in `_meta` on each outbound request, and
  `MCP-Protocol-Version` in the HTTP header.
- The clients must set `Mcp-Method` (and `Mcp-Name`, where applicable)
  on every outbound POST and honour the upstream's `x-mcp-header`
  annotations.
- They must accept `InputRequiredResult` from upstream and either
  satisfy the elicitations themselves (when muster is acting as the
  agent host), or surface them as MRTR-style retries upwards.
- They must drop reliance on `Mcp-Session-Id` headers on the outbound
  request; sticky upstream routing is no longer the spec's
  responsibility, and any per-upstream cross-call state in the
  outbound direction must move to explicit, server-minted handles
  embedded in tool arguments per SEP-2567.

mcp-go itself will need to ship a `2026-07-28`-aware client and a
matching `WithClientTracing` adapter (we already wire
[mcp-go/otel](https://github.com/mark3labs/mcp-go) to propagate
`traceparent` on outbound — see the CHANGELOG entry "Outbound MCP
clients … install mcp-go's OTEL tracer …"). Until it does, muster's
implementation of `MCPClient` will have to gate behaviour on the
negotiated protocol version.

### 3.4 Caching: `ttlMs` / `cacheScope` and notifications

The current aggregator caches per-`(sessionID, serverName)` capability
sets in
[internal/aggregator/capability_store.go](../../../internal/aggregator/capability_store.go)
(plus `capability_store_valkey.go`) with a flat
`DefaultCapabilityStoreTTL = 30 * 24 * time.Hour`, and it depends on
`notifications/*/list_changed` from upstreams to refresh — driven by
the long-lived SSE/notification stream and surfaced by handlers in
`internal/aggregator/notification_subscriber.go`,
[internal/aggregator/registry.go](../../../internal/aggregator/registry.go),
and `internal/agent/client.go`. With SEP-2549:

- The cache TTL for each `tools/list` / `resources/list` /
  `prompts/list` / `resources/read` / `resources/templates/list`
  entry should come from the upstream's per-response `ttlMs` rather
  than a hard-coded constant; the 30-day default should be the upper
  bound, not the only value.
- `cacheScope` must be honoured: `"private"` responses **MUST NOT** be
  served from a shared cache (the Valkey-backed store) to a different
  authenticated principal. This restricts what the multi-pod, shared
  capability cache can hold and changes how
  `valkey`-backed entries are keyed.
- A list-changed notification still invalidates the cached entry
  regardless of remaining TTL — that behaviour stays the same — but
  with the GET SSE stream removed (SEP-2575), the long-lived
  notification channel switches to `subscriptions/listen`, which has
  to be wired through the outbound client layer.
- Because SEP-2567 forbids per-session variation of list results,
  muster's outbound view of an upstream's tool set can be cached
  across all inbound principals that share the same authorization —
  giving a "fetch once, reuse for the entire deployment" path that
  the current `(sessionID, serverName)` keying cannot express.

### 3.5 Tracing: `_meta` keys for `traceparent` / `tracestate` / `baggage`

Muster already propagates W3C trace context: the CHANGELOG entry
("Outbound MCP clients (stdio, SSE, streamable-http, dynamic-auth)
install mcp-go's OTEL tracer via the `mcp-go/otel.WithClientTracing`
adapter so the muster → backend leg inherits the inbound trace context
and a W3C `traceparent` is emitted on every outgoing JSON-RPC frame")
documents the current setup, and the same paragraph notes that
"W3C TraceContext + Baggage propagators are installed unconditionally
so inbound `traceparent` headers are honoured even when no exporter is
configured". The Helm chart in `helm/muster/values.yaml` notes that
inbound `traceparent` headers from agentgateway propagate to outbound;
[pkg/logging/logging.go](../../../pkg/logging/logging.go) decorates
log records with the active span's `TraceID` and `SpanID`; and
[docs/explanation/observability.md](../observability.md) documents
the end-to-end behaviour for downstream spans that "join via
`traceparent`". The tracing-related test files
([internal/mcpserver/tracing_test.go](../../../internal/mcpserver/tracing_test.go),
[internal/aggregator/tracing_test.go](../../../internal/aggregator/tracing_test.go),
[internal/workflow/tracing.go](../../../internal/workflow/tracing.go),
[internal/workflow/tracing_test.go](../../../internal/workflow/tracing_test.go))
cover the wiring.

What SEP-414 changes for muster is the **canonical location** of the
keys: the spec now pins them inside `_meta` (alongside, not instead
of, the HTTP-header propagation that mcp-go's OTEL adapter handles).
Muster needs to make sure that:

- When acting as a server, it accepts `traceparent`, `tracestate`,
  and `baggage` inside `_meta` (not just as HTTP headers) and feeds
  them into the OTel propagator before the per-tool span starts.
- When acting as a client, it writes the same keys into `_meta` on
  every outbound request — not under a DNS-prefixed key — so that
  upstream SDKs that read from `_meta` (Python, C#, the Envoy AI
  Gateway, …) participate in the same trace.
- The reserved-key list in any custom `_meta` validators stays in
  sync with SEP-414, since `traceparent` / `tracestate` / `baggage`
  are now exempt from the otherwise-mandatory reverse-DNS prefix rule.

### 3.6 Server-to-client requests (muster as elicitor)

Muster does not, today, issue `elicitation/create` or
`sampling/createMessage` server-to-client requests as part of its own
tool surface (a repo grep for `elicitation` / `Elicit` /
`InputRequired` returns no files). The relevant cases are:

- **Forwarding upstream elicitations.** When an aggregated upstream
  returns `InputRequiredResult`, muster has to either satisfy it on
  behalf of the agent (when it is the host) or forward it to the
  inbound caller using the same MRTR shape, then retry the upstream
  call with the user's `inputResponses` and the echoed
  `requestState`. Because the spec forbids resumable streams and
  requires `requestState` to be self-contained, muster must **not**
  introduce any cross-pod state to track in-flight elicitations.
- **Muster-originated elicitations (future).** If muster ever decides
  to elicit (for example, prompting the user to authorize a new
  upstream mid-tool-call), it has to do so per SEP-2260 — only while
  it is processing a client-originated request — and per SEP-2322,
  by returning `InputRequiredResult` and resuming on retry, not by
  holding open an SSE stream.

## 4. Required changes / migration notes

The work breaks into clean phases. Each item below is meant to be
turned into a separate GitHub issue once the analysis docs in this
folder have all landed.

1. **Track `2026-07-28` support in `mark3labs/mcp-go`** and decide
   muster's adoption window. Most of the work below is gated on the
   underlying SDK speaking the new transport.
2. **Decouple muster's internal session model from `Mcp-Session-Id`.**
   Re-key `SessionConnectionPool`, `CapabilityStore`, and
   `SessionAuthStore` (and their Valkey variants) on something muster
   owns — most likely the authenticated principal — and update
   ADR-006 and ADR-011 to reflect the change.
3. **Implement `server/discover` on the aggregator** and surface
   muster's aggregated capabilities through it. Drop the inbound
   `initialize` requirement and switch to per-request `_meta` parsing.
4. **Wire `Mcp-Method` / `Mcp-Name` (and `Mcp-Param-*` from
   `x-mcp-header`) into both the inbound server and the outbound
   clients,** with the strict header/body equality check on the
   server side and the Base64 encoding rules on the client side.
5. **Replace the GET SSE / `Last-Event-ID` path with
   `subscriptions/listen`** on the inbound server and switch the
   outbound clients to the same channel for `notifications/*/list_changed`
   and `notifications/resources/updated`.
6. **Handle `InputRequiredResult` end-to-end.** Outbound clients must
   accept it from upstreams and surface it; the inbound server must
   be willing to return it on `tools/call`, `resources/read`, and
   `prompts/get`; the aggregator must thread `inputResponses` and
   `requestState` back through to the upstream on retry without
   adding any cross-pod state.
7. **Adopt `ttlMs` / `cacheScope` in the capability store.** Replace
   the flat 30-day TTL with the upstream's per-response `ttlMs`,
   refuse to serve `"private"` responses from a shared cache, keep
   `notifications/*/list_changed` as an immediate-invalidate signal,
   and re-key shared cache entries so that they can be reused across
   inbound principals when the upstream allows it.
8. **Standardise `_meta` trace-context handling.** Accept and emit
   `traceparent` / `tracestate` / `baggage` inside `_meta` (in
   addition to the HTTP-header path mcp-go already wires up) and
   update any custom `_meta` reserved-key validators to honour the
   SEP-414 exception to the reverse-DNS prefix rule.
9. **Update operator-facing docs** —
   [docs/explanation/architecture.md](../architecture.md),
   [docs/explanation/observability.md](../observability.md),
   [docs/explanation/decisions/006-session-scoped-tool-visibility.md](../decisions/006-session-scoped-tool-visibility.md),
   [docs/explanation/decisions/011-session-connection-pool.md](../decisions/011-session-connection-pool.md) —
   to describe muster behaviour after the session removal, the
   header-routing rules, the new caching contract, and the MRTR flow.

## 5. Open questions

- Which authenticated-principal key replaces `sessionID` end-to-end
  in muster (sub from the bearer token? the OAuth `sub`? a derived
  ID that survives token rotation?), and how does it interact with
  the existing `subjectSessionTracker` in
  [internal/aggregator/server.go](../../../internal/aggregator/server.go)?
- The Valkey-backed `CapabilityStore` is keyed per session today. If
  the new key is the authenticated principal, what TTL is appropriate
  (currently 30 days for inactivity), and how does it interact with
  the per-response `ttlMs` from SEP-2549? Probably "min(ttlMs, the
  store-level upper bound)" but worth confirming.
- `subscriptions/listen` is a single long-lived POST that selectively
  delivers notifications. Does muster (which currently runs in a
  multi-pod deployment with Valkey sharing auth/capability state but
  per-pod `SessionConnectionPool`) need to fan notifications across
  pods, or can a notification stream simply terminate against
  whichever pod the subscribe call landed on?
- `InputRequiredResult` requires the server to encode all the state
  it needs to resume into `requestState`. For the elicitations muster
  *forwards* on behalf of an upstream, can we always pass the
  upstream's `requestState` through verbatim, or do we need to wrap
  it (for example, to carry the upstream identifier and the original
  inbound caller's correlation key)?
- The Tasks extension is the spec-blessed home for workflows that
  used to rely on long-lived state or resumability. Should muster's
  `action_<workflow-name>` / `workflow_<workflow-name>` machinery be
  re-expressed as the Tasks extension when speaking `2026-07-28`?
  This depends on the analysis in
  `04-tasks-extension.md` (forthcoming) and on whether the upstream
  Tasks extension is stable enough at GA.
- SEP-2243's `x-mcp-header` is a JSON-Schema annotation. Generated
  workflow tool schemas (`muster test --generate-schema`) and any
  tool factory that builds schemas in code need a story for emitting
  and validating this annotation. The detail belongs in the JSON
  Schema 2020-12 doc (`07-json-schema-2020-12.md`), but the cache
  and routing changes here depend on it.

## 6. References

- [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/) — primary announcement, section "A Stateless Protocol"
- [Exploring the Future of MCP Transports (Dec 2025)](https://blog.modelcontextprotocol.io/posts/2025-12-19-mcp-transport-future/) — roadmap that this section closes out
- [SEP-2575 — Make MCP Stateless](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2575)
- [SEP-2567 — Sessionless MCP via Explicit State Handles](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2567)
- [SEP-2260 — Require Server requests to be associated with a Client request](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2260)
- [SEP-2322 — Multi Round-Trip Requests (MRTR)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2322)
- [SEP-2243 — HTTP Header Standardization for Streamable HTTP Transport](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2243)
- [SEP-2549 — TTL for List Results](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2549)
- [SEP-414 — Document OpenTelemetry Trace Context Propagation Conventions](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/414)
- [W3C Recommendation — Trace Context](https://www.w3.org/TR/trace-context/) and [W3C Baggage](https://www.w3.org/TR/baggage/)
- [OpenTelemetry](https://opentelemetry.io/)
- Draft specification entry points (for context only): [draft spec](https://modelcontextprotocol.io/specification/draft), [draft changelog](https://modelcontextprotocol.io/specification/draft/changelog)
