# MCP Apps (MCP 2026-07-28)

MCP Apps is the first official MCP extension shipped under the
SEP-2133 Extensions framework
([SEP-1865 — MCP Apps: Interactive User Interfaces for MCP](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1865),
merged final on 2026-01-26 with the
[ext-apps](https://github.com/modelcontextprotocol/ext-apps)
repository as its long-term home). It standardises how an MCP server
declares an **interactive UI template** that a host renders in a
sandboxed iframe when the user invokes a tool. The host fetches the
template via the existing `resources/read` flow, the tool result
becomes the data the UI consumes, and the iframe talks back to the
host over JSON-RPC-over-`postMessage` — the same MCP base protocol
everything else in the spec uses.

For an aggregator like muster, MCP Apps does **not** require the
aggregator to render anything. What it does require is that the
aggregator preserves the `_meta.ui.*` shape end-to-end on `tools/list`,
`resources/list`, and `resources/read`, advertises the
`io.modelcontextprotocol/ui` extension capability when at least one
upstream supports it, and audits any place where prefetched UI
templates might be cached server-side. The blast radius is narrower
than the stateless rework
([01-stateless-protocol.md](01-stateless-protocol.md)) but it
intersects directly with muster's meta-tool architecture, which
currently strips tool and resource `_meta` on its way out to inbound
clients.

## 1. What the spec says

### 1.1 The extension and its shape

SEP-1865's
[PR description](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1865)
labels MCP Apps **Extensions Track / Final**, points to the
[ext-apps](https://github.com/modelcontextprotocol/ext-apps) repo as
the canonical home, and summarises the proposal as introducing four
things on top of core MCP:

- a `ui://` URI scheme for **pre-declared** UI resources;
- a `_meta.ui.resourceUri` link on tools that associates a tool with
  the UI template that should render its results;
- bi-directional JSON-RPC communication between the iframe and the
  host, using a `postMessage` transport and a `ui/initialize` /
  `ui/notifications/initialized` handshake plus a set of `ui/*`
  methods (open links, download files, request display-mode change,
  update model context, …);
- a security model built on iframe sandboxing, declared CSP, and
  auditable JSON-RPC messages.

The
[full ext-apps draft](https://raw.githubusercontent.com/modelcontextprotocol/ext-apps/main/specification/draft/apps.mdx)
defines the extension identifier as
**`io.modelcontextprotocol/ui`** and the canonical MIME type as
`text/html;profile=mcp-app`. The capability is negotiated through
the SEP-2133 `extensions` map (see
[02-extensions-first-class.md](02-extensions-first-class.md)):

```json
{
  "method": "initialize",
  "params": {
    "capabilities": {
      "extensions": {
        "io.modelcontextprotocol/ui": {
          "mimeTypes": ["text/html;profile=mcp-app"]
        }
      }
    },
    "clientInfo": { "name": "claude-desktop", "version": "1.0.0" }
  }
}
```

Servers SHOULD check `clientCapabilities.extensions["io.modelcontextprotocol/ui"]`
before registering UI-enabled tools and provide a text-only fallback
when the capability is missing; tools MUST always return a meaningful
`content` array even when the UI variant is available, so that
non-supporting hosts degrade gracefully (the SEP's "Server Behavior"
and "Graceful Degradation" sections in the
[full spec](https://raw.githubusercontent.com/modelcontextprotocol/ext-apps/main/specification/draft/apps.mdx)).

### 1.2 UI resources, the `ui://` scheme, and `_meta.ui`

A UI resource is an ordinary MCP resource whose URI starts with
`ui://`, whose `mimeType` is `text/html;profile=mcp-app`, and whose
`_meta.ui` field carries the host-facing rendering metadata (CSP,
permissions, dedicated sandbox origin, border preference):

```json
{
  "uri": "ui://weather-server/dashboard-template",
  "name": "weather_dashboard",
  "mimeType": "text/html;profile=mcp-app",
  "_meta": {
    "ui": {
      "csp": {
        "connectDomains": ["https://api.openweathermap.org"],
        "resourceDomains": ["https://cdn.jsdelivr.net"]
      },
      "permissions": { "geolocation": {} },
      "prefersBorder": true
    }
  }
}
```

`_meta.ui` may appear on both the `resources/list` entry (a static
default that hosts can review at connection time) and on each
`resources/read` content item (per-response, dynamic overrides). When
both are present the content-item value wins; hosts MUST check both
locations. Servers MAY omit UI-only resources from `resources/list`
entirely and rely on `_meta.ui.resourceUri` on tool definitions to
guide discovery (the SEP's "Behavior" subsection).

The corresponding link on a tool is also under `_meta.ui`:

```json
{
  "name": "get_weather",
  "description": "Get current weather for a location",
  "inputSchema": { "type": "object", "properties": { "location": { "type": "string" } } },
  "_meta": {
    "ui": {
      "resourceUri": "ui://weather-server/dashboard-template",
      "visibility": ["model", "app"]
    }
  }
}
```

`visibility` defaults to `["model", "app"]`; tools with
`visibility: ["app"]` MUST NOT appear in the agent's `tools/list` but
remain callable by the app over the iframe's JSON-RPC channel
(refresh buttons, internal form submissions, etc.). The legacy flat
`_meta["ui/resourceUri"]` form is deprecated and slated for removal
before the extension's GA, per the spec's "Deprecation notice".

When the host renders a tool's UI, it MUST fetch the template via
`resources/read` and MUST construct the iframe CSP from the declared
`connectDomains` / `resourceDomains` / `frameDomains` / `baseUriDomains`
arrays, falling back to a restrictive default (`default-src 'none';
script-src 'self' 'unsafe-inline'; …`) when `ui.csp` is omitted.
Permissions (`camera`, `microphone`, `geolocation`, `clipboardWrite`)
map to the iframe's `allow` attribute and are advertised back through
the `hostCapabilities.sandbox` map.

### 1.3 Bi-directional protocol and lifecycle

Conceptually the iframe acts as an MCP client and the host as an MCP
server (which may proxy to the actual upstream MCP server). The
transport is `postMessage` carrying JSON-RPC 2.0 frames. The
lifecycle the spec defines is:

1. **Discovery.** `resources/list` includes the `ui://` entries,
   `tools/list` includes tools whose `_meta.ui.resourceUri` points at
   one of them.
2. **Initialization.** The host fires off `tools/call` to the server
   in parallel with creating the iframe (or, on web hosts, the
   double-iframe sandbox described in "Sandbox proxy": the outer
   sandbox sends `ui/notifications/sandbox-proxy-ready`, the host
   replies with `ui/notifications/sandbox-resource-ready` carrying
   the HTML and the CSP/permissions). The View sends `ui/initialize`
   declaring `appCapabilities` (notably `tools` and
   `availableDisplayModes`); the host replies with
   `McpUiInitializeResult` containing `hostCapabilities`
   (`serverTools`, `serverResources`, `sampling`, `openLinks`,
   `downloadFile`, `logging`, …) and a `hostContext`
   (theme, CSS variables, display mode, container dimensions, locale,
   timezone, safe-area insets, platform). The View follows with
   `ui/notifications/initialized`. The host then pushes
   `ui/notifications/tool-input(-partial)` and, when the tool call
   completes, `ui/notifications/tool-result`.
3. **Interactive phase.** The View calls `tools/call`, `tools/list`,
   `resources/read`, and `sampling/createMessage` against the host;
   the host proxies tool / resource calls to the upstream MCP server.
   The View can also emit `ui/open-link`, `ui/download-file`,
   `ui/message`, `ui/update-model-context`, and
   `ui/request-display-mode` requests, and the host can push
   `ui/notifications/host-context-changed` notifications when theme
   or display mode change.
4. **Teardown.** The View MAY request teardown via
   `ui/notifications/request-teardown`; in any case the host MUST
   send `ui/resource-teardown` and SHOULD await the View's response
   before tearing the iframe down.

In addition to consuming upstream tools, apps MAY **register their
own** tools via the `tools` app capability — these are ephemeral,
tied to the iframe's lifetime, and MUST be torn down with the
iframe. The spec's "App-Provided Tools" section is explicit that
hosts MUST NOT persist app tool registrations across sessions, MUST
return an error for `tools/call` against a closed app, and SHOULD
isolate them in their own namespace (with `readOnlyHint` driving
auto-approval policy).

### 1.4 Security model

The "Security Implications" section names five mitigation layers that
hosts MUST or SHOULD implement:

- **Iframe sandboxing** — sandboxed iframe with restricted
  permissions, no host DOM access; all communication is via
  `postMessage` under host control. Web hosts MUST use a separate
  same-origin-different-from-host sandbox proxy (`allow-scripts`,
  `allow-same-origin`) that re-renders the View's HTML with the
  CSP/permissions declared in `_meta.ui`.
- **Auditable communication** — every UI-to-host message is
  JSON-RPC, every host SHOULD validate and log incoming RPCs.
- **Pre-declared resource review** — because `ui://` templates are
  enumerable via `resources/list` (or pointed at by
  `_meta.ui.resourceUri`), hosts can hash, screenshot, allowlist, or
  warn on them before any tool call runs.
- **CSP enforcement** — hosts MUST construct the iframe CSP from
  declared domains; they MAY restrict further but MUST NOT loosen.
- **App-provided tool approval** — clear attribution ("Tool from
  TicTacToe App"), per-app/per-server/per-tool approval policy,
  resource limits (recommended: ≤50 tools per app, ≤30 s execution,
  ≤10 MB results, throttled `notifications/tools/list_changed`),
  audit trails, and result-schema validation.

The threat model treats the upstream MCP server as **potentially
untrusted**: a malicious server can ship HTML that tries to
exfiltrate data, phish the user, or escape the sandbox. The whole
security stance therefore lives in the host; an aggregator that
forwards UI templates without preserving the declared CSP /
permissions / pre-declared shape would silently degrade host-side
mitigations.

### 1.5 How the 2026-07-28 announcement frames it

The release-candidate
[announcement](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/),
in its **"MCP Apps: server-rendered user interfaces"** section,
describes the extension as letting "servers ship interactive HTML
interfaces that hosts render in a sandboxed iframe", with two design
properties called out explicitly:

- "Tools declare their UI templates ahead of time so hosts can
  **prefetch, cache, and security-review** them before anything
  runs." — that is the cacheability story (and it lines up
  cleanly with SEP-2549 `ttlMs` / `cacheScope` from
  [01-stateless-protocol.md](01-stateless-protocol.md): a UI
  template is a `resources/read` result and can be cached with the
  same TTL/scope rules as any other resource).
- "The rendered UI talks back to the host over the same JSON-RPC
  base protocol used everywhere else in MCP, so every UI-initiated
  action goes through **the same audit and consent path** as a
  direct tool call." — i.e. there is no parallel protocol; whatever
  audit/consent the host enforces for `tools/call` it also enforces
  for app-initiated `tools/call`.

The preview-stage
[MCP Apps blog (Jan 2026)](https://blog.modelcontextprotocol.io/posts/2026-01-26-mcp-apps/)
adds two pieces of context that matter for muster planning:

- The reference SDK is JavaScript / TypeScript
  ([@modelcontextprotocol/ext-apps](https://www.npmjs.com/package/@modelcontextprotocol/ext-apps)),
  and adoption at launch is concentrated on visual hosts (Claude
  Web, Claude Desktop, Goose, VS Code Insiders, ChatGPT). There is
  no Go host SDK and no expectation that headless / CLI hosts
  render MCP Apps natively.
- Migration from MCP-UI (the community precursor) is explicitly
  framed as straightforward at the SDK level but does change wire
  shapes (the `_meta["ui/resourceUri"]` deprecation noted above
  comes from that history). MCP servers in the wild today may be
  emitting either shape during the transition.

## 2. Linked SEPs and PRs

- [SEP-1865 — MCP Apps: Interactive User Interfaces for MCP](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1865)
  (Final, Extensions Track, merged 2026-01-26)
- [modelcontextprotocol/ext-apps — full draft specification](https://raw.githubusercontent.com/modelcontextprotocol/ext-apps/main/specification/draft/apps.mdx)
- [SEP-2133 — Extensions framework for MCP](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133)
  (the negotiation framework MCP Apps rides on top of; covered in
  [02-extensions-first-class.md](02-extensions-first-class.md))
- Announcement, section "MCP Apps: server-rendered user interfaces":
  [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
- Preview blog: [MCP Apps - Bringing UI Capabilities To MCP Clients (Jan 2026)](https://blog.modelcontextprotocol.io/posts/2026-01-26-mcp-apps/)
- Reference SDK and examples: [ext-apps](https://github.com/modelcontextprotocol/ext-apps)

## 3. Muster impact

Muster does not render iframes — it is a control plane and an MCP
aggregator. The question is therefore not "should muster implement an
MCP Apps host?" (the answer is no) but "what does a UI-aware
upstream MCP server need from muster so that a UI-aware inbound
client can render its apps?". The answer is: muster must (a) preserve
the `_meta.ui` shape on tool and resource definitions and on resource
contents end-to-end, (b) advertise / forward the
`io.modelcontextprotocol/ui` extension capability in the negotiated
`extensions` map (per
[02-extensions-first-class.md](02-extensions-first-class.md)), and
(c) not introduce a server-side UI template cache that the spec's
security model is not designed for.

### 3.1 The meta-tool wrapper strips `_meta.ui` today

The structural blocker is the server-side meta-tools migration. As
documented at the top of
[internal/aggregator/tool_factory.go](../../../internal/aggregator/tool_factory.go)
(lines 17–32):

> As of the server-side meta-tools migration (Issue #343), this
> method ONLY exposes meta-tools (list_tools, call_tool,
> describe_tool, etc.) to MCP clients. All other tools (workflow,
> service, config, etc.) are accessed ONLY through the call_tool
> meta-tool, which delegates to callCoreToolDirectly() for execution.

Inbound MCP clients (Cursor, Claude Code, the agent's MCP-server
mode, any future MCP Apps-aware host) therefore never see real
upstream tools directly. They see `list_tools` / `describe_tool` /
`call_tool` and have to discover everything through those. The
formatters that back those meta-tools
([internal/metatools/formatters.go](../../../internal/metatools/formatters.go))
serialise tool entries through a fixed shape:

```go
// FormatToolDetailJSON
toolInfo := map[string]interface{}{
    api.FieldName:            tool.Name,
    api.SchemaKeyDescription: tool.Description,
    api.FieldInputSchema:     tool.InputSchema,
}
```

(lines 236–249) and `FormatResourcesListJSON` / `FormatResourceDetailJSON`
do the same with a fixed `ResourceInfo` struct (lines 146–177, 251 +
following). Neither pulls `_meta` off the source `mcp.Tool` /
`mcp.Resource`. Concretely:

- A UI-aware host calling `list_tools` against muster receives a
  JSON document with no `_meta.ui.resourceUri` field on any tool,
  even if every upstream tool declares one. The host has no signal
  that a tool should be rendered as an app.
- The same host calling `describe_resource` against muster on a
  `ui://…` URI receives `{ uri, name, description, mimeType }` only.
  `_meta.ui.csp`, `_meta.ui.permissions`, `_meta.ui.domain`, and
  `_meta.ui.prefersBorder` are all lost. The host has no way to
  honour the SEP's CSP-construction rules.

The `get_resource` meta-tool
([internal/metatools/handlers.go](../../../internal/metatools/handlers.go),
lines 366 + following) is closer to passing through because it
forwards to `handler.GetResource(ctx, uri)` and is meant to return
the underlying `ReadResourceResult`. The aggregator's `ReadResource`
([internal/aggregator/server.go](../../../internal/aggregator/server.go),
lines 2994–3016) resolves the exposed URI back to the upstream
server, calls `client.ReadResource(ctx, originalURI)`, and returns
the result verbatim. Whether `ResourceContents._meta` survives that
chain depends on whether `mcp-go`'s `ReadResourceResult` shape carries
arbitrary `_meta`. That needs to be confirmed against the version of
mcp-go muster is on (see §3.5) before any further work; if it does
not, the same fix applies as for tools/resources.

The aggregator-side surface that builds the `mcp.Resource` cache is
also schema-bound:

```go
// internal/aggregator/capability_store.go
type Capabilities struct {
    Tools     []mcp.Tool
    Resources []mcp.Resource
    Prompts   []mcp.Prompt
}
```

(lines 11–16). `mcp.Resource` is the mcp-go type; if it does not
include a `Meta` / `_meta` field, the aggregator drops UI metadata
before the meta-tool layer even gets to consider it. SEP-1865 puts
metadata on **both** the listing entry and the content item; muster
needs both halves to reach the inbound client unchanged.

### 3.2 Extension advertisement on the inbound side

Per the extensions framework
([02-extensions-first-class.md](02-extensions-first-class.md) §3.1),
the inbound MCP server muster runs in
[internal/aggregator/server.go](../../../internal/aggregator/server.go)
will need to start emitting an `extensions` map on its
`ServerCapabilities`. For MCP Apps the contract is:

- If at least one currently-reachable upstream server (for the
  authenticated principal) advertises
  `io.modelcontextprotocol/ui` in its own `ServerCapabilities.extensions`,
  muster SHOULD re-advertise it inbound, with the set of MIME types
  computed from the upstreams' `mimeTypes` lists (the only required
  setting field today; future settings — `features`,
  `sandboxPolicies` — are explicitly called out as "may be added"
  in the SEP and need a similar passthrough strategy).
- The advertisement is conditional on §3.1 actually being fixed.
  Advertising `io.modelcontextprotocol/ui` while the meta-tool
  layer strips `_meta.ui` would mislead UI-aware hosts into
  expecting renderable tools that have no `resourceUri` they can
  follow.
- Inbound `clientCapabilities.extensions` from request `_meta`
  (post-SEP-2575) becomes the input to the SEP's
  "Server-Side Capability Checking" pattern. Muster can use it to
  decide which upstreams' UI-enabled tool variants to expose vs.
  fall back to text-only (where the upstream offers both).

### 3.3 Forwarding `ui/*` traffic — out of scope for the aggregator

A subtle but important point: the SEP's `ui/initialize`,
`ui/notifications/tool-input`, `ui/notifications/tool-result`,
`ui/open-link`, `ui/download-file`, `ui/message`,
`ui/update-model-context`, `ui/request-display-mode`,
`ui/notifications/host-context-changed`, and the rest of the
`ui/*` namespace are exchanged **between the iframe (View) and the
host**, not between the host and the MCP server. They never reach
the aggregator at all. What the aggregator forwards is the standard
core surface: `resources/list`, `resources/read`, `tools/list`,
`tools/call`, `notifications/resources/list_changed`,
`notifications/tools/list_changed`, and (per
[01-stateless-protocol.md](01-stateless-protocol.md)) the SEP-2549
TTL/scope hints that come with each list/read.

This is a useful negative result for muster:

- The aggregator does **not** need a `postMessage` transport, an
  iframe sandbox, a CSP enforcer, or any of the `hostContext` /
  `hostCapabilities` machinery from the spec. Those belong in the
  host (Claude Code, Cursor, a hypothetical muster-as-host).
- App-provided tools (the View registering its own tools via the
  `tools` app capability) are similarly invisible to muster — they
  live entirely in the host-iframe pair and are torn down with the
  iframe. Even if a future muster-frontend wanted to participate,
  the SEP's lifecycle rules ("apps MUST NOT persist app tool
  registrations across sessions, calling a tool from a closed app
  MUST return an error") rule out the multi-pod, shared-store
  caching shapes muster uses for upstream tools.

### 3.4 The agent (REPL and MCP-server mode) is text-only

[internal/agent/](../../../internal/agent) ships three modes
(REPL, monitoring, MCP-server-for-AI-assistant; see the architecture
overview in
[internal/agent/doc.go](../../../internal/agent/doc.go) lines
26–46). None of them is an MCP Apps host:

- **REPL mode** (`internal/agent/repl.go`) prints tool, resource,
  and prompt names as text and reacts to
  `notifications/tools/list_changed`,
  `notifications/resources/list_changed`,
  `notifications/prompts/list_changed` by refreshing tab completion
  (lines 681–692). Rendering an `ui://` resource through the REPL
  cannot meaningfully fire a sandboxed iframe; the resource would
  be displayed as HTML text or omitted.
- **Agent MCP-server mode**
  ([internal/agent/server_upgrade.go](../../../internal/agent/server_upgrade.go)
  lines 27–112) re-exposes the aggregator's meta-tools — including
  `list_resources`, `describe_resource`, `get_resource` — to an
  upstream AI assistant via stdio. Whatever the AI assistant ends
  up with as a "host" inherits the meta-tool-shaped surface
  described in §3.1; no UI rendering happens in muster.
- **Client cache** (`internal/agent/client.go` lines 60–101,
  1020–1092, 1454–1502) keeps `toolCache` / `resourceCache` /
  `promptCache` lists for completion and freshness comparison and
  calls `resources/read` directly through mcp-go's `ReadResource`.
  The cache is keyed by the resource URI and has no special
  handling for `ui://` URIs; SEP-1865's rule that
  "Servers MAY omit UI-only resources from `resources/list`"
  means the cache may simply not see UI templates that the upstream
  considers tool-discoverable only.

The right framing in the muster impact docs is **SEP-2133's
"graceful degradation" rule applied to muster's own host surfaces**:
when the REPL or agent mode talks to a UI-aware upstream, it MUST
revert to core-protocol behaviour (text-only output), and it MUST
NOT pretend to support `io.modelcontextprotocol/ui` on the inbound
side. The aggregator may still forward the extension to other
inbound clients that do support it.

### 3.5 Caching: a security review item, not a feature

The 2026-07-28 announcement's framing of MCP Apps emphasises
prefetch caching: "Tools declare their UI templates ahead of time so
hosts can prefetch, cache, and security-review them before anything
runs." That sentence is about **the host**, not the aggregator.
Three concrete caching surfaces in muster need a deliberate decision:

- **Per-`(sessionID, serverName)` capability store**
  ([internal/aggregator/capability_store.go](../../../internal/aggregator/capability_store.go)
  and `capability_store_valkey.go`) — already covered in
  [01-stateless-protocol.md](01-stateless-protocol.md) §3.4 for
  `ttlMs` / `cacheScope`. UI resources are ordinary resources; their
  TTL and cache scope come from the upstream's per-response
  `ttlMs` / `cacheScope`. SEP-1865's separation of presentation
  (template, mostly static) from data (tool result, dynamic) lines
  up naturally with caching the template entry with a generous
  `ttlMs` and refreshing it on
  `notifications/resources/list_changed`.
- **Resource-content prefetch.** Muster does **not** prefetch
  `resources/read` results today; the aggregator's
  [AggregatorServer.ReadResource](../../../internal/aggregator/server.go)
  (lines 2994–3016) is a synchronous passthrough that resolves the
  exposed URI back to its origin server and forwards the read.
  SEP-1865 specifically anticipates host-side prefetching of UI
  templates; introducing aggregator-side prefetching of `ui://`
  resources would (a) re-introduce a long-lived per-template cache
  that SEP-2549 `ttlMs` / `cacheScope` was meant to constrain, and
  (b) put the aggregator in possession of untrusted HTML that no
  part of the SEP's threat model is designed for. The cleaner
  position is: **muster does not prefetch UI templates**; if any
  caching of `ui://` reads is desired in the future, it must obey
  the same `ttlMs` / `cacheScope` rules as any other
  `resources/read` and must keep the upstream's `_meta.ui` intact
  on every cached entry.
- **Agent client cache** (`internal/agent/client.go`) — described
  in §3.4. Today this is a list cache (URIs, names, mimeTypes), not
  a content cache. The same "no prefetched HTML" rule applies.

In short, MCP Apps does not change muster's caching contract; it
forces the caching code to **continue not prefetching UI content**
even though SEP-1865 unlocks an obvious prefetch optimisation for
hosts.

### 3.6 SDK / mcp-go dependency

Two pieces of `mark3labs/mcp-go` matter for this section:

- Whether `mcp.Tool`, `mcp.Resource`, and `mcp.ReadResourceResult`
  (and the per-content `ResourceContents` types in mcp-go) carry a
  pass-through `Meta` / `_meta` field. If they do, the fix to
  §3.1's stripping behaviour is to update the metatools formatters
  to emit it; if they do not, the fix has to land in mcp-go first.
- Whether `mcp.ClientCapabilities` / `mcp.ServerCapabilities` carry
  the SEP-2133 `extensions` map (the same gating issue covered in
  [02-extensions-first-class.md](02-extensions-first-class.md)
  §3.4). MCP Apps advertisement is downstream of that field
  existing.

mcp-go is community-maintained (Tier-2-style; see
[08-protocol-evolution.md](08-protocol-evolution.md) for SDK
tiering), so muster may need to drive both items upstream itself.

## 4. Required changes / migration notes

The items below are intended to become separate GitHub issues once
the analysis documents in this folder have all landed. They are
ordered so an earlier item is a prerequisite for a later one.

1. **Audit mcp-go's `_meta` carriage.** Confirm whether
   `mcp.Tool`, `mcp.Resource`, `mcp.ReadResourceResult`, and the
   underlying `ResourceContents` variants carry an arbitrary
   `_meta` (or `Meta` / `MetaRaw`) field that survives JSON
   marshal/unmarshal in both directions. Everything below depends
   on the answer.
2. **Preserve `_meta` in the metatools formatters.** Update
   [internal/metatools/formatters.go](../../../internal/metatools/formatters.go)
   so that `FormatToolListJSON`, `FormatToolDetailJSON`,
   `FormatResourcesListJSON`, and `FormatResourceDetailJSON` emit
   the source `_meta` verbatim (at minimum the `ui` sub-object).
   The current `ResourceInfo` / `toolInfo` structs at lines
   146–177 / 236–249 drop everything outside `name` /
   `description` / `mimeType` / `inputSchema`.
3. **Preserve `_meta` on `get_resource` passthrough.** Verify in
   [internal/metatools/handlers.go](../../../internal/metatools/handlers.go)
   (`handleGetResource`, lines 366 + following) and
   [internal/aggregator/server.go](../../../internal/aggregator/server.go)
   (`AggregatorServer.ReadResource`, lines 2994–3016) that the
   upstream `ReadResourceResult` — including each
   `ResourceContents._meta` — round-trips unchanged through the
   aggregator and the meta-tool wrapper. Add a regression test that
   loads a `ui://…` resource end-to-end.
4. **Persist `_meta.ui` on the capability cache.** The
   `Capabilities` struct in
   [internal/aggregator/capability_store.go](../../../internal/aggregator/capability_store.go)
   (lines 11–16) currently holds `Tools` / `Resources` / `Prompts`
   as plain `mcp.*` slices. Whichever shape mcp-go grows for `_meta`
   on those types, the cache (and the Valkey variant in
   `capability_store_valkey.go`) must round-trip it; otherwise the
   first cache hit on a UI-aware upstream silently strips the
   metadata.
5. **Advertise `io.modelcontextprotocol/ui` in the inbound
   `extensions` map** once items 1–4 are done. Reuse the same
   union-of-upstream-extensions computation §3.2 of
   [02-extensions-first-class.md](02-extensions-first-class.md)
   defines. The setting value is the union of upstream
   `mimeTypes` lists; future settings (`features`,
   `sandboxPolicies`) should round-trip verbatim per SEP-2133's
   "additionalProperties: true" passthrough rule.
6. **Pass upstream `mimeTypes` through correctly.** When two
   upstreams disagree on `mimeTypes` (one supports
   `text/html;profile=mcp-app` only, another adds a future MIME),
   the advertised value should be the union *with* a per-upstream
   record of who supports what, so that the meta-tool layer knows
   which tools' `_meta.ui.resourceUri` it is safe to expose to
   which inbound client. See the per-principal / per-deployment
   question in
   [02-extensions-first-class.md](02-extensions-first-class.md)
   §5.
7. **Honour `_meta.ui.visibility`.** SEP-1865's `visibility`
   array (`["model"]` / `["app"]` / `["model","app"]`) means that
   some upstream tools MUST NOT appear in the inbound `tools/list`
   the agent sees. The metatools `list_tools` handler in
   [internal/metatools/handlers.go](../../../internal/metatools/handlers.go)
   needs to filter on `_meta.ui.visibility` when emitting the list
   for an inbound `model`-facing host, and to allow `app`-only
   tools through only when the inbound caller is an MCP Apps app
   on the same upstream connection (per the SEP's "Cross-server
   tool calls are always blocked for app-only tools" rule).
8. **Do not prefetch UI templates server-side.** Make this an
   explicit non-goal in
   [docs/explanation/mcp-aggregation.md](../mcp-aggregation.md):
   muster's caching layer keys on resource URI like any other
   resource and obeys `ttlMs` / `cacheScope`; it does not
   speculatively fetch `ui://…` reads to "help" hosts. The host
   owns prefetch.
9. **Document the agent's graceful-degradation contract.** Add a
   note to [internal/agent/doc.go](../../../internal/agent/doc.go)
   that the REPL and the agent's MCP-server mode do not advertise
   `io.modelcontextprotocol/ui`, and that UI-aware upstream tools
   will surface as text-only tool calls in those modes. Pair the
   change with a release-note line so users with MCP-Apps-emitting
   upstreams know what to expect.
10. **Track the `_meta["ui/resourceUri"]` deprecation.** SEP-1865
    schedules removal of the flat key before extension GA. If
    items 2–4 land, the formatters and aggregator should treat
    `_meta.ui.resourceUri` as canonical and the flat form as
    legacy-only.

## 5. Open questions

- **Per-principal vs deployment-wide `extensions` advertisement.**
  Same question as the cross-extension one in
  [02-extensions-first-class.md](02-extensions-first-class.md)
  §5, restated for MCP Apps: in a multi-tenant muster deployment
  where principal A has access to a UI-aware upstream but
  principal B does not, can the inbound advertisement of
  `io.modelcontextprotocol/ui` vary by principal? SEP-2549 forbids
  per-session variation of list bodies but does not directly address
  the capabilities map. The pragmatic answer is "yes, varying by
  authenticated principal is allowed", but it needs an explicit
  call.
- **Should muster filter or pass through upstream MIME types?**
  Today only `text/html;profile=mcp-app` exists. The SEP says
  `mimeTypes` "MAY be extended" and notes `externalUrl` as a
  deferred-but-likely future addition. If a future upstream
  advertises a MIME type muster has no specific knowledge of, the
  default should presumably be to pass it through; if it ever
  becomes possible to advertise dangerous MIME types (loading
  arbitrary remote URLs into the iframe), muster may want a
  deny-list. Worth revisiting once `externalUrl` lands.
- **Visibility filtering needs an "is this caller an app?" signal.**
  SEP-1865's `visibility: ["app"]` is meaningful only when the
  caller is a known MCP Apps app on the same upstream connection.
  Muster's meta-tool layer has no concept of an "app caller" today;
  the `(authPrincipal, serverName)` keying that
  [01-stateless-protocol.md](01-stateless-protocol.md) §3.1
  proposes does not on its own distinguish a model from an app. The
  filter in change item 7 needs a way to tell them apart, probably
  via a per-request `_meta` field that an MCP Apps host sets.
- **App-provided tools forwarding.** SEP-1865's "App-Provided
  Tools" lets a View register tools the host can call. By the
  spec's lifecycle rules those tools live and die with the iframe
  and MUST NOT persist. If muster ever fronts a host that uses
  MCP Apps, those app tools would surface to muster only through
  the iframe-host channel and never reach the aggregator — but it
  is worth confirming that no one in the host wants to *forward*
  app-tool calls to muster's tool routing layer, because doing so
  would violate the "calling a tool from a closed app MUST return
  an error" rule under muster's typical multi-pod, shared-cache
  topology.
- **Are app-emitted `sampling/createMessage` calls
  muster-aggregable?** The spec says apps MAY call
  `sampling/createMessage` against the host (subject to
  `hostCapabilities.sampling`). Sampling is, per
  [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md),
  being deprecated in 2026-07-28. Whether muster ever sees an
  app-issued sampling call depends on whether the host proxies it
  through the aggregator; the current expectation is "no, sampling
  stays host-local", but worth a sanity check.
- **mcp-go timeline.** As in
  [02-extensions-first-class.md](02-extensions-first-class.md)
  §5: native MCP Apps support is gated on mcp-go shipping both the
  SEP-2133 `extensions` map and a faithful `_meta` carriage on
  tool/resource/result types. If upstream mcp-go is late, the
  fallback is a side-channel marshaller in muster that preserves
  `_meta` even when the typed structs do not — at the cost of
  forking the surface area.

## 6. References

- [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/) — primary announcement, section "MCP Apps: server-rendered user interfaces"
- [MCP Apps - Bringing UI Capabilities To MCP Clients (Jan 2026)](https://blog.modelcontextprotocol.io/posts/2026-01-26-mcp-apps/) — preview blog with context on Claude / ChatGPT / Goose / VS Code adoption and the JS reference SDK
- [SEP-1865 — MCP Apps: Interactive User Interfaces for MCP](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1865) — PR description (Final, Extensions Track)
- [modelcontextprotocol/ext-apps — full draft specification](https://raw.githubusercontent.com/modelcontextprotocol/ext-apps/main/specification/draft/apps.mdx) — the normative wire-level spec (UI resource shape, `_meta.ui`, lifecycle, sandbox-proxy, security mitigations, app-provided tools)
- [ext-apps repository](https://github.com/modelcontextprotocol/ext-apps) — long-term home, SDK, and example servers (threejs, map, pdf, system-monitor, sheet-music, …)
- [SEP-2133 — Extensions framework for MCP](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133) — how `io.modelcontextprotocol/ui` is negotiated through the `extensions` map (cross-section context in [02-extensions-first-class.md](02-extensions-first-class.md))
- Cross-section context inside this folder: [01-stateless-protocol.md](01-stateless-protocol.md) (per-request `_meta`, SEP-2549 `ttlMs` / `cacheScope` for UI-template caching), [02-extensions-first-class.md](02-extensions-first-class.md) (extension negotiation), [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md) (sampling deprecation, referenced under open questions)
- [@modelcontextprotocol/ext-apps](https://www.npmjs.com/package/@modelcontextprotocol/ext-apps) — the JavaScript reference SDK referenced in the preview blog
