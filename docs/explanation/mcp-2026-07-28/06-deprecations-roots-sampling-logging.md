# Deprecations: Roots, Sampling, and Logging (MCP 2026-07-28)

The MCP `2026-07-28` release candidate formally **deprecates** three
core protocol features — **Roots**, **Sampling**, and structured
**Logging** — under the brand-new feature lifecycle policy introduced
in the same release. The change is annotation-only: no wire-level
behaviour changes, no types are removed, and capability negotiation is
unchanged. The announcement summarises the move and the replacements
in its "Roots, Sampling, and Logging Are Deprecated" section
([blog post](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)):

> Three core features are deprecated under the new feature lifecycle
> policy (SEP-2577): Roots → tool parameters, resource URIs, or
> server configuration; Sampling → direct integration with LLM
> provider APIs; Logging → `stderr` for stdio transports;
> OpenTelemetry for structured observability.

For muster the most useful framing is the negative one: muster has
**no inbound or outbound implementation of any of these three
methods today**. The audit in §3 below shows that the only
"roots" / "sampling" / "logging" hits in the muster Go tree are
x509 system roots, template "view roots", and muster's own
application logging stack (`pkg/logging`, `internal/aggregator/logging.go`,
`cmd/serve.go`) — which is already an OpenTelemetry/`slog`
pipeline and is therefore aligned with the SEP-2577 replacement
guidance rather than a candidate for migration. That makes this
section primarily a **policy commitment** for the aggregator (forward
the deprecated methods unchanged for the deprecation window if an
upstream advertises them) and a **non-event** for the muster code
base itself.

## 1. What the spec says

### 1.1 Three features, one deprecation SEP

[SEP-2577 — "Deprecate Roots, Sampling, and Logging"](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2577)
was authored by Kurtis Van Gent, reached the `final` label, and was
merged into `draft`. Its PR body states the scope concisely:

> Proposes deprecating three core protocol features: Roots, Sampling,
> and Logging. Features remain fully functional in all spec versions
> released within one year of the deprecating version. No wire-level
> protocol changes — deprecation is advisory only, signaling the
> ecosystem to plan for eventual removal.

The PR diff (`gh pr diff 2577 --repo modelcontextprotocol/modelcontextprotocol`)
adds a single new SEP page,
`docs/seps/2577-deprecate-roots-sampling-and-logging.mdx`, and
registers it in `docs/docs.json`. The abstract of that page enumerates
every protocol surface that is now `@deprecated`:

- **Roots:** the `roots/list` request, the
  `notifications/roots/list_changed` notification, and the
  `ClientCapabilities.roots` capability flag. Schema types covered by
  the annotation: `Root`, `ListRootsRequest`, `ListRootsResult`,
  `ListRootsResultResponse`, and `RootsListChangedNotification`.
- **Sampling:** the `sampling/createMessage` request and the
  `ClientCapabilities.sampling` and
  `ClientCapabilities.tasks.requests.sampling` capability flags.
  Schema types: `CreateMessageRequestParams`, `CreateMessageRequest`,
  `CreateMessageResult`, `CreateMessageResultResponse`,
  `SamplingMessage`, `SamplingMessageContentBlock`, `ToolChoice`,
  `ToolUseContent`, `ToolResultContent`, `ModelPreferences`, and
  `ModelHint`.
- **Logging:** the `logging/setLevel` request, the
  `notifications/message` notification, and the
  `ServerCapabilities.logging` capability flag. Schema types:
  `LoggingLevel`, `SetLevelRequestParams`, `SetLevelRequest`,
  `SetLevelResultResponse`, `LoggingMessageNotificationParams`, and
  `LoggingMessageNotification`.

The union types that reference these (`ClientNotification`,
`ClientResult`, `ServerRequest`, `ServerNotification`) are explicitly
**not** modified during the deprecation period; SEP-2577 records that
they will only be touched when the deprecated types are removed.

### 1.2 The motivation in the SEP's own words

The "Motivation" section of SEP-2577 captures the rationale for each
feature and is worth quoting in full because it directly informs
muster's own decision not to add support for any of them in the
future:

- **Roots:** *"Vague semantics: the specification describes roots as
  informational — servers are not required to respect them, which
  reduces their utility. Overlapping alternatives: working directory
  context can be provided through tool parameters, resource URIs,
  server configuration, or environment variables — all of which are
  more explicit."*
