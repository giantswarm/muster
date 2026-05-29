# MCP `2026-07-28` — what it means for muster

This folder is muster's working analysis of the MCP
[2026-07-28 release candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/),
"the largest revision of the protocol since launch." The release
candidate is locked as of May 21, 2026 and the final specification is
due July 28, 2026. The three artefacts every reader should keep open
alongside these docs are:

- **Announcement:** [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
- **Draft specification (the RC):** [/specification/draft](https://modelcontextprotocol.io/specification/draft)
- **Draft changelog (every change vs `2025-11-25`):** [/specification/draft/changelog](https://modelcontextprotocol.io/specification/draft/changelog)

Each section document below is grounded in the upstream SEP/PR text
(read via `gh` against
[modelcontextprotocol/modelcontextprotocol](https://github.com/modelcontextprotocol/modelcontextprotocol)),
the announcement, and the relevant external standards, then cross-checked
against muster's own code. Every section follows the same six-part
template — what the spec says, linked SEPs/PRs, muster impact, required
changes, open questions, and a `References` list — so they are easy to
skim.

## Executive summary

muster is, by design, **both an inbound MCP server** (for clients like
Cursor, Claude Code, and the muster agent's own MCP-server mode) **and an
outbound MCP client** (for every aggregated upstream MCP server). The
`2026-07-28` release touches both surfaces, but very unevenly:

- The **stateless rework is the dominant change.** Removing the
  `initialize` handshake, the `Mcp-Session-Id` header, the long-lived GET
  SSE stream, and the free-floating server-to-client channel — and
  replacing them with per-request `_meta`, header-level routing,
  `server/discover`, multi-round-trip `InputRequiredResult`, and
  `ttlMs` / `cacheScope` caching — rewires the parts of muster that are
  built around the protocol-level session (the connection pool, the
  capability store, the session-auth store, the inbound transport, and
  the outbound clients). This is the highest-blast-radius section by far.
- **Extensions become a first-class, negotiated concept,** and MCP Apps
  and Tasks are the two extensions that ship on top of that framework.
  For muster the framework lands first as a **passthrough requirement**
  (forward an opaque `extensions` map both ways) and second as a
  per-extension "do we support this natively?" decision.
- **Authorization is hardened** with six OAuth/OIDC SEPs. muster's auth
  code is mostly aligned already (CIMD `client_id`, issuer-keyed token
  store, agent-side `iss` validation); the main concrete gap is
  **server-side `iss` validation** in the aggregator's OAuth proxy.
- **Roots, Sampling, and Logging are deprecated** — and muster implements
  none of them over MCP today, so this is a **policy commitment and a
  non-event** for the code base. muster's logging is already the
  OpenTelemetry pipeline the spec names as the replacement.
- **Tool schemas adopt full JSON Schema 2020-12** and the
  resource-not-found error code moves from `-32002` to `-32602`. muster's
  schema plumbing is already permissive and emits no literal `-32002`, so
  this is mostly a `mark3labs/mcp-go` dependency bump plus
  "don't regress" guards.
- **Governance and timeline** (the feature-lifecycle policy, the
  conformance gate, the SDK tier system, the RC → Final calendar) are
  pure process for muster, but they hand muster a usable external
  conformance signal and a vocabulary for tracking feature state.

The single most important strategic fact spanning every section: muster
runs on `github.com/mark3labs/mcp-go` (v0.54.1), which is **not** one of
the tier-rated official SDKs. The Tier 1 "ship by July 28" guarantee
applies to `modelcontextprotocol/go-sdk`, **not** to muster's dependency.
**muster's adoption clock is `mcp-go`'s clock, not the spec's** — so most
wire-level work is gated on `mcp-go` shipping a `2026-07-28`-aware
release (or muster migrating to the official Go SDK).

## Document set

| Doc | Section | Primary SEPs / PRs |
| --- | --- | --- |
| [01-stateless-protocol.md](01-stateless-protocol.md) | Stateless protocol (handshake/session removal, server-to-client restructuring, headers/caching, tracing) | SEP-2575, 2567, 2260, 2322, 2243, 2549, 414 |
| [02-extensions-first-class.md](02-extensions-first-class.md) | Extensions become first-class (the framework) | SEP-2133 |
| [03-mcp-apps.md](03-mcp-apps.md) | MCP Apps (server-rendered UIs) | SEP-1865 |
| [04-tasks-extension.md](04-tasks-extension.md) | Tasks graduates to an extension | SEP-2663 |
| [05-authorization-hardening.md](05-authorization-hardening.md) | Authorization hardening | SEP-2468, 837, 2352, 2207, 2350, 2351 |
| [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md) | Roots / Sampling / Logging deprecated | SEP-2577 (+ 2596) |
| [07-json-schema-2020-12.md](07-json-schema-2020-12.md) | Full JSON Schema 2020-12 for tools | SEP-2106, 2164 |
| [08-protocol-evolution.md](08-protocol-evolution.md) | How the protocol evolves from here (governance) | SEP-2596, 2484, 1730/#1777 |
| [09-release-timeline.md](09-release-timeline.md) | Release timeline and validation | (process/calendar) |

These docs are the **seed material for GitHub issues**, not a commitment
to a particular implementation. No muster code, `schema.json`, or Go file
is changed by this folder.

## Risk / effort overview

Per section, an at-a-glance read of blast radius, effort, and what gates
the work. Highest impact first.

- **01 Stateless protocol — highest risk, highest effort, mostly
  gated.** Touches the session layer
  (`internal/aggregator/session_connection_pool.go`,
  `capability_store.go`, `session_auth_store.go` and their Valkey
  variants), the inbound transport (`server.go`, `connection_helper.go`),
  and every outbound client (`internal/mcpserver/client_*.go`). Most of
  it is gated on `mcp-go` speaking the new transport; the internal
  session-model refactor is the one large ungated piece that can start
  now.
- **05 Authorization hardening — medium risk, medium effort, mostly
  ungated.** Concentrated in muster's own OAuth code
  (`internal/oauth/`, `pkg/oauth/`, `internal/agent/oauth/`). Much is
  already aligned; the highest-priority concrete gap is server-side `iss`
  validation in `internal/oauth/handler.go`. Independent of `mcp-go`.
- **04 Tasks extension — medium risk, high effort, gated.** muster's
  workflow engine (`internal/workflow/`) already behaves like a Tasks
  server (durable `execution_id`, status enum, lookup endpoints); mapping
  it onto `tasks/get` / `tasks/update` / `tasks/cancel`, adding
  `input_required` / `cancelled` states, and aggregator forwarding is
  substantial new code. Gated on the `extensions` plumbing and `mcp-go`.
- **02 Extensions framework — medium risk, medium effort, gated.**
  Passthrough of an opaque `extensions` map (inbound and outbound) plus a
  native-support decision matrix. Gated on `mcp-go` exposing the
  `extensions` field on its capability structs.
- **03 MCP Apps — medium risk, low-to-medium effort, gated.** muster
  renders nothing; the work is preserving `_meta.ui` end-to-end through
  the meta-tool layer (which strips `_meta` today), advertising
  `io.modelcontextprotocol/ui` when an upstream supports it, and **not**
  prefetching UI templates server-side. Gated on `mcp-go` `_meta`
  carriage + the extensions field.
- **07 JSON Schema 2020-12 — low risk, low effort, gated on a bump.**
  muster's schema plumbing already passes arbitrary keywords through and
  emits no literal `-32002`. Mostly a mandatory `mcp-go` dependency bump
  plus "don't regress" guards (forward array/primitive
  `structuredContent`, bound schema depth, reject network `$ref`s,
  regression-guard `-32002`). Opportunistic upside: add `outputSchema`
  to muster's own structured-result tools.
- **06 Deprecations — minimal risk, minimal effort, a non-event.** No
  code to delete or migrate (muster speaks none of Roots/Sampling/MCP
  Logging). The work is a policy commitment, a guard scenario, and docs;
  muster's logging is already the OTel replacement.
- **08 Protocol evolution — process, low effort.** Adopt the upstream
  conformance suite in CI (report-only first), add a `sep:` tag
  convention to the BDD scenarios, and decide the `mcp-go`
  vs `modelcontextprotocol/go-sdk` question. No wire work of its own.
- **09 Release timeline — process, no code.** Sequences everything
  above onto the RC → Final calendar; the only hard deadlines muster
  faces are *removal* dates (none inside this window), and muster's pace
  is set by `mcp-go` availability, not July 28.

## Suggested adoption order

Synthesised from the per-section "required changes" and the phased
schedule in [09-release-timeline.md](09-release-timeline.md). The order
front-loads ungated work and test scaffolding so the gated wire work has
a clean place to land and an external signal to validate against.

1. **Decide and track the SDK dependency** ([08](08-protocol-evolution.md)
   §5, [09](09-release-timeline.md) Phase 0). Open a tracking issue for
   `mcp-go`'s `2026-07-28` support and make the
   `mcp-go` vs `modelcontextprotocol/go-sdk` decision once, explicitly —
   it determines how much of the wire work muster writes versus inherits.
2. **Do the ungated muster work** ([09](09-release-timeline.md)
   Phase 1): decouple the internal session model from `Mcp-Session-Id`
   ([01](01-stateless-protocol.md) §4 item 2) and land server-side
   `iss` validation plus the other muster-owned auth items
   ([05](05-authorization-hardening.md) §4).
3. **Stand up the conformance / BDD scaffolding**
   ([08](08-protocol-evolution.md) §4): adopt the upstream conformance
   suite in CI report-only, add the `sep:` tag convention, and add the
   `-32002` regression guard ([07](07-json-schema-2020-12.md) §4.2).
4. **Bump `mcp-go` once it ships `2026-07-28` support**
   ([07](07-json-schema-2020-12.md) §4.1) — unlocks JSON Schema 2020-12
   and the `-32602` error code with a one-line `go.mod` change.
5. **Implement the stateless inbound transport and outbound clients**
   ([01](01-stateless-protocol.md) §4 items 3–8): `server/discover`,
   per-request `_meta`, `Mcp-Method` / `Mcp-Name` routing,
   `subscriptions/listen`, `InputRequiredResult`, and
   `ttlMs` / `cacheScope`.
6. **Wire the `extensions` map and the extension forwarding/native
   support** ([02](02-extensions-first-class.md),
   [04](04-tasks-extension.md), [03](03-mcp-apps.md)).
7. **Flip conformance CI to blocking at/after Final**
   ([08](08-protocol-evolution.md) §5, [09](09-release-timeline.md)
   Phase 3) and add a CHANGELOG entry per adopted surface naming the SEP
   and its lifecycle state.
8. **Carry the deprecations policy throughout** ([06](06-deprecations-roots-sampling-logging.md)):
   do not add Roots/Sampling/MCP-Logging; keep forwarding the deprecated
   methods unchanged; restate the OTel posture in operator docs.

The dependency between sections is small but real: the extensions
framework ([02](02-extensions-first-class.md)) is a prerequisite for
both MCP Apps ([03](03-mcp-apps.md)) and Tasks
([04](04-tasks-extension.md)), and the stateless transport
([01](01-stateless-protocol.md)) underpins per-request `_meta`, which
in turn carries the `extensions` map and the Tasks/clientCapabilities
declarations.

## Consolidated source material

This mirrors the "Source material" index the section docs were written
against. Every SEP/PR was read via `gh` against
`modelcontextprotocol/modelcontextprotocol`; non-GitHub sources were
fetched directly.

### Primary announcement

- Release announcement: <https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/>

### Spec entry points

- Draft specification: <https://modelcontextprotocol.io/specification/draft>
- Draft changelog: <https://modelcontextprotocol.io/specification/draft/changelog>
- 2025-11-25 spec (compare-from baseline): <https://modelcontextprotocol.io/specification/2025-11-25>
- Draft authorization spec: <https://modelcontextprotocol.io/specification/draft/basic/authorization>
- Draft tools spec: <https://modelcontextprotocol.io/specification/draft/server/tools>
- Draft resources spec: <https://modelcontextprotocol.io/specification/draft/server/resources>
- 2025-11-25 (experimental) Tasks spec: <https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/tasks>

### Context & process

- 2026 MCP roadmap: <https://blog.modelcontextprotocol.io/posts/2026-mcp-roadmap/>
- The Future of MCP Transports (Dec 2025): <https://blog.modelcontextprotocol.io/posts/2025-12-19-mcp-transport-future/>
- First MCP anniversary (2025-11-25 release post): <https://blog.modelcontextprotocol.io/posts/2025-11-25-first-mcp-anniversary/>
- MCP Apps preview blog (Jan 2026): <https://blog.modelcontextprotocol.io/posts/2026-01-26-mcp-apps/>
- SEP guidelines: <https://modelcontextprotocol.io/community/sep-guidelines>
- SDK tier system docs: <https://modelcontextprotocol.io/docs/sdk> and <https://modelcontextprotocol.io/community/sdk-tiers>
- Conformance suite repo: <https://github.com/modelcontextprotocol/conformance>
- Spec issue tracker: <https://github.com/modelcontextprotocol/modelcontextprotocol/issues>
- Working & Interest Groups: <https://modelcontextprotocol.io/community/working-interest-groups>
- Official extension repos: [ext-apps](https://github.com/modelcontextprotocol/ext-apps), [ext-auth](https://github.com/modelcontextprotocol/ext-auth), [experimental-ext-tasks](https://github.com/modelcontextprotocol/experimental-ext-tasks)
- muster's SDK dependency: [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go)

### External standards referenced

- JSON Schema 2020-12: <https://json-schema.org/draft/2020-12>
- RFC 9207 (`iss` parameter): <https://www.rfc-editor.org/rfc/rfc9207>
- RFC 8414 (AS metadata): <https://datatracker.ietf.org/doc/html/rfc8414>
- RFC 9728 (Protected Resource Metadata): <https://datatracker.ietf.org/doc/html/rfc9728>
- RFC 7591 (Dynamic Client Registration): <https://datatracker.ietf.org/doc/html/rfc7591>
- RFC 6750 §3.1 (`scope` on `WWW-Authenticate`): <https://datatracker.ietf.org/doc/html/rfc6750#section-3.1>
- OpenID Connect Dynamic Client Registration 1.0: <https://openid.net/specs/openid-connect-registration-1_0.html>
- OAuth 2.0 Security Best Current Practice (mix-up class): <https://datatracker.ietf.org/doc/html/draft-ietf-oauth-security-topics>
- W3C Trace Context: <https://www.w3.org/TR/trace-context/> and W3C Baggage: <https://www.w3.org/TR/baggage/>
- OpenTelemetry: <https://opentelemetry.io/>
- Kubernetes deprecation policy (prior art cited by SEP-2596): <https://kubernetes.io/docs/reference/using-api/deprecation-policy/>
- RFC 8996 (TLS 1.0/1.1 deprecation, prior art): <https://www.rfc-editor.org/rfc/rfc8996>

### All SEPs / PRs by section

- **Stateless protocol** ([01](01-stateless-protocol.md)):
  - SEP-2575 (remove `initialize` handshake): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2575>
  - SEP-2567 (remove `Mcp-Session-Id` and protocol-level session): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2567>
  - SEP-2260 (server-initiated requests only during client request): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2260>
  - SEP-2322 (Multi Round-Trip Requests / `InputRequiredResult`): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2322>
  - SEP-2243 (`Mcp-Method` / `Mcp-Name` headers, `x-mcp-header`): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2243>
  - SEP-2549 (`ttlMs` / `cacheScope` for list/read results): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2549>
  - SEP-414 (W3C Trace Context propagation in `_meta`): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/414>
- **Extensions become first-class** ([02](02-extensions-first-class.md)):
  - SEP-2133 (Extensions framework): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133>
  - SEP-1850 (PR-based SEP workflow): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1850>
- **MCP Apps** ([03](03-mcp-apps.md)):
  - SEP-1865 (MCP Apps): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1865>
  - ext-apps draft spec: <https://raw.githubusercontent.com/modelcontextprotocol/ext-apps/main/specification/draft/apps.mdx>
- **Tasks extension** ([04](04-tasks-extension.md)):
  - SEP-2663 (Tasks extension): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2663>
- **Authorization hardening** ([05](05-authorization-hardening.md)):
  - SEP-2468 (`iss` validation per RFC 9207): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2468>
  - SEP-837 (OIDC `application_type` in DCR): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/837>
  - SEP-2352 (bind credentials to issuer; re-register on migration): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2352>
  - SEP-2207 (refresh tokens with OIDC): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2207>
  - SEP-2350 (scope accumulation on step-up): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2350>
  - SEP-2351 (`.well-known` discovery suffix): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2351>
- **Deprecations (Roots, Sampling, Logging)** ([06](06-deprecations-roots-sampling-logging.md)):
  - SEP-2577 (deprecation of Roots/Sampling/Logging): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2577>
- **JSON Schema 2020-12 for tools** ([07](07-json-schema-2020-12.md)):
  - SEP-2106 (full JSON Schema 2020-12 for `inputSchema` / `outputSchema`): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2106>
  - SEP-2164 (resource-not-found error code `-32002` → `-32602`): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2164>
- **Protocol evolution / governance** ([08](08-protocol-evolution.md)):
  - SEP-2596 (feature lifecycle and deprecation policy): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2596>
  - SEP-2484 (Standards-Track conformance gate): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2484>
  - SEP-1730 / PR #1777 (SDK tier system): <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1777>

## References

- [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/) — the announcement this whole folder analyses
- [Draft specification](https://modelcontextprotocol.io/specification/draft) — the release candidate
- [Draft changelog](https://modelcontextprotocol.io/specification/draft/changelog) — every change against `2025-11-25`, SEP-tagged
- The nine section documents in this folder:
  [01-stateless-protocol.md](01-stateless-protocol.md),
  [02-extensions-first-class.md](02-extensions-first-class.md),
  [03-mcp-apps.md](03-mcp-apps.md),
  [04-tasks-extension.md](04-tasks-extension.md),
  [05-authorization-hardening.md](05-authorization-hardening.md),
  [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md),
  [07-json-schema-2020-12.md](07-json-schema-2020-12.md),
  [08-protocol-evolution.md](08-protocol-evolution.md),
  [09-release-timeline.md](09-release-timeline.md) — each with its own
  full `References` section listing the SEPs/PRs and standards it relied on.
- Related existing muster explanation docs:
  [mcp-aggregation.md](../mcp-aggregation.md),
  [architecture.md](../architecture.md),
  [observability.md](../observability.md), and the decision records
  [decisions/005-muster-auth.md](../decisions/005-muster-auth.md),
  [decisions/006-session-scoped-tool-visibility.md](../decisions/006-session-scoped-tool-visibility.md),
  [decisions/011-session-connection-pool.md](../decisions/011-session-connection-pool.md).
