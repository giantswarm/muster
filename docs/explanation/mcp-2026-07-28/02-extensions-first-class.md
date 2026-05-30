# Extensions become first-class (MCP 2026-07-28)

The `2026-07-28` release candidate does not just ship MCP Apps and the
Tasks extension; it ships **the framework that allows extensions to
exist as a stable concept at all**. [SEP-2133 — Extensions framework
for MCP](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133)
defines what an MCP extension is, how it is identified, who governs
it, how it is negotiated on the wire, and how it relates to the core
specification's own lifecycle. Without SEP-2133 there is no `extensions`
field on `ClientCapabilities` / `ServerCapabilities` for MCP Apps and
Tasks to advertise themselves through, and no SEP track for new
extensions to follow.

For an aggregator like muster the framework is the bigger of the two
news items: muster has to learn to **forward an opaque map of
extension identifiers** from upstream MCP servers to its inbound
clients (and vice versa) without understanding every entry in it, on
top of choosing which extensions it natively supports.

## 1. What the spec says

### 1.1 The Extensions framework (SEP-2133)

The PR
([SEP-2133 — Extensions framework for MCP](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133))
was opened to port the earlier `#1724` proposal into the new PR-based
SEP format and was merged as `Draft` / `Standards Track`. The
acceptance vote from the core maintainers was 7× yes, 2× yes with
changes, 0× no
([discussion comment from @dsp-ant on 2026-01-26](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133)).

The SEP defines an **MCP extension** as "an optional addition to the
specification that defines capabilities beyond the core protocol";
extensions enable functionality that is "modular (e.g., distinct
features like authentication), specialized (e.g., industry-specific
logic), or experimental (e.g., features being incubated for potential
core inclusion)". The four pieces the SEP nails down are
identification, governance, lifecycle, and on-the-wire negotiation.

**Identification.** Extensions are named with the same reverse-DNS
scheme that `_meta` keys already use, except that the prefix is
**mandatory**: every extension identifier has the form
`{vendor-prefix}/{extension-name}` — for example
`io.modelcontextprotocol/oauth-client-credentials`,
`io.modelcontextprotocol/apps`, or `com.example/websocket-transport`.
The vendor prefix SHOULD be a reversed DNS name that the extension
author owns (Java package naming conventions), and breaking changes
MUST use a new identifier (e.g. `…/oauth-client-credentials-v2`).