- **Sampling:** *"Complex to implement: correct sampling
  implementation requires human-in-the-loop approval, model selection
  logic, security considerations, and (since SEP-1577) tool loop
  support. … Direct alternatives: servers that need LLM capabilities
  can integrate directly with LLM provider APIs, giving them full
  control over model selection, parameters, and streaming."*
- **Logging:** *"Overlapping infrastructure: standard logging
  mechanisms (stderr for stdio transports, OpenTelemetry for
  structured observability) are mature, widely adopted, and better
  suited to logging than an application-protocol channel."*

The "Security Implications" section is unambiguous about the
direction of travel:

> Sampling is the most security-sensitive of the three. It allows
> servers to request LLM completions through the client, which
> creates attack surface for prompt injection and data exfiltration.
> Removing it reduces this risk. Roots exposes information about the
> client's filesystem to servers. Removing it reduces the risk of
> servers using root information to attempt directory traversal or
> access files outside intended boundaries.

For an aggregator like muster, those two paragraphs argue strongly
against ever **originating** roots or sampling traffic on behalf of an
inbound client, even within the deprecation window.

### 1.3 What "deprecated" means under the lifecycle policy

SEP-2577 piggybacks on the brand-new lifecycle policy introduced in
the same release,
[SEP-2596 — "Specification Feature Lifecycle and Deprecation Policy"](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2596)
(Den Delimarsky, `accepted`, MERGED). SEP-2596 defines three explicit
states for every feature — **Active**, **Deprecated**, **Removed** —
and pins down what each one means on the wire and for SDKs. The two
SEPs together let muster reason about a deprecated feature without
guessing:

- The **minimum deprecation window** is twelve months (SEP-2596,
  "Deprecating a feature"). The clock is anchored to the release of
  the spec revision in which the feature is first marked Deprecated,
  not to the date the deprecation SEP reaches Final. For the three
  features here that revision is `2026-07-28`, so the earliest spec
  revision in which any of them may legally be removed is the first
  Current release on or after `2027-07-28`.
- During the window, **wire behaviour is unchanged** (SEP-2577,
  "Capability negotiation"). Implementations that support the
  features SHOULD keep declaring the capabilities; counterparties
  MUST still handle them correctly; implementations SHOULD emit a
  warning (logs or dev tooling) when a deprecated capability is
  negotiated; new implementations SHOULD NOT adopt them unless needed
  for back-compat.
