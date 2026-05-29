# Release timeline and validation (MCP 2026-07-28)

The `2026-07-28` release is shipping as a **release candidate** first and
a **final specification** later, with a deliberate validation window in
between. The closing operational section of the announcement,
["Release Timeline and Validation"](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/),
states the schedule plainly:

> The release candidate is locked as of May 21, 2026. The final
> specification will be published on July 28, 2026. The ten-week window
> is for SDK maintainers and client implementers to validate the changes
> against real workloads; under the SDK tier system, Tier 1 SDKs are
> expected to ship support within this window.

This document is the **planning bookend** of the
`docs/explanation/mcp-2026-07-28/` series. Where
[08-protocol-evolution.md](08-protocol-evolution.md) covers the
*governance* that keeps future revisions from breaking things, this one
covers the *calendar*: the upstream RC → Final schedule, where the spec
and its changelog live during the window, how the SDK tier system turns
the abstract "validate against real workloads" into a concrete
ship-by-date obligation for the SDKs muster depends on, how the spec
issue tracker is the feedback channel during the window, and how the
Working & Interest Groups structure the conversation. It ends with a
suggested muster adoption schedule that hangs the wire-level work from
the other section docs onto this timeline.

The single most important strategic fact carries over from
[08-protocol-evolution.md](08-protocol-evolution.md) §3.1: muster does
not run on one of the tier-rated official SDKs. It runs on
**`github.com/mark3labs/mcp-go` v0.54.1** (and `mcp-go/otel` v0.54.0) —
see [go.mod](../../../go.mod) — which is *not* listed in the SDK tier
table at all. The "Tier 1 SDKs ship within the window" guarantee
therefore does **not** apply to muster's dependency, and that is what
makes muster's timeline a function of `mcp-go`'s timeline, not the
spec's.

## 1. What the spec says

### 1.1 The RC → Final schedule and the ten-week validation window

The announcement fixes three dates:

- **May 21, 2026 — the release candidate is locked.** The wire format
  and the SEP set are frozen at this point; the RC is the thing
  implementers validate against. "Locked" means no further substantive
  changes are expected before Final, only fixes surfaced by validation.
- **July 28, 2026 — the final specification is published.** This is the
  Current revision that implementations targeting `2026-07-28` are held
  to. It is also the version string clients send in
  `MCP-Protocol-Version` and `_meta.io.modelcontextprotocol/protocolVersion`
  (see [01-stateless-protocol.md](01-stateless-protocol.md) §1.1).
- **The ~ten weeks in between** (May 21 → July 28) is the validation
  window, explicitly scoped "for SDK maintainers and client implementers
  to validate the changes against real workloads."

The announcement also tells implementers exactly where the
authoritative artefacts are during the window: "The full release
candidate is in the draft specification, and the changelog will list
every change against `2025-11-25`." Both are below.

The announcement frames the breaking nature of the release as a
one-time event, in the same paragraph as the timeline rationale:
because the stateless rework is "the kind of foundational change that
needed a clean break," and because deprecation windows and extensions
are now the standard evolution tools (see
[08-protocol-evolution.md](08-protocol-evolution.md)), the
expectation set is that *future* revisions will not need a ten-week
break-the-world window. `2026-07-28` is the exception the governance
machinery is designed to prevent recurring.

### 1.2 The draft specification is the RC