**Governance — Official Extensions.** Official extensions live in
the `modelcontextprotocol` GitHub org in repositories prefixed `ext-`
(today: [ext-auth](https://github.com/modelcontextprotocol/ext-auth)
and [ext-apps](https://github.com/modelcontextprotocol/ext-apps)).
Each repository has its own `MAINTAINERS.md`, appointed by the core
maintainers, and "SHOULD have an associated working group or interest
group to guide their development". Official extensions use the
`io.modelcontextprotocol` vendor prefix and MUST be Apache 2.0
licensed. The Linux Foundation receives a perpetual, worldwide,
non-exclusive, no-charge, royalty-free, irrevocable contributor license
grant for every accepted contribution.

**Lifecycle.** New extensions are proposed via a SEP using a new
**Extensions Track** type (it follows the same review process as
Standards Track but is clearly tagged as an extension). Extension
SEPs MUST have at least one reference implementation in an official
SDK before being accepted, SHOULD be discussed in a working group
first, and are reviewed by the core maintainers. Once accepted, the
extension lives in its repository and **iterates without further core
maintainer review**: extension repository maintainers approve changes,
backwards compatibility is the extension's own responsibility, and
breaking changes ship under a new identifier rather than a new
extension-wide version. Promotion to core protocol is optional and
treated as a separate Standards Track SEP. Per the SEP's "SDK
Implementation" section, "SDKs **MAY** implement extensions. Where
implemented, extensions **MUST** be disabled by default and require
explicit opt-in. … Extension support is not required for 100% protocol
conformance or the upcoming SDK conformance tiers."

**Negotiation.** SEP-2133 adds a new `extensions` field to both
`ClientCapabilities` and `ServerCapabilities`, used during the
`initialize` handshake (or, post-SEP-2575 in `2026-07-28`, in every
request's `_meta`). The field is a map from extension identifier to a
per-extension settings object (an empty object indicates "supported,
no settings"):

```json
{
  "capabilities": {
    "roots": {},
    "extensions": {
      "io.modelcontextprotocol/apps": {
        "mimeTypes": ["text/html;profile=mcp-app"]
      }
    }
  }
}
```

The JSON Schema added in `schema/draft/schema.json` is intentionally
permissive — `additionalProperties: true` on each value object — so
that servers and gateways can pass through extension identifiers and
their settings even when they do not natively understand them:

```json
"extensions": {
    "additionalProperties": {
        "additionalProperties": true,
        "properties": {},
        "type": "object"
    },
    "description": "Optional MCP extensions that the client supports. …",
    "type": "object"
}
```

The accompanying lifecycle-spec change in
`docs/specification/draft/basic/lifecycle.mdx` lists `extensions`
alongside `roots`, `sampling`, `elicitation`, `tasks`, and
`experimental` as a standard capability for both clients and servers.
Crucially, **graceful degradation** is normative: "If one party
supports an extension but the other does not, the supporting party
MUST either revert to core protocol behavior or reject the request
with an appropriate error if the extension is mandatory. Extensions
SHOULD document their expected fallback behavior."

Server-side capability checking is illustrated in the SEP with a
TypeScript snippet that simply reads `clientCapabilities?.extensions?.["io.modelcontextprotocol/ui"]?.mimeTypes`
and conditionally registers the UI-enabled tool variant. The intended
implementation pattern is: probe the negotiated map, and either offer
the extension-aware code path or fall back to a core-only one.

**Out of scope (for now).** The SEP explicitly does **not** specify a
schema mechanism for extensions to declare how they modify the wire
schema, dependency declarations between extensions or against core
protocol versions, or extension profiles (grouping extensions). The
rationale ("Start simple, refine later") is to ship the governance
chassis and revisit technical primitives when real extensions surface
the need.

### 1.2 How extensions show up in the 2026-07-28 release

The release-candidate
[announcement](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/),
in its **"Extensions Become First-Class"** section, frames SEP-2133 as
the formalisation of something that was implicit in `2025-11-25`:

> Extensions existed in the `2025-11-25` release but had no formal
> process behind them. SEP-2133 adds that: extensions are identified
> by reverse-DNS IDs, negotiated through an `extensions` map on
> client and server capabilities, live in their own `ext-*`
> repositories with delegated maintainers, and version independently
> of the specification. A new Extensions Track in the SEP process
> gives them a path from experimental to official.

The same section then lists the two extensions that ship with the
2026-07-28 release candidate as direct beneficiaries of the new
framework: **MCP Apps** (server-rendered UIs, advertised as
`io.modelcontextprotocol/apps`, the subject of `03-mcp-apps.md`) and
the **Tasks extension** (long-running, server-directed work units,
the subject of `04-tasks-extension.md`). Both negotiate exactly
through the `extensions` map that SEP-2133 carves out.

The "How the Protocol Evolves From Here" section
([announcement](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/))
positions the framework as a load-bearing piece of the spec's
deprecation strategy: "new capabilities can ship as opt-in extensions
and stabilize there before, if ever, moving into the specification".
Future protocol additions are expected to land as extensions first
and graduate only after they have been validated against real
implementations.

### 1.3 Where SEP-2133 sits in the SEP guidelines

The framework is itself a Standards Track SEP authored under the
existing
[SEP guidelines](https://modelcontextprotocol.io/community/sep-guidelines).
The guidelines define three SEP kinds (Standards Track, Informational,
Process) and require every SEP to ship Abstract, Motivation,
Specification, Rationale, Backward Compatibility, Reference
Implementation, and Security Implications sections. Standards Track
SEPs with observable protocol behavior cannot reach `Final` until a
matching scenario lands in the
[conformance suite](https://github.com/modelcontextprotocol/conformance)
(see `08-protocol-evolution.md` for SEP-2484, which made that
mandatory).

What SEP-2133 adds to the SEP guidelines is the **Extensions Track**
as a fourth marker on Standards Track proposals. Per the SEP: an
extension SEP "follows the same review and acceptance process as
Standards Track SEPs, but clearly indicates that the proposal is for
an extension rather than a core protocol addition. The SEP must
identify the Working Group and Extension Maintainers that will be
responsible for the extension." After acceptance, the day-to-day
versioning and review work moves out of the main repo into the
extension repository — but the conformance and tier-system framing
still flows through the SDK tier docs and SEP-2484's gating rules.

## 2. Linked SEPs and PRs

- [SEP-2133 — Extensions framework for MCP](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133)
  (merged 2026-01-26; status: Final, Standards Track per the SEP index update in the PR)
- [SEP-1850 — PR-Based SEP Workflow](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1850)
  (the workflow SEP-2133 was ported to)
- Announcement, section "Extensions Become First-Class":
  [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
- SEP process: [SEP guidelines](https://modelcontextprotocol.io/community/sep-guidelines)
- Existing official extension repositories (referenced by SEP-2133 as
  precedent for the patterns it describes):
  [ext-apps](https://github.com/modelcontextprotocol/ext-apps),
  [ext-auth](https://github.com/modelcontextprotocol/ext-auth)
- Cross-section context: SEP-2575 (stateless protocol) carries the
  `extensions` map in per-request `_meta` rather than the
  `initialize` handshake — see
  [01-stateless-protocol.md](01-stateless-protocol.md).

## 3. Muster impact

Muster sits between an outbound population of upstream MCP servers
(each of which may, post-2026-07-28, advertise its own extensions
map) and an inbound population of MCP clients (Cursor, Claude Code,
the muster agent's REPL mode, the agent's MCP-server mode for AI
hosts). The Extensions framework therefore lands on muster as a
**protocol-passthrough requirement first**, and an opt-in
"native support" decision second.

### 3.1 Capability negotiation in the aggregator

The aggregator's inbound MCP server is constructed in
[internal/aggregator/server.go](../../../internal/aggregator/server.go)
inside `AggregatorServer.Start`. Today it advertises a fixed set of
capabilities via mcp-go's `ServerOption` API:

```go
opts := []mcpserver.ServerOption{
    mcpserver.WithToolCapabilities(true),           // Enable tool execution
    mcpserver.WithResourceCapabilities(true, true), // Enable resources with subscribe and listChanged
    mcpserver.WithPromptCapabilities(true),         // Enable prompt retrieval
    mcpserver.WithToolFilter(a.sessionToolFilter),  // Return session-specific tools for OAuth servers
    mcpserver.WithHooks(hooks),                     // Clean up subject-session mappings on disconnect
}
opts = append(opts, mcpServerOptions()...)
mcpSrv := mcpserver.NewMCPServer("muster-aggregator", serverVersion, opts...)
```
(see `internal/aggregator/server.go` lines 721–729; cited in
[internal/aggregator/server.go](../../../internal/aggregator/server.go))

There is no `WithExtensions(…)` call here, and there is no inbound
`extensions` map at all. The inbound `initialize` (`AfterInitialize`)
hook only logs the client info, protocol version, and server
version — it does not inspect, store, or echo any extensions block
that the client may send:

```go
hooks.AddAfterInitialize(func(ctx context.Context, _ any, msg *mcp.InitializeRequest, result *mcp.InitializeResult) {
    logging.InfoWithAttrsCtx(ctx, "MCP-Protocol", "Initialize completed",
        slog.String("client", msg.Params.ClientInfo.Name+"/"+msg.Params.ClientInfo.Version),
        slog.String("protocol", string(msg.Params.ProtocolVersion)),
        logging.TransportSessionID(getTransportSessionID(ctx)),
        slog.String("serverVersion", result.ServerInfo.Version))
})
```
([internal/aggregator/server.go](../../../internal/aggregator/server.go),
lines 657–663). The aggregator's own `updateCapabilities()` flow
([internal/aggregator/server.go](../../../internal/aggregator/server.go),
lines 1242–1265) is entirely about **adding/removing meta-tools**
based on what upstreams advertise — it does not touch a
`ServerCapabilities.extensions` field, because there isn't one in the
current mcp-go server build muster compiles against.

For 2026-07-28 this means muster must learn to:

- **Advertise** a non-empty `extensions` map on the inbound side
  whenever the aggregator natively supports an extension (initially,
  realistically, none of them: see §3.3) **and** when it is willing
  to **forward** an upstream extension's surface. Per SEP-2133 the
  values may be opaque settings objects; the aggregator can re-emit
  the upstream's settings verbatim or expose a curated subset.
- **Read** the inbound client's `extensions` capability map and act
  on it. The SEP's "Server-Side Capability Checking" example is the
  intended pattern: the aggregator queries
  `clientCapabilities?.extensions?.[<id>]` before advertising any
  extension-only feature (for example, MCP Apps mime types — see
  `03-mcp-apps.md`).
- **Hook into per-request `_meta` instead of `initialize`.** Once
  SEP-2575 lands (see [01-stateless-protocol.md](01-stateless-protocol.md)),
  the inbound `extensions` map travels in `_meta` on every request,
  not in a one-time handshake. The `AfterInitialize` hook today is
  the only place muster looks at client capabilities and it will
  need to be replaced with per-request `_meta` inspection.

### 3.2 Forwarding extensions through upstream MCP clients

Muster's outbound MCP clients
([internal/mcpserver/client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go),
`client_sse.go`, `client_stdio.go`, `client_dynamic_auth.go`) all
hand the same empty `ClientCapabilities` to the upstream `Initialize`
call:

```go
initResult, err := mcpClient.Initialize(ctx, mcp.InitializeRequest{
    Params: struct {
        ProtocolVersion string                 `json:"protocolVersion"`
        Capabilities    mcp.ClientCapabilities `json:"capabilities"`
        ClientInfo      mcp.Implementation     `json:"clientInfo"`
    }{
        ProtocolVersion: "2024-11-05",
        ClientInfo: mcp.Implementation{
            Name:    "muster-aggregator",
            Version: "1.0.0",
        },
        Capabilities: mcp.ClientCapabilities{},
    },
})
```
([internal/mcpserver/client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go),
lines 91–104; identical patterns at
[client_sse.go](../../../internal/mcpserver/client_sse.go) lines
69–88,
[client_stdio.go](../../../internal/mcpserver/client_stdio.go) lines
75–88,
[client_dynamic_auth.go](../../../internal/mcpserver/client_dynamic_auth.go)
lines 93–106, and the agent's REPL client at
[internal/agent/client.go](../../../internal/agent/client.go) lines
400–417). The `initResult` is consumed only for its `ServerInfo.Name`
and `ServerInfo.Version` — `initResult.Capabilities.Extensions` (or
whatever the upgraded mcp-go field will be called) is dropped on the
floor.

For 2026-07-28 the outbound clients have to:

- **Announce** the extensions the aggregator natively understands
  on the outbound `ClientCapabilities.extensions` map. For an empty
  initial implementation the announcement is "we support
  pass-through only", which is just an empty map (or the union of
  extensions muster has decided to expose to inbound clients — see
  §3.3).
- **Capture** the upstream's advertised
  `ServerCapabilities.extensions` so that the aggregator can decide
  per upstream whether to (a) expose the extension to inbound
  clients verbatim, (b) translate it (the way Tasks ends up
  translating onto muster's workflow engine; see
  `04-tasks-extension.md`), or (c) drop it. Today the upstream's
  capabilities are never persisted past the `Initialize` call — the
  aggregator's `Capabilities` struct
  ([internal/aggregator/capability_store.go](../../../internal/aggregator/capability_store.go),
  lines 11–27) tracks only `Tools`, `Resources`, and `Prompts`. A
  parallel store (or an additional field on the existing
  `ServerInfo` populated by
  [registry.refreshServerCapabilities](../../../internal/aggregator/registry.go)
  at line 1052) is needed to retain the per-upstream extensions map.

### 3.3 Which extensions does muster understand natively?

`internal/api/mcpserver.go` is muster's API-layer description of an
upstream MCP server: `MCPServer` (the persisted definition at
[internal/api/mcpserver.go](../../../internal/api/mcpserver.go),
lines 12–72), the `MCPServerType` enum (lines 245–260, today
`stdio` / `streamable-http` / `sse`), `MCPServerAuth` (lines 95–148),
and the API-response shape `MCPServerInfo` (lines 272–364). The
runtime view that the aggregator gives to API consumers is
deliberately about **process / connection state** — `State`,
`StatusMessage`, `ConsecutiveFailures`, `SessionStatus`,
`SessionAuth`, `ToolsCount` — and **not** about which MCP-spec
extensions an upstream advertises. There is no `Extensions` field on
either `MCPServer` or `MCPServerInfo`.

For 2026-07-28, muster needs a deliberate decision on which
extensions enter that API shape, and which are merely transparent
passthrough. Initial recommendation (full justification belongs in
`03-mcp-apps.md` and `04-tasks-extension.md`):

- **MCP Apps (`io.modelcontextprotocol/apps`).** Pass through the
  upstream `extensions` entry to inbound clients (so a Claude Code
  or a custom host that natively renders MCP Apps can do so via
  muster). Muster's own REPL agent in
  [internal/agent/](../../../internal/agent) does not render
  HTML/iframe UIs and is unlikely to acquire that capability; it
  reverts to core protocol behavior per SEP-2133's graceful-
  degradation rule.
- **Tasks extension (`io.modelcontextprotocol/tasks`, or whatever
  identifier the extension repo finalises).** Native support is
  attractive because muster's workflow engine
  ([internal/workflow/](../../../internal/workflow)) is the
  natural home for long-running, server-directed work. Whether
  muster advertises support, only forwards it, or wraps its
  `action_<workflow-name>` machinery in the Tasks extension is the
  central question of `04-tasks-extension.md`.
- **`ext-auth` extensions
  (`io.modelcontextprotocol/oauth-client-credentials`,
  `io.modelcontextprotocol/enterprise-managed-authorization`).**
  These live close to the OAuth code in
  [pkg/oauth/](../../../pkg/oauth) and
  [internal/oauth/](../../../internal/oauth) and are evaluated in
  `05-authorization-hardening.md`. Muster already speaks OAuth
  end-to-end; native advertisement is plausible once the underlying
  ext-auth specs stabilise.
- **Unknown / future extensions.** Forward the upstream's
  `extensions` entry verbatim to inbound clients that share the
  same authenticated principal, and rely on the SEP-2133
  graceful-degradation rule on both ends. Never re-emit an
  extension on the inbound side that no upstream advertises and
  that muster does not implement itself — that would lie about a
  capability the aggregator can actually fulfil.

In all cases, **muster must keep the `extensions` map opaque** where
it does not understand the value — per SEP-2133's permissive
JSON-Schema definition (`additionalProperties: true`), the contents
of any per-extension settings object are extension-defined and a
gateway has no business reshaping them.

### 3.4 SDK / mcp-go dependency

Muster does not own the wire-level types: `mcp.ClientCapabilities`
and `mcp.ServerCapabilities` come from
[mark3labs/mcp-go](https://github.com/mark3labs/mcp-go). Until
mcp-go ships the SEP-2133 `extensions` field on those structs, muster
cannot natively read or emit the map at all; the work in §3.1 and
§3.2 is **gated on the underlying SDK** the same way the broader
stateless rework is. mcp-go is a Tier-2-style community SDK rather
than an official tier-1 SDK (see `08-protocol-evolution.md` for SDK
tiering), so muster may need to drive or land that change upstream
itself.

## 4. Required changes / migration notes

The items below are meant to be turned into separate GitHub issues
once the analysis documents in this folder have all landed. They are
ordered so that an earlier item is a prerequisite for a later one.

1. **Track SEP-2133 support in `mark3labs/mcp-go`.** Confirm whether
   the upstream SDK exposes `Extensions` on `ClientCapabilities` and
   `ServerCapabilities`, in which release, and what its passthrough
   defaults are. The rest of this list is gated on a usable SDK
   version.
2. **Persist upstream `extensions` per server.** Extend either the
   `ServerInfo` struct touched by
   [refreshServerCapabilities](../../../internal/aggregator/registry.go)
   (line 1052) or add a sibling to
   [Capabilities](../../../internal/aggregator/capability_store.go)
   (lines 11–27) so that the aggregator retains the upstream's
   `ServerCapabilities.extensions` map verbatim alongside the
   existing tools / resources / prompts. Today only the latter three
   are stored.
3. **Read inbound `extensions` from per-request `_meta`.** Once the
   stateless transport from `01-stateless-protocol.md` lands, replace
   the one-shot `AfterInitialize` hook in
   [internal/aggregator/server.go](../../../internal/aggregator/server.go)
   (lines 657–663) with a per-request inspection that surfaces the
   client's `extensions` map to the meta-tool routing layer. The
   "Server-Side Capability Checking" pattern from SEP-2133 is the
   reference implementation.
4. **Compute the inbound `extensions` advertisement.** Build a
   function that, for the current authenticated principal, takes the
   union of (a) extensions muster natively implements and (b)
   extensions advertised by upstream servers the principal has
   access to, and emits that as the inbound `ServerCapabilities.extensions`
   map. Wire it next to the existing
   `mcpserver.WithToolCapabilities` / `WithResourceCapabilities` /
   `WithPromptCapabilities` calls in
   [internal/aggregator/server.go](../../../internal/aggregator/server.go)
   (lines 721–729) when mcp-go grows a `WithExtensions` equivalent.
5. **Forward `extensions` on outbound `Initialize` / per-request
   `_meta`.** Replace the empty `mcp.ClientCapabilities{}` literal
   in
   [client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go)
   (lines 91–104),
   [client_sse.go](../../../internal/mcpserver/client_sse.go)
   (lines 69–88),
   [client_stdio.go](../../../internal/mcpserver/client_stdio.go)
   (lines 75–88),
   [client_dynamic_auth.go](../../../internal/mcpserver/client_dynamic_auth.go)
   (lines 93–106), and the agent client
   ([internal/agent/client.go](../../../internal/agent/client.go),
   lines 400–417) with the inbound caller's extensions map (or a
   filtered subset). On a 2026-07-28 transport the same map travels
   in `_meta` on each request, not in `Initialize`.
6. **Decide muster's native-support matrix.** Produce an ADR (likely
   in [docs/explanation/decisions/](../decisions)) listing every
   extension muster intends to natively understand, every extension
   it merely forwards, and the rationale. Initial candidates:
   `io.modelcontextprotocol/apps` (forward), `…/tasks` (native, see
   `04-tasks-extension.md`), and the `ext-auth` family (native, see
   `05-authorization-hardening.md`).
7. **Surface extensions in the muster API.** Once §2 stores the
   upstream extensions map, add an `Extensions` field to
   `MCPServerInfo`
   ([internal/api/mcpserver.go](../../../internal/api/mcpserver.go),
   lines 272–364) so that `muster get mcpserver <name>` and the
   admin UI can show which extensions a given upstream advertises.
   The `MCPServer` persisted definition (lines 12–72) does not need
   an `Extensions` field — extensions are negotiated, not
   configured — but a future "deny-list of extensions to not forward"
   may belong there.
8. **Document the graceful-degradation contract.** When an inbound
   client requests an extension muster cannot fulfil (because no
   upstream advertises it and muster does not implement it natively),
   muster MUST follow the SEP-2133 rule: revert to core protocol
   behavior, or reject the request with a clear error if the client
   marks the extension mandatory. Make this explicit in
   [docs/explanation/mcp-aggregation.md](../mcp-aggregation.md).
9. **Plan for the Extensions Track in muster's own conformance
   strategy.** `08-protocol-evolution.md` covers the conformance
   suite generally, but the extensions-track-specific implication
   is that muster's BDD scenarios in
   [internal/testing/scenarios/](../../../internal/testing) need
   per-extension scenarios for the extensions muster claims to
   support natively. Plain passthrough does not need its own
   conformance scenario beyond "the aggregator forwards an unknown
   `extensions` entry unchanged".

## 5. Open questions

- **Per-principal vs per-deployment advertisement.** SEP-2567 (see
  [01-stateless-protocol.md](01-stateless-protocol.md)) forbids
  per-session variation of `tools/list` / `resources/list` /
  `prompts/list` results, but does **not** explicitly address
  `ServerCapabilities.extensions`. In a multi-tenant aggregator, is
  it acceptable for the advertised `extensions` map to vary by
  authenticated principal (because principal A has access to an
  upstream that advertises MCP Apps but principal B does not)? The
  SEP's permissive "values may vary by authorization presented on
  the request" phrasing suggests yes, but this needs an explicit
  call.
- **Settings objects: union vs intersection.** When two upstreams
  both advertise the same extension identifier with different
  settings (for example, MCP Apps with different `mimeTypes` lists),
  what does muster expose to inbound clients? The intersection is
  the only safe answer for "things both upstreams can handle", but
  inbound clients may need the union to discover all renderable
  MIME types. Tracking which upstream supports which value matters
  for routing.
- **Native vs forwarded for `ext-auth`.** The OAuth extensions in
  [ext-auth](https://github.com/modelcontextprotocol/ext-auth)
  describe protocol-level authorization mechanics that the
  aggregator already performs end-to-end on behalf of the inbound
  caller. Does muster advertise the ext-auth extensions itself
  (because it implements them upstream-of-muster), or does it
  forward whatever the underlying upstream advertises? The answer
  depends on whether ext-auth extensions are meant to be visible to
  end clients at all, and is the subject of `05-authorization-hardening.md`.
- **Mandatory extensions on the inbound side.** SEP-2133 lets an
  extension be mandatory — "MAY reject the request with an
  appropriate error if the extension is mandatory". Should muster
  support inbound clients declaring an extension as mandatory? If
  so, where does the rejection happen — on the inbound HTTP layer,
  or after the first tool call that would need the extension?
- **Extension-aware caching.** SEP-2549 `cacheScope: "public"`
  responses are share-able across principals; do they also share
  across `extensions` advertisements, or does a different inbound
  `extensions` map invalidate the cache? Most likely "no" (the
  result shape does not depend on the extensions map), but worth
  confirming as `ttlMs` / `cacheScope` land (see
  [01-stateless-protocol.md](01-stateless-protocol.md) §3.4).
- **mcp-go SDK timeline.** Native support hinges on
  [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) shipping
  the `extensions` field on its capability structs and on the
  per-request `_meta` plumbing from SEP-2575. If upstream mcp-go is
  late, muster may have to either fork or speak the field via a
  side-channel marshaller. Either way it's a planning constraint,
  not a blocker on writing the analysis.

## 6. References

- [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/) — primary announcement, section "Extensions Become First-Class"
- [SEP-2133 — Extensions framework for MCP](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133) — PR description, diff, and discussion (including the unanimous-yes acceptance vote summarised in @dsp-ant's 2026-01-26 comment)
- [SEP-1850 — PR-Based SEP Workflow](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1850) — workflow that SEP-2133 was ported into
- [SEP guidelines](https://modelcontextprotocol.io/community/sep-guidelines) — SEP types, statuses, format, sponsor and conformance requirements
- [SEP-2484 — Conformance tests required for Final SEPs](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2484) — Standards-Track conformance gate referenced from the SEP guidelines
- [ext-apps repository](https://github.com/modelcontextprotocol/ext-apps) — official MCP Apps extension home (cited by SEP-2133 as precedent)
- [ext-auth repository](https://github.com/modelcontextprotocol/ext-auth) — official authorization extensions home (cited by SEP-2133 as precedent)
- [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) — the SDK whose capability structs muster depends on
- Cross-section context inside this folder: [01-stateless-protocol.md](01-stateless-protocol.md) (per-request `_meta` carries the `extensions` map post-SEP-2575)