- The "Removed" state means the type is gone from `draft/` and will
  be absent from the next Current revision (SEP-2596, "Feature
  states" table). It will still be available to an implementation
  that negotiates an older protocol version in which it was Active or
  Deprecated.
- **Tier 1 SDK obligations** (SEP-2596, "Tier 1 SDK obligations"):
  once `2026-07-28` is released as Current, Tier 1 SDKs MUST mark the
  corresponding API surface with the language's native deprecation
  mechanism (`@Deprecated`, `[Obsolete]`, the `Deprecated:` doc
  convention in Go, etc.) referencing SEP-2577 and the earliest
  removal date, and SHOULD emit a runtime warning when a deprecated
  feature is exercised. This is what muster will eventually see
  flowing through `mark3labs/mcp-go` — Go-style `// Deprecated:`
  doc comments on the affected types and methods and, optionally, a
  `slog.Warn` from the SDK when one of them fires.
- **Expedited removal** is possible but bounded (SEP-2596,
  "Expedited removal"): the twelve-month floor can be shortened only
  for an active security risk with a published advisory or
  in-the-wild exploitation and no in-place mitigation, and the
  shortened window must still leave at least ninety days. This caps
  how surprised muster can reasonably be by an earlier-than-expected
  removal of any of these three features.
- The **deprecated registry**
  (`docs/specification/draft/deprecated.mdx`) is the canonical
  single page that lists every feature currently Deprecated, the SEP
  that deprecated it, the revision in which it became Deprecated, and
  its earliest removal date. That page is the upstream signal muster
  should watch, rather than re-reading SEP-2577 each time.

### 1.4 Why deprecation rather than extension migration

SEP-2577's "Rationale" section addresses the obvious alternative head
on:

> These features are already implemented in many clients and servers.
> The extensions mechanism (SEP-2133) specifies that unless an
> extension is provided, implementations must behave as if the
> extension is not present. Retrofitting this logic into existing
> SDKs — especially across multiple protocol versions — would be
> complex and error-prone. Deprecation followed by removal is less
> disruptive: implementations can continue using the features as-is
> during the transition period, then simply stop when the features
> are removed.

This matters for the muster discussion below because it rules out a
"move Roots into an extension" path: the upstream decision is
**remove**, not **relocate**. Anything muster builds on the assumption
that Roots will return as an `ext-*` extension will rot.

### 1.5 What replaces each feature in practice

The release-candidate announcement collapses the replacement story
into a three-row table:

| Feature  | Replacement                                                                |
| -------- | -------------------------------------------------------------------------- |
| Roots    | Tool parameters, resource URIs, or server configuration                    |
| Sampling | Direct integration with LLM provider APIs                                  |
| Logging  | `stderr` for stdio transports; OpenTelemetry for structured observability  |

The draft tools spec
([specification/draft/server/tools](https://modelcontextprotocol.io/specification/draft/server/tools))
operationalises the **Roots replacement**. Tool authors are expected
to declare any required filesystem or workspace context as part of
the tool's JSON Schema 2020-12 `inputSchema` (covered in
[07-json-schema-2020-12.md](07-json-schema-2020-12.md)), with
optional UI hints via `x-mcp-header` and friends. A tool that needs
"the user's repo root" should ask for it as an `inputSchema` property
rather than as a side-channel `roots/list` query. For muster's
workflow engine this is already how
`muster_workflow_<workflow-name>` tools collect their inputs, so the
Roots replacement is essentially a no-op.

The **Logging replacement** is OpenTelemetry. The
[OpenTelemetry project](https://opentelemetry.io/) is the
"open-source observability framework for instrumenting, generating,
collecting, and exporting telemetry data (metrics, logs, and
traces)" — its OTLP logs pipeline is precisely the structured,
multi-process logging story SEP-2577 says replaces
`logging/setLevel` and `notifications/message`. This is also the
exact pipeline muster's own
[pkg/logging](../../../pkg/logging/logging.go) already uses (see
§3 below): the OTel logs SDK fed by `slog`, with TraceID/SpanID
correlation. So muster does not have to "migrate" to OTel; it is
already there.

The **Sampling replacement** — direct LLM provider integration — is
the most decoupled of the three for muster, because muster does not
itself perform inference. Where a workflow step or downstream
upstream MCP server wants LLM access, it integrates with an LLM API
directly (today most muster scenarios use `claude-code` or a similar
host process as the LLM driver and feed muster tools to it). Nothing
in muster has to change for this replacement to take effect.

## 2. Linked SEPs and PRs

- [SEP-2577 — Deprecate Roots, Sampling, and Logging (#2577)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2577) — primary PR; `final`, MERGED; metadata, body, and full diff via `gh pr view 2577` / `gh pr diff 2577 --repo modelcontextprotocol/modelcontextprotocol`. Adds `docs/seps/2577-deprecate-roots-sampling-and-logging.mdx`.
- [SEP-2596 — Specification Feature Lifecycle and Deprecation Policy (#2596)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2596) — accepted, MERGED. Defines the Active / Deprecated / Removed states, the twelve-month minimum deprecation window, Tier 1 SDK obligations, the deprecated registry, the expedited-removal carve-out, and grandfathers the two existing informal deprecations (HTTP+SSE transport and `includeContext: "thisServer" / "allServers"`). Covered in detail in [08-protocol-evolution.md](08-protocol-evolution.md); referenced here for the lifecycle terminology.
- [Draft tools spec — server/tools](https://modelcontextprotocol.io/specification/draft/server/tools) — the place a tool author goes once Roots is no longer available; `inputSchema` properties replace the `roots/list` side channel.
- [OpenTelemetry](https://opentelemetry.io/) — the replacement for structured Logging, and the framework muster's `pkg/logging` already builds on via `mcp-toolkit/logging`.
- [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/) — announcement, section "Roots, Sampling, and Logging Are Deprecated" with the replacement table.
- Cross-section context: [02-extensions-first-class.md](02-extensions-first-class.md) (SEP-2133, the alternative path that SEP-2577 explicitly declined), [04-tasks-extension.md](04-tasks-extension.md) (`ClientCapabilities.tasks.requests.sampling` is one of the deprecated capabilities — the Tasks extension's sampling sub-capability is dropped along with everything else), [08-protocol-evolution.md](08-protocol-evolution.md) (full treatment of SEP-2596 and SEP-2484).

## 3. Muster impact

### 3.1 Audit results: muster speaks none of these MCP methods today

A focused audit across the directories called out in the plan
(`internal/agent/`, `internal/mcpserver/`, `internal/aggregator/`,
`pkg/logging/`) plus the rest of the Go tree finds **zero** uses of
any of the deprecated MCP methods on either the inbound or the
outbound path. Ripgrep results:

- `roots/list`, `notifications/roots/list_changed`,
  `ListRootsRequest`, `ListRootsResult`, `RootsListChangedNotification`,
  `ClientCapabilities.roots` — **no matches** in any `.go` file.
- `sampling/createMessage`, `CreateMessageRequest`,
  `CreateMessageResult`, `SamplingMessage`, `ModelPreferences`,
  `ModelHint`, `ClientCapabilities.sampling` — **no matches** in any
  `.go` file.
- `logging/setLevel`, `notifications/message`,
  `SetLevelRequest`, `LoggingMessageNotification`,
  `ServerCapabilities.logging`, `WithLoggingCapability` — **no
  matches** in any `.go` file.

The only `roots`/`sampling`/`Roots`/`Sampling` substrings in the
muster Go tree are unrelated to the MCP protocol:

- [internal/cli/errors.go](../../../internal/cli/errors.go) lines
  117–120 use `x509.SystemRootsError` to classify TLS trust-store
  errors. Nothing to do with MCP `roots/list`.
- [internal/admin/templates.go](../../../internal/admin/templates.go)
  line 17 has a comment referring to "per-view template roots" for
  the admin UI's HTML template tree. Nothing to do with MCP
  `roots/list`.

The `"logging"` and `logging` references that do show up are all
muster's **own** application logging stack, which is already an
OpenTelemetry-aware `slog` pipeline:

- [pkg/logging/logging.go](../../../pkg/logging/logging.go) wraps
  [giantswarm/mcp-toolkit/logging](https://github.com/giantswarm/mcp-toolkit)
  to initialise an `slog` handler that either writes to stderr (CLI
  mode) or routes records through the OpenTelemetry Logs SDK when
  `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`,
  `OTEL_EXPORTER_OTLP_ENDPOINT`, or `OTEL_LOGS_EXPORTER` is set
  (`Init` doc comment, lines 89–119). The
  `DebugCtx`/`InfoCtx`/`WarnCtx`/`ErrorCtx` family threads
  `context.Context` through so the active span's TraceID and SpanID
  attach to each log record — exactly the OpenTelemetry pipeline
  SEP-2577 names as the Logging replacement.
- [internal/aggregator/logging.go](../../../internal/aggregator/logging.go)
  defines a `server.ToolHandlerMiddleware` that emits one structured
  info-level line per tool call (`msg = "tool call"`, fields `tool`,
  `outcome`, `duration_s`, `error`) via
  `logging.InfoWithAttrsCtx`. This is **muster-internal** observability
  that never crosses the MCP wire; it has no relationship to the
  upstream MCP `logging/setLevel` request or `notifications/message`
  notification.
- [cmd/serve.go](../../../cmd/serve.go) line 114 (`defer
  otelShutdown("logging", shutdownLogging)`) and
  [internal/testing/muster_manager.go](../../../internal/testing/muster_manager.go)
  lines 1162–1168 (`"logging": {"level": "debug"}` in the
  generated test config) are both about muster's application
  logging configuration, not the MCP `logging` capability.

So the SEP-2577 replacement guidance — "stderr for stdio transports;
OpenTelemetry for structured observability" — describes muster's
existing logging stack. There is no muster code today that would need
to be rewritten to comply with the deprecation; the work is to make
sure it stays that way.

### 3.2 Aggregator: pass-through obligations

The aggregator is muster's MCP surface area, and its inbound MCP
server is built with mcp-go in
[internal/aggregator/server.go](../../../internal/aggregator/server.go).
The capability options passed to `mcpserver.NewMCPServer` are
explicit (lines 729–737):

```go
opts := []mcpserver.ServerOption{
    mcpserver.WithToolCapabilities(true),
    mcpserver.WithResourceCapabilities(true, true),
    mcpserver.WithPromptCapabilities(true),
    mcpserver.WithToolFilter(a.sessionToolFilter),
    mcpserver.WithHooks(hooks),
}
opts = append(opts, mcpServerOptions()...)
mcpSrv := mcpserver.NewMCPServer("muster-aggregator", serverVersion, opts...)
```

There is no `WithLoggingCapability(...)` and no equivalent for
`Roots` or `Sampling`. The outbound client interface
[internal/mcpserver/client_interface.go](../../../internal/mcpserver/client_interface.go)
exposes only `ListTools`, `CallTool`, `ListResources`, `ReadResource`,
`ListPrompts`, `GetPrompt` — nothing else. The aggregator therefore:

- **Does not advertise** the deprecated server-side capability
  (`ServerCapabilities.logging`).
- **Does not advertise** support for any deprecated client-side
  capability (`ClientCapabilities.roots`,
  `ClientCapabilities.sampling`).
- **Does not consume** `LoggingMessageNotification` from upstream
  MCP servers, nor invoke `logging/setLevel` against them.
- **Does not issue** `roots/list` server-to-client requests at its
  inbound interface, nor reply to one on the outbound side.
- **Does not issue** `sampling/createMessage` at its inbound
  interface, nor handle one on the outbound side.

What the aggregator does have to do during the deprecation window is
**not break upstream MCP servers that still rely on the deprecated
methods**. SEP-2596's "Tier 1 SDK obligations" combined with
SEP-2577's "Capability negotiation" rules say wire behaviour MUST be
unchanged. For muster the implications are narrow:

- If a downstream MCP server connected through
  [internal/mcpserver/client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go),
  [client_sse.go](../../../internal/mcpserver/client_sse.go), or
  [client_stdio.go](../../../internal/mcpserver/client_stdio.go)
  advertises `ServerCapabilities.logging`, muster's aggregator is
  not required to subscribe to that capability — but it MUST NOT
  refuse the connection just because the capability is advertised.
  Today muster ignores the capability flag entirely, which already
  satisfies this rule.
- If an inbound MCP client (e.g. an LLM host connecting through
  [internal/aggregator/server.go](../../../internal/aggregator/server.go))
  advertises `ClientCapabilities.roots` or
  `ClientCapabilities.sampling`, muster MUST NOT reject the
  connection on the strength of those flags either. Today muster's
  aggregator does not inspect client capabilities for these
  features, so this also already holds.
- The aggregator's
  [capability_store.go](../../../internal/aggregator/capability_store.go)
  / [registry.go](../../../internal/aggregator/registry.go) only
  cache `tools/list`, `resources/list`, and `prompts/list` payloads
  (see also [01-stateless-protocol.md](01-stateless-protocol.md)
  §3 for the `ttlMs` / `cacheScope` work). They do not cache or
  proxy any deprecated method, and SEP-2577 does not add anything
  cache-relevant.

If a future muster release ever **adds** a Roots, Sampling, or
Logging implementation, SEP-2577 §"Capability negotiation" requires
the aggregator to emit a deprecation warning when negotiating it.
Concretely that would be a `logging.WarnCtx(ctx, "MCP-Aggregator",
"deprecated capability negotiated: …")` call from inside the
mcp-go server hook (the `WithHooks(hooks)` line above is the right
place to wire it). Because muster does not currently negotiate any
of these capabilities, that warning is not needed today — but the
hook is the agreed extension point if it is ever needed.

### 3.3 Agent: REPL and agent-mode MCP server

Muster's agent layer (`internal/agent/`) exposes muster either as an
interactive REPL or as a stdio MCP server that other LLM hosts can
talk to (see the agent commands under
[internal/agent/commands/](../../../internal/agent/commands)). The
same audit applies: no `Root`, `Sampling`, or `CreateMessage` types
appear anywhere in `internal/agent/`. The agent's MCP-server-mode
init in
[internal/agent/test_mcp_server.go](../../../internal/agent/test_mcp_server.go)
follows the same `mark3labs/mcp-go` pattern as the aggregator and
similarly does not advertise the deprecated capabilities.

The replacement guidance is, again, already in place: any contextual
state the agent or its workflows need from the user is collected
through tool arguments (the Roots replacement), and any LLM
inference happens in the host process that drives muster (the
Sampling replacement). Structured logging from agent code uses
`logging.InfoWithAttrsCtx` / `logging.ErrorCtx` from
[pkg/logging/logging.go](../../../pkg/logging/logging.go) — i.e.
the OpenTelemetry-aware pipeline that SEP-2577 names as the Logging
replacement.

### 3.4 Documentation and policy posture

The relevant policy commitment for muster is therefore a short one:

- Muster does **not** implement Roots, Sampling, or structured
  Logging over MCP today and will **not** add new support for them
  while they are Deprecated. SEP-2577 §"Capability negotiation"
  recommends exactly this: "New implementations SHOULD NOT add
  support for deprecated features unless needed for backward
  compatibility with existing counterparts."
- Muster's aggregator will **forward the deprecated methods
  unchanged** to upstream MCP servers that advertise them, for as
  long as SEP-2596 keeps them in the spec. Practically, since muster
  does not proxy these methods at all today, "forward unchanged"
  means "do nothing": the methods never enter muster's caching,
  rate-limiting, or audit paths.
- Muster's structured-logging story is already the OpenTelemetry
  pipeline that SEP-2577 names as the replacement for the deprecated
  MCP Logging feature. The Trace-Context plumbing work in
  [01-stateless-protocol.md](01-stateless-protocol.md) §3 closes
  the remaining gap (standardising `traceparent` / `tracestate` /
  `baggage` keys inside `_meta`) so that a single trace can span
  host → muster → upstream MCP server through the OTel pipeline,
  with `slog` records and OTel logs carrying TraceID / SpanID.

## 4. Required changes / migration notes

Because the muster audit comes back empty, the deprecations turn into
a much smaller list of follow-ups than the other 2026-07-28 sections.
There is no code to delete and no API to migrate. The work is
**documentation, posture, and a small amount of defensive plumbing**:

1. **Adopt the policy commitment in this document.** The "muster does
   not implement and will not add support for Roots / Sampling /
   MCP-Logging" statement above is the binding posture. Cross-link
   it from the operator-facing
   [docs/explanation/architecture.md](../architecture.md) so that
   anyone reviewing muster's MCP feature support sees the explicit
   "out-of-scope" line.
2. **Warn on negotiation if we ever change our mind.** If a future
   release does add (for any reason) one of the deprecated
   capabilities, that addition MUST come with a structured warning
   from the aggregator's mcp-go hook
   ([internal/aggregator/server.go](../../../internal/aggregator/server.go)
   `WithHooks(hooks)` line 734) that reads, at minimum,
   `logging.WarnCtx(ctx, "MCP-Aggregator", "deprecated MCP
   capability negotiated", slog.String("capability", "…"),
   slog.String("sep", "SEP-2577"), slog.String("earliest_removal",
   "2027-07-28 or later"))`. SEP-2596 §"Tier 1 SDK obligations"
   makes this SHOULD; muster's house style for visible deprecations
   makes it MUST.
3. **Watch the upstream "Deprecated" registry.** SEP-2596 introduces
   `docs/specification/draft/deprecated.mdx` as the canonical
   single-page index of every Deprecated feature, its SEP, the
   revision in which it became Deprecated, and its earliest removal
   date. The muster `docs/explanation/mcp-2026-07-28/` series should
   include a follow-up review when the Final `2026-07-28` revision
   ships, to confirm the registry entries for Roots / Sampling /
   Logging match the dates assumed in this document. The check
   belongs in [09-release-timeline.md](09-release-timeline.md).
4. **Keep ignoring upstream `logging` capability advertisements.**
   The outbound MCP clients
   ([internal/mcpserver/client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go),
   [client_sse.go](../../../internal/mcpserver/client_sse.go),
   [client_stdio.go](../../../internal/mcpserver/client_stdio.go),
   [client_dynamic_auth.go](../../../internal/mcpserver/client_dynamic_auth.go),
   [client_interface.go](../../../internal/mcpserver/client_interface.go))
   ignore `ServerCapabilities.logging` today. That stays. No new
   subscription, no `logging/setLevel` call, no
   `notifications/message` handling.
5. **Document the Roots-vs-input-schema decision for workflows.**
   Workflow authors should not invent a "roots" parameter on a
   `muster_workflow_<workflow-name>` tool; if they need a filesystem
   or workspace path, it should be a declared property on the
   workflow's `inputSchema` per the draft tools spec
   ([draft server/tools](https://modelcontextprotocol.io/specification/draft/server/tools)),
   bounded and validated using the JSON Schema 2020-12 rules in
   [07-json-schema-2020-12.md](07-json-schema-2020-12.md). Add a
   short note to the workflow-authoring documentation.
6. **Restate the OTel posture in operator docs.** The existing
   [docs/explanation/observability.md](../observability.md) (linked
   from `01-stateless-protocol.md` §4) should grow a short paragraph
   that explicitly says: "muster's structured logging is the
   OpenTelemetry replacement for MCP `logging/setLevel` /
   `notifications/message` named in SEP-2577. Configure OTLP via
   `OTEL_EXPORTER_OTLP_*` and TraceID/SpanID will propagate from
   the host through muster's tools into upstream servers via the
   `_meta` trace-context keys defined in
   [SEP-414](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/414)."
7. **Conformance scenarios.** Add one minimal scenario to
   [internal/testing/scenarios/](../../../internal/testing) that
   asserts muster's aggregator does not advertise
   `ServerCapabilities.logging` in its `server/discover` response,
   and does not negotiate either `ClientCapabilities.roots` or
   `ClientCapabilities.sampling` on outbound connections. This is
   the cheapest way to detect a future regression where someone
   accidentally adds one of the deprecated capabilities back. The
   broader conformance work is tracked in
   [08-protocol-evolution.md](08-protocol-evolution.md).
8. **CHANGELOG framing.** When muster's CHANGELOG entry for the
   `2026-07-28` adoption work is written, it should explicitly note
   that the SEP-2577 deprecations are a **non-event** for muster —
   no code change, no behaviour change for users — so that
   downstream consumers reading the CHANGELOG do not assume there
   is a migration path to plan.

## 5. Open questions

- **Should muster emit the runtime warning even though it doesn't
  negotiate any deprecated capability today?** SEP-2596 only requires
  the warning when a deprecated feature is *exercised*. A defensive
  reading is: register a no-op hook now so that if a future change
  starts negotiating Roots / Sampling / Logging the warning fires
  for free. The simpler reading is: write the warning when the
  feature is added. Worth deciding once rather than re-litigating
  per-feature.
- **Do we want a CLI / admin flag to surface a per-connection
  "deprecated capabilities advertised by upstream" list?** Today
  muster ignores upstream `ServerCapabilities.logging`; an admin
  who wants to know which of their MCP servers are still relying on
  the deprecated capability has no way to find out from muster
  itself. A simple "show me which of my upstreams advertise a
  deprecated MCP capability" report (probably a
  `core_diagnostics_deprecated_capabilities` tool) would help
  operators plan their own migrations during the twelve-month
  window.
- **`ClientCapabilities.tasks.requests.sampling` interaction with
  the Tasks extension.** SEP-2577 deprecates the sampling
  sub-capability under Tasks alongside Sampling itself; the Tasks
  extension as designed in
  [04-tasks-extension.md](04-tasks-extension.md) does not include
  it. Confirm that the muster Tasks-extension plan never reaches for
  the sampling sub-capability (it should not), and that any future
  upstream MCP server that does advertise it triggers the same
  "deprecated capability advertised" warning suggested in item 2 of
  §4 rather than being silently accepted.
- **Removal date alignment with muster's release train.** The
  twelve-month minimum window puts the earliest possible removal in
  the first spec revision released as Current on or after
  `2027-07-28`. SEP-2596 explicitly allows features to remain
  Deprecated indefinitely beyond that floor. Muster's
  [09-release-timeline.md](09-release-timeline.md) should record
  the assumption that we are not designing for an early removal and
  do not need a contingency plan for one.
- **mcp-go SDK readiness.** None of the deprecation annotations are
  in `mark3labs/mcp-go` yet (and may never be needed there because
  the SDK does not implement Roots / Sampling for muster's use
  cases). The check is whether `mark3labs/mcp-go` will start
  emitting `slog.Warn` lines when negotiating the deprecated
  capabilities under SEP-2596 Tier 1 SDK obligations. If it does,
  muster should route those into the existing
  `logging.WarnWithAttrsCtx` pipeline rather than letting them go
  to `stderr` unconditionally. Same SDK-readiness question as the
  other 2026-07-28 docs — see
  [02-extensions-first-class.md](02-extensions-first-class.md)
  §3.4 and [04-tasks-extension.md](04-tasks-extension.md) §5.

## 6. References

- [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/) — announcement, section "Roots, Sampling, and Logging Are Deprecated" (replacement table) and the surrounding context.
- [SEP-2577 — Deprecate Roots, Sampling, and Logging (#2577)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2577) — primary deprecation PR; `final`, MERGED. PR body, schema annotation lists, motivation per feature, and the security analysis quoted above. Fetched via `gh pr view 2577 --repo modelcontextprotocol/modelcontextprotocol` and `gh pr diff 2577 --repo modelcontextprotocol/modelcontextprotocol`.
- [SEP-2596 — Specification Feature Lifecycle and Deprecation Policy (#2596)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2596) — accepted, MERGED. Defines Active / Deprecated / Removed, twelve-month minimum window, expedited-removal carve-out, Tier 1 SDK obligations, and the `deprecated.mdx` registry. Fetched via `gh pr view 2596` / `gh pr diff 2596`.
- [Draft specification — server/tools](https://modelcontextprotocol.io/specification/draft/server/tools) — the replacement for Roots: tool authors declare any required filesystem / workspace context on the tool's JSON Schema 2020-12 `inputSchema` (see [07-json-schema-2020-12.md](07-json-schema-2020-12.md)).
- [OpenTelemetry](https://opentelemetry.io/) — the replacement for structured MCP Logging; the framework muster's `pkg/logging` already uses through `mcp-toolkit/logging`.
- Cross-section context within `docs/explanation/mcp-2026-07-28/`:
  - [01-stateless-protocol.md](01-stateless-protocol.md) — Trace Context propagation in `_meta` (SEP-414) closes the OTel-correlation gap that complements the Logging replacement.
  - [02-extensions-first-class.md](02-extensions-first-class.md) — SEP-2133, the framework SEP-2577 explicitly declined as a migration path.
  - [04-tasks-extension.md](04-tasks-extension.md) — context for the deprecation of `ClientCapabilities.tasks.requests.sampling`.
  - [07-json-schema-2020-12.md](07-json-schema-2020-12.md) — JSON Schema 2020-12 rules that underwrite the Roots replacement story.
  - [08-protocol-evolution.md](08-protocol-evolution.md) — full treatment of SEP-2596 and SEP-2484; the lifecycle policy whose terminology this document inherits.
  - [09-release-timeline.md](09-release-timeline.md) — where the deprecation-window dates and the registry-watch follow-up belong.
- Muster code paths cited in this document:
  [pkg/logging/logging.go](../../../pkg/logging/logging.go),
  [internal/aggregator/logging.go](../../../internal/aggregator/logging.go),
  [internal/aggregator/server.go](../../../internal/aggregator/server.go),
  [internal/aggregator/capability_store.go](../../../internal/aggregator/capability_store.go),
  [internal/aggregator/registry.go](../../../internal/aggregator/registry.go),
  [internal/mcpserver/client_interface.go](../../../internal/mcpserver/client_interface.go),
  [internal/mcpserver/client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go),
  [internal/mcpserver/client_sse.go](../../../internal/mcpserver/client_sse.go),
  [internal/mcpserver/client_stdio.go](../../../internal/mcpserver/client_stdio.go),
  [internal/mcpserver/client_dynamic_auth.go](../../../internal/mcpserver/client_dynamic_auth.go),
  [internal/agent/test_mcp_server.go](../../../internal/agent/test_mcp_server.go),
  [internal/cli/errors.go](../../../internal/cli/errors.go),
  [internal/admin/templates.go](../../../internal/admin/templates.go),
  [cmd/serve.go](../../../cmd/serve.go),
  [internal/testing/muster_manager.go](../../../internal/testing/muster_manager.go),
  [internal/testing/scenarios/](../../../internal/testing).