During the window the RC lives at the
[draft specification](https://modelcontextprotocol.io/specification/draft).
Its overview already reflects the post-RC shape of the base protocol —
the "Key Details" section now reads:

> ### Base Protocol
> - JSON-RPC message format
> - **Stateless, self-contained requests**
> - **Per-request capability negotiation**

That is the stateless core from
[01-stateless-protocol.md](01-stateless-protocol.md) stated as a
top-level property of the protocol, not a transport footnote. The draft
spec is what `--spec-version draft` (aliased to the current draft
identifier) resolves to in the upstream conformance suite (see
[08-protocol-evolution.md](08-protocol-evolution.md) §1.4), which is
why the validation window and the conformance suite are the same
exercise viewed from two angles.

### 1.3 The changelog is the migration map from 2025-11-25

The [draft changelog](https://modelcontextprotocol.io/specification/draft/changelog)
is the authoritative, SEP-tagged enumeration of "every change against
`2025-11-25`" the announcement promises. Its structure is itself the
shape of the work in this folder:

- **Seven major changes** — session/`Mcp-Session-Id` removal (SEP-2567),
  statelessness and the handshake removal with per-request `_meta`
  (SEP-2575), the new `server/discover` RPC (SEP-2575), the GET-endpoint
  /`resources/subscribe` replacement by `subscriptions/listen`
  (SEP-2575), the removal of `ping` / `logging/setLevel` /
  `notifications/roots/list_changed` (SEP-2575), Tasks moving to the
  `io.modelcontextprotocol/tasks` extension (SEP-2663), and Multi
  Round-Trip Requests replacing server-initiated requests (SEP-2322).
  These map onto [01-stateless-protocol.md](01-stateless-protocol.md)
  and [04-tasks-extension.md](04-tasks-extension.md).
- **Six minor changes** — the `extensions` field on client/server
  capabilities (→ [02-extensions-first-class.md](02-extensions-first-class.md)),
  the OpenTelemetry `_meta` trace-context conventions (SEP-414),
  deterministic `tools/list` ordering, the `Mcp-Method` / `Mcp-Name`
  headers and `x-mcp-header` (SEP-2243), the `ttlMs` / `cacheScope`
  `CacheableResult` interface (SEP-2549), and the `-32002` → `-32602`
  resource-not-found code change (→
  [07-json-schema-2020-12.md](07-json-schema-2020-12.md)).
- **Three deprecations** — Roots / Sampling / Logging (SEP-2577), the
  HTTP+SSE transport reclassified under the lifecycle policy
  (SEP-2596), and the `includeContext` `"thisServer"` / `"allServers"`
  values (SEP-2596). These map onto
  [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md).
- **Governance and process** — the feature lifecycle and deprecation
  policy with its twelve-month minimum window and deprecated-features
  registry (SEP-2596), and the PR-based SEP workflow (SEP-1850). These
  map onto [08-protocol-evolution.md](08-protocol-evolution.md).

For muster, the changelog is the single most useful upstream artefact in
the window: it is the closed list of surfaces muster must audit, each
already tagged with the SEP that owns it, which is precisely the index
the per-section docs in this folder were written against.

### 1.4 The SDK tier system turns "validate" into "ship by July 28"

The validation window is not advisory for everyone. The
[SDK index](https://modelcontextprotocol.io/docs/sdk) publishes the
current tier assignments, and the tier system (SEP-1730 / PR #1777,
covered in [08-protocol-evolution.md](08-protocol-evolution.md) §1.3)
attaches a ship-by obligation to Tier 1:

- **Tier 1 — Fully Supported:** TypeScript
  (`modelcontextprotocol/typescript-sdk`), Python
  (`modelcontextprotocol/python-sdk`), C#
  (`modelcontextprotocol/csharp-sdk`), and **Go
  (`modelcontextprotocol/go-sdk`)**. Tier 1 is the tier the announcement
  refers to: these SDKs are "expected to ship support within this
  window," i.e. to speak `2026-07-28` by July 28, 2026, alongside a 100%
  conformance pass rate.
- **Tier 2 — Commitment to Full Support:** Java
  (`modelcontextprotocol/java-sdk`), Rust
  (`modelcontextprotocol/rust-sdk`). New features within six months —
  i.e. no obligation to be ready at Final.
- **Tier 3 — Experimental:** Swift, Ruby, PHP. No timeline commitment.
- **TBD:** Kotlin.

The page directs readers to the
[SDK Tiering System](https://modelcontextprotocol.io/community/sdk-tiers)
for what each tier guarantees. The operative point for this document is
that the tier table is the *only* upstream instrument that converts the
soft "validate against real workloads" into a hard date — and it does so
only for the official SDKs.

### 1.5 The spec issue tracker is the validation feedback channel

The announcement names the feedback path directly: "If you find a
problem, open an issue in the specification repository." The
[spec issue tracker](https://github.com/modelcontextprotocol/modelcontextprotocol/issues)
is therefore the live record of validation-window findings. As of this
writing it carries **129 open issues**, and several labels are timeline-
relevant:

- **`rc-high-priority`** — the issues that gated the RC. All **three**
  (`#1847` SSE-polling scope of SEP-1699, `#1824` how `input_required`
  is handled in the Tasks spec, `#1819` whether a server signals
  URL-mode elicitation completion) are now **CLOSED**, which is the
  signal that the RC lock on May 21 was real: the blocking items were
  resolved before the freeze.
- **`sdk/2026-07-feedback`** — the label for validation feedback against
  this release (e.g. `#2748` "SEP-2352 Clarification"). This is the
  label muster's own validation findings would carry if filed upstream.
- Open issues that bear directly on the muster section docs include
  `#2806` ("`oneOf` in tool `inputSchema` isn't practically consumable
  by tool-calling LLMs" — relevant to
  [07-json-schema-2020-12.md](07-json-schema-2020-12.md)), `#2762`
  ("SEP-2243 Clarifications" — the header-routing rules in
  [01-stateless-protocol.md](01-stateless-protocol.md)), and `#2721`
  (protocol-version conflict between `initialize.params.protocolVersion`
  and `MCP-Protocol-Version`). These are exactly the "validate against
  real workloads" findings the window exists to surface, and they are
  worth watching because a clarification landing before July 28 can
  change muster's implementation target.

### 1.6 The Working & Interest Groups own the conversation

The announcement's second feedback route — "For implementation
questions, the relevant Working Group channel in the contributor Discord
is the fastest path to an answer" — points at the structure documented
on the
[Working & Interest Groups page](https://modelcontextprotocol.io/community/working-interest-groups).
The relevant distinctions:

- **Working Groups (WGs)** produce concrete deliverables — SEPs,
  reference implementations, and ongoing projects (Registry, Inspector,
  Tool Filtering, Server Identity). They make binding decisions via lazy
  consensus → formal vote → escalation. The **SDK Working Group**
  approves tier advancement and oversees relegation (per
  [08-protocol-evolution.md](08-protocol-evolution.md) §1.3), so it is
  the group that governs the "ship by July 28" expectation.
- **Interest Groups (IGs)** identify and discuss problems without binding
  output (Security in MCP, Auth in MCP, MCP in enterprise settings,
  tooling for hosting MCP clients). The Auth and Security IGs are the
  natural venues for the
  [05-authorization-hardening.md](05-authorization-hardening.md)
  questions; an enterprise/hosting IG is the natural venue for muster's
  aggregator-specific concerns.
- **Reporting cadence:** WGs publish quarterly updates at the end of
  January, April, July, and October. The **July** update lands the same
  month as the Final spec, so it is the checkpoint where the validation
  window's outcome (which SDKs shipped, which findings are unresolved)
  becomes visible. Each group also publishes meetings on
  `meet.modelcontextprotocol.io` at least seven days ahead with notes
  within 48 hours — a predictable signal muster can subscribe to rather
  than poll.

## 2. Linked SEPs and PRs

This is a timeline/process document, so the per-section SEPs are
referenced through their own docs rather than re-summarised here. The
primary sources are the schedule, spec, and process artefacts:

- Announcement, "Release Timeline and Validation":
  https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/
- Draft specification (the RC):
  https://modelcontextprotocol.io/specification/draft
- Draft changelog (every change vs `2025-11-25`):
  https://modelcontextprotocol.io/specification/draft/changelog
- SDK index and tier assignments:
  https://modelcontextprotocol.io/docs/sdk and
  https://modelcontextprotocol.io/community/sdk-tiers
- Spec issue tracker (validation feedback):
  https://github.com/modelcontextprotocol/modelcontextprotocol/issues
- Working & Interest Groups:
  https://modelcontextprotocol.io/community/working-interest-groups
- SDK tier system PR (the ship-by obligation): SEP-1730 / PR #1777,
  https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1777
- Conformance gate (Standards-Track Final requires a scenario): SEP-2484,
  https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2484
- The per-section SEPs themselves are listed in
  [01](01-stateless-protocol.md) through
  [08](08-protocol-evolution.md) and in the folder
  [README.md](README.md).

## 3. Muster impact

The plan's brief for this document is "timeline, suggested milestones for
muster … and which upstream SDKs muster depends on (track their tier-1
readiness)." There is no wire-level work unique to this doc; instead it
sequences the work the other docs define. Three facts drive muster's
timeline.

### 3.1 Muster's clock is `mcp-go`'s clock, not the spec's

Muster depends on `github.com/mark3labs/mcp-go` v0.54.1
([go.mod](../../../go.mod)), built with Go 1.26.0 / toolchain 1.26.3.
`mcp-go` is **not** in the SDK tier table — the official Go SDK is
`modelcontextprotocol/go-sdk`, which is Tier 1 and therefore carries the
"ship `2026-07-28` support by July 28" obligation. `mark3labs/mcp-go`
carries no such obligation and is not scored by the conformance suite's
tier assessment.

The practical consequence, repeated across the section docs, is that
muster cannot adopt most of `2026-07-28` until `mcp-go` ships a release
that speaks the new transport and schema:

- The stateless transport, headers, `server/discover`, and
  `InputRequiredResult` work is gated on `mcp-go`
  ([01-stateless-protocol.md](01-stateless-protocol.md) §4 item 1:
  "Most of the work below is gated on the underlying SDK speaking the
  new transport").
- The JSON Schema 2020-12 and `-32002` → `-32602` work is "one mandatory
  dependency bump" on `mcp-go`
  ([07-json-schema-2020-12.md](07-json-schema-2020-12.md) §4.1): until
  `mcp-go` ships, muster's `tools/list` is capped at the 2025-11-25
  subset and its `resources/read` error code is non-conforming, and
  "there is nothing muster's own Go can do to fix either of those
  without forking the library."

So the muster timeline has an explicit, non-muster-controlled
dependency: **`mcp-go`'s `2026-07-28` release date**. That date is not
guaranteed by the tier system, which is why §5 raises the migrate-to-
`go-sdk` question and why the milestone schedule below has a "track
upstream" milestone before any "implement" milestone.

### 3.2 The work muster *can* do without `mcp-go` shipping

Not everything is gated. Several items in the other docs are pure muster
Go and can land during the validation window regardless of `mcp-go`:

- **Decouple muster's internal session model from `Mcp-Session-Id`**
  ([01-stateless-protocol.md](01-stateless-protocol.md) §4 item 2):
  re-key `SessionConnectionPool`, `CapabilityStore`, and
  `SessionAuthStore` (and their Valkey variants in
  [internal/aggregator/](../../../internal/aggregator)) on a
  muster-owned principal. This is internal refactoring with no wire
  dependency.
- **Server-side `iss` validation** (SEP-2468,
  [05-authorization-hardening.md](05-authorization-hardening.md) §4
  item 1): the highest-priority auth change touches
  [internal/oauth/handler.go](../../../internal/oauth/handler.go) and
  [pkg/oauth/types.go](../../../pkg/oauth/types.go), entirely within
  muster's own OAuth code, not `mcp-go`.
- **The conformance and BDD scaffolding**
  ([08-protocol-evolution.md](08-protocol-evolution.md) §4): adopting
  the upstream conformance suite in CI (report-only), adding a `sep:`
  tag convention to the 129 scenarios in
  [internal/testing/scenarios/](../../../internal/testing), and the
  `-32002` regression guard
  ([07-json-schema-2020-12.md](07-json-schema-2020-12.md) §4.2). These
  produce the test signal that later, gated work is validated against.

This split is what makes a sensible muster schedule possible: do the
ungated refactors and the test scaffolding during the upstream window,
so that the moment `mcp-go` ships, the gated wire work has somewhere
clean to land and an external signal to validate against.

### 3.3 Muster's own validation contributes to the window

Muster is a non-trivial real-world MCP host-and-aggregator, so running
the RC against muster's workloads is exactly the "validate against real
workloads" the announcement asks for. Muster has two channels back into
the window:

- **File findings on the spec issue tracker** under
  `sdk/2026-07-feedback` (§1.5). Aggregator-specific edge cases —
  multi-upstream capability merging, session-isolation under the
  stateless model, OAuth issuer binding across many upstreams — are the
  kind of finding the official-SDK maintainers are less likely to hit.
- **Engage the relevant WG/IG** (§1.6): the Auth IG for the
  [05](05-authorization-hardening.md) questions, an enterprise/hosting
  IG for aggregator concerns, the SDK WG for `mcp-go`'s tier posture.

This is optional but cheap, and it is the lever by which muster reduces
the risk that the Final spec ships with a gap that only an aggregator
hits.

## 4. Required changes / migration notes

These are not code changes to make today; they are a **suggested muster
adoption schedule** to file as issues once the section docs in this
folder all land. The phases hang the work items from the other docs onto
the upstream RC → Final calendar. "Now" means during the validation
window (the window opened May 21, 2026; Final is July 28, 2026).

**Phase 0 — Track and decide (now, no `mcp-go` dependency).**

1. **Open a tracking issue for `mcp-go`'s `2026-07-28` support** and
   watch its releases against the
   [draft changelog](https://modelcontextprotocol.io/specification/draft/changelog)
   surfaces. Record whether `mcp-go` commits to a date at all. This is
   the gating dependency for every Phase 2 item.
2. **Make the `mcp-go` vs `modelcontextprotocol/go-sdk` decision**
   ([08-protocol-evolution.md](08-protocol-evolution.md) §5,
   [01-stateless-protocol.md](01-stateless-protocol.md) §5). The Tier
   1 go-sdk inherits conformance for free but is a large cross-cutting
   migration; staying on `mcp-go` means muster owns the conformance
   validation itself. Decide once, explicitly, because it determines how
   much of Phase 2 muster writes versus gets from the SDK.

**Phase 1 — Ungated muster work + test scaffolding (now → Final).**

3. **Decouple the internal session model from `Mcp-Session-Id`**
   ([01](01-stateless-protocol.md) §4 item 2). Pure refactor; unblocks
   the inbound transport rework.
4. **Land server-side `iss` validation** (SEP-2468,
   [05](05-authorization-hardening.md) §4 item 1) and the other
   muster-owned auth items (SEP-837 `application_type`, SEP-2207
   `offline_access`, SEP-2350 scope accumulation) that do not depend on
   `mcp-go`.
5. **Stand up the conformance suite in CI, report-only**
   ([08](08-protocol-evolution.md) §4 items 1–2) against the `draft`
   spec-version, with a `conformance-baseline.yml` listing everything
   muster does not yet implement. Keep it non-blocking until Final (see
   §5 and [08](08-protocol-evolution.md) §5).
6. **Add the `sep:` tag convention and the `-32002` guard**
   ([08](08-protocol-evolution.md) §4 items 3, 5;
   [07](07-json-schema-2020-12.md) §4.2) to
   [internal/testing/scenarios/](../../../internal/testing).

**Phase 2 — Gated wire work (when `mcp-go` ships `2026-07-28` support).**

7. **Bump `mcp-go`** ([07](07-json-schema-2020-12.md) §4.1) — the
   one-line `go.mod` change that unlocks 2020-12 schemas and the
   `-32602` error code.
8. **Implement the stateless inbound transport** — `server/discover`,
   per-request `_meta`, `Mcp-Method`/`Mcp-Name` routing,
   `subscriptions/listen`, `InputRequiredResult`, `ttlMs`/`cacheScope`
   ([01](01-stateless-protocol.md) §4 items 3–8) — and the matching
   outbound-client changes in
   [internal/mcpserver/](../../../internal/mcpserver).
9. **Wire the `extensions` map and the Tasks/MCP-Apps forwarding**
   ([02](02-extensions-first-class.md), [04](04-tasks-extension.md),
   [03](03-mcp-apps.md)).

**Phase 3 — Conformance gate flips (at/after Final, July 28).**

10. **Flip the conformance CI from report-only to blocking** once the
    Final spec is published and the `draft` suite stabilises into the
    `2026-07-28` tag, trimming the `expected-failures` baseline as each
    Phase 2 surface lands (the stale-entry detection forces this). This
    is the decision deferred from
    [08-protocol-evolution.md](08-protocol-evolution.md) §5.
11. **Add a CHANGELOG entry per adopted surface** naming the SEP and its
    lifecycle state ([08](08-protocol-evolution.md) §4 item 8), so
    operators reading muster's
    [CHANGELOG.md](../../../CHANGELOG.md) can map muster behaviour onto
    the upstream registry.

The hard deadline in this schedule is **not** July 28 — muster is a
downstream consumer, not a Tier 1 SDK, so it is not obligated to ship by
Final. The only hard deadlines muster faces are *removal* dates under the
twelve-month lifecycle policy (none of which fall inside this window; see
[06](06-deprecations-roots-sampling-logging.md) and
[08](08-protocol-evolution.md)). Everything here is paced by `mcp-go`
availability and muster's own capacity, with the upstream window as the
recommended — not mandatory — target.

## 5. Open questions

- **When does `mark3labs/mcp-go` ship `2026-07-28` support, and will it?**
  This is the load-bearing unknown for the entire schedule. The tier
  system guarantees the *official* `go-sdk` ships by July 28; it says
  nothing about `mcp-go`. If `mcp-go` lags or never adopts the RC,
  Phase 2 slips indefinitely and the migrate-to-`go-sdk` decision
  (Phase 0 item 2) becomes forced rather than optional. Muster should
  not assume the spec's July 28 date is muster's date.

- **Report-only or blocking conformance CI before Final?** Wiring the
  `draft` suite into a *blocking* gate during the validation window risks
  churn, because draft scenarios can still change in response to the
  open feedback issues (§1.5). The likely answer — run report-only until
  July 28, then flip to blocking once the suite pins to the `2026-07-28`
  tag — is captured as Phase 1 item 5 / Phase 3 item 10, but the exact
  flip date depends on when the suite stabilises, which muster does not
  control.

- **Should muster file its validation findings upstream, and through
  which channel?** Muster will exercise aggregator-specific behaviour the
  official SDKs may not (multi-upstream merging, session isolation under
  statelessness, cross-issuer OAuth). Filing under
  `sdk/2026-07-feedback` and/or raising it in the Auth/Security/enterprise
  IGs is high-leverage for the ecosystem but is unbudgeted work; whether
  muster commits to it in the window is an open call (mirrors the
  "contribute scenarios upstream" question in
  [08-protocol-evolution.md](08-protocol-evolution.md) §5).

- **Do any open spec issues change muster's implementation target before
  Final?** Issues like `#2806` (`oneOf` consumability), `#2762` (SEP-2243
  clarifications), and `#2721` (protocol-version conflict) could alter
  the exact behaviour muster implements in Phase 2. Muster should track
  the ones that touch its section docs and avoid implementing against an
  RC detail that an open issue is actively contesting.

- **What is muster's standing subscription to upstream signals?** The
  WG meeting feed (`meet.modelcontextprotocol.io`, 7-day notice), the
  quarterly WG updates (the July one is the key checkpoint), and the
  `sdk/2026-07-feedback` label are all pollable. Whether muster
  formalises watching them (e.g. a recurring review) or leaves it
  ad-hoc is a process decision worth making once.

## 6. References

- [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
  — announcement, section "Release Timeline and Validation" (RC locked
  May 21, 2026; Final July 28, 2026; ten-week window; Tier 1 SDKs ship
  within the window; spec issue tracker and Working Group Discord as the
  feedback channels) and "How the Protocol Evolves From Here" (the
  one-time-break framing).
- [Draft specification](https://modelcontextprotocol.io/specification/draft)
  — the RC; "Base Protocol: stateless, self-contained requests,
  per-request capability negotiation."
- [Draft changelog](https://modelcontextprotocol.io/specification/draft/changelog)
  — the SEP-tagged enumeration of every change against `2025-11-25`
  (seven major, six minor, three deprecations, governance/process),
  which is muster's audit index for this release.
- [SDK index (/docs/sdk)](https://modelcontextprotocol.io/docs/sdk)
  and [SDK Tiering System (/community/sdk-tiers)](https://modelcontextprotocol.io/community/sdk-tiers)
  — current tier assignments (Go `modelcontextprotocol/go-sdk` Tier 1;
  TypeScript/Python/C# Tier 1; Java/Rust Tier 2; Swift/Ruby/PHP Tier 3;
  Kotlin TBD). `mark3labs/mcp-go` is not listed.
- [PR #1777 — SEP-1730 SDK tiers definition](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1777)
  — the ship-by-window obligation that applies to Tier 1.
- [SEP-2484 — conformance test gate](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2484)
  — the conformance suite that the validation window's `draft` tag feeds.
- [Spec issue tracker](https://github.com/modelcontextprotocol/modelcontextprotocol/issues)
  — 129 open issues; the `rc-high-priority` set (`#1847`, `#1824`,
  `#1819`) is all closed; `sdk/2026-07-feedback` is the validation-feedback
  label; `#2806` / `#2762` / `#2721` are open issues touching muster's
  section docs. Queried via `gh issue list` / `gh issue list --label`.
- [Working & Interest Groups](https://modelcontextprotocol.io/community/working-interest-groups)
  — WG vs IG roles, the SDK WG's authority over tier advancement, and
  the quarterly (Jan/Apr/Jul/Oct) reporting cadence whose July update
  coincides with Final.
- Cross-section context within `docs/explanation/mcp-2026-07-28/`:
  - [01-stateless-protocol.md](01-stateless-protocol.md) — the gated
    transport work and the `mcp-go` dependency (§4 item 1).
  - [02-extensions-first-class.md](02-extensions-first-class.md) and
    [03-mcp-apps.md](03-mcp-apps.md) and
    [04-tasks-extension.md](04-tasks-extension.md) — the extension
    forwarding in Phase 2 item 9.
  - [05-authorization-hardening.md](05-authorization-hardening.md) —
    the ungated `iss`-validation work in Phase 1 item 4.
  - [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md)
    — where the only hard (removal) deadlines come from.
  - [07-json-schema-2020-12.md](07-json-schema-2020-12.md) — the
    mandatory `mcp-go` bump (§4.1) and the `-32002` guard (§4.2).
  - [08-protocol-evolution.md](08-protocol-evolution.md) — the
    conformance/BDD scaffolding (§4) and the migrate-to-`go-sdk` and
    blocking-CI open questions (§5).
- Muster code paths cited in this document:
  [go.mod](../../../go.mod) (`mark3labs/mcp-go` v0.54.1, Go 1.26.0),
  [internal/aggregator/](../../../internal/aggregator),
  [internal/mcpserver/](../../../internal/mcpserver),
  [internal/oauth/handler.go](../../../internal/oauth/handler.go),
  [pkg/oauth/types.go](../../../pkg/oauth/types.go),
  [internal/testing/scenarios/](../../../internal/testing),
  [CHANGELOG.md](../../../CHANGELOG.md).
