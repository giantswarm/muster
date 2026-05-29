# How the protocol evolves from here (MCP 2026-07-28)

The `2026-07-28` release candidate is, by the announcement's own
admission, "the largest revision of the protocol since launch" and it
"contains breaking changes." The final section of the announcement,
["How the Protocol Evolves From Here"](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/),
is the promise that this will not be the norm:

> This release contains breaking changes. We don't intend for that to
> be the norm. Three governance SEPs in this release are designed so
> that future revisions can evolve the protocol without breaking core
> capabilities. The feature lifecycle policy gives every feature an
> Active, Deprecated, and Removed lifecycle with at least twelve months
> between deprecation and the earliest possible removal. The Extensions
> framework means new capabilities can ship as opt-in extensions and
> stabilize there before, if ever, moving into the specification. And a
> Standards Track SEP can no longer reach Final status until a matching
> scenario lands in the conformance suite (SEP-2484), which is the same
> suite the new SDK tier system scores official SDKs against.

This document is the governance/strategy bookend of the
`docs/explanation/mcp-2026-07-28/` series. Where the other section
docs describe wire-level changes muster must implement, this one is
almost entirely **process**: how muster decides what to adopt and
when, how it tracks the Active/Deprecated/Removed state of every
feature it depends on, and — the most concrete deliverable — how the
existing BDD scenarios in [internal/testing/scenarios/](../../../internal/testing)
relate to the new upstream conformance suite that now gates the
specification itself.

Three governance instruments matter here, and one supporting one:

- **SEP-2596** — the feature lifecycle and deprecation policy
  (Active / Deprecated / Removed). Covered in passing in
  [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md);
  given its full treatment here.
- **SEP-2484** — the conformance-test gate on `Accepted → Final` for
  Standards Track SEPs.
- **SEP-1730 / PR #1777** — the SDK tier system that SEP-2484 feeds.
- **The Extensions framework (SEP-2133)** — the third governance lever,
  covered in full in [02-extensions-first-class.md](02-extensions-first-class.md);
  referenced here only as the "ship it as an extension" escape hatch.

## 1. What the spec says

### 1.1 SEP-2596 — the feature lifecycle and deprecation policy

[SEP-2596 — "Specification Feature Lifecycle and Deprecation Policy"](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2596)
(Den Delimarsky, `accepted`, MERGED, type **Process**) defines a
lifecycle for *individual features* within the specification, distinct
from the Draft/Current/Final lifecycle of the specification *document*.
Its abstract states the goal as "a predictable timeline that SDK
authors and implementers can plan migrations against when protocol
surface area is retired."

The diff (`gh pr diff 2596`) adds a single SEP page,
`docs/seps/2596-spec-feature-lifecycle-and-deprecation.mdx`, and
registers it in `docs/docs.json`. The substance:

**Three feature states.** A specification feature is always in exactly
one of:

- **Active** — part of the Current revision with no planned removal.
  Implement per the feature's normative requirements.
- **Deprecated** — still in the spec but scheduled for removal, with a
  documented migration path. New implementations SHOULD NOT adopt it;
  existing ones SHOULD migrate before the earliest removal date.
- **Removed** — deleted from `draft` and absent from the next Current
  revision (it remains documented in the last Final revision that
  carried it). Implementations targeting that next revision MUST NOT
  depend on it.

The SEP explicitly **retires the term "soft-deprecated"** and folds the
two existing informal deprecations into the new vocabulary.

**Deprecation is a SEP; removal is not.** A feature may be proposed for
deprecation when it is superseded, presents an unmitigable
security/privacy/interoperability risk, or has negligible adoption
relative to maintenance cost. Deprecation requires its own SEP that
must (1) identify the feature and link its `schema.ts` definition, (2)
state the rationale, (3) document the migration path (and the named
replacement must already be Active, not merely in `draft`), and (4)
specify the **minimum deprecation window**: at least **twelve months**,
measured from the *release of the revision* in which the feature is
first marked Deprecated — not from the date the SEP reaches Final.
Removal itself does **not** need a second SEP; once the deprecation SEP
is Final the project has committed to removal, and removal happens as a
Core Maintainer decision at release-preparation time once the window
has elapsed. A second SEP is reserved only for the cases that change
the committed outcome: extending the window, restoring the feature, or
invoking the **expedited-removal** clause (which shortens the floor for
a security risk, with a three-month minimum).

**Tier 1 SDK obligations.** SEP-2596 ties into the SDK tier system:
Tier 1 SDKs are expected to surface the deprecation to developers
(an `@deprecated` annotation and, where practical, a runtime warning
when a Deprecated feature is exercised).

**The `deprecated.mdx` registry.** When the SEP reaches Final it seeds
`docs/specification/draft/deprecated.mdx` as the canonical single-page
index of every Deprecated feature, its SEP, the revision it became
Deprecated in, and its earliest removal date. The two grandfathered
features (the HTTP+SSE transport and `includeContext: "thisServer" /
"allServers"`) are listed there with a three-month grace period because
the ecosystem has already had well over a year to migrate.

The Rationale section anchors the policy in prior art — the
[Kubernetes deprecation policy](https://kubernetes.io/docs/reference/using-api/deprecation-policy/),
the Node.js deprecation cycle, and IETF practice such as
[RFC 8996](https://www.rfc-editor.org/rfc/rfc8996) (deprecating TLS 1.0
/ 1.1) — and notes the relationship to SEP-1400 (semantic versioning)
is complementary: SEP-2596 measures the window from a revision
*release*, so it is independent of how revisions are numbered.

### 1.2 SEP-2484 — conformance tests gate Standards Track SEPs

[SEP-2484 — "Require Conformance Tests for Standards Track SEPs to Reach
Final Status"](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2484)
(Paul Carleton, `accepted`, MERGED, type **Process**) adds a
conformance-test requirement to the `Accepted → Final` transition. The
abstract:

> Before a Standards Track SEP that changes observable protocol
> behavior can be marked `Final`, a conformance scenario covering its
> normative requirements must be merged into the conformance
> repository, accompanied by a structured traceability file mapping
> each MUST/MUST NOT and SHOULD/SHOULD NOT to a check or a documented
> exclusion.

The motivation is the gap between English prose and code: "SDK
maintainers translate that English into code, and every translation is
an opportunity for drift." A reference implementation only proves *one*
valid interpretation can be built; a conformance test defines what
*every* implementation must do, as executable assertions.

The mechanics from the diff (`gh pr diff 2484`, which edits
`docs/community/sep-guidelines.mdx` and adds the SEP page):

- **Scope.** Applies only to Standards Track SEPs that change
  **observable protocol behavior** — anything a conformant peer can
  detect on the wire, transport-observable side effects (HTTP status
  codes, headers, connection lifecycle, OAuth redirects), or
  process-observable side effects for stdio (stream content, exit
  codes).
- **Exempt.** Process and Informational SEPs (so SEP-2596 and SEP-2484
  themselves are exempt), and Standards Track SEPs with no observable
  behavior (doc-only clarifications, non-validating schema annotations,
  implementation-hardening recommendations — notably the kind of
  annotation-only change SEP-2577's deprecations are).
- **The requirement.** (1) a scenario tagged with the SEP number is
  merged into the [conformance repository](https://github.com/modelcontextprotocol/conformance),
  targeting its draft spec-version tag; (2) a structured traceability
  file `sep-NNNN.yaml` accompanies it; (3) the scenario passes against
  the SEP's reference implementation.
- **The traceability file** maps each normative requirement to a
  `check` ID, or documents an `excluded` reason. Exclusions come in two
  flavours: *framework gaps* (observable but not yet expressible —
  must link a tracking `issue`) and *not-protocol-observable* (client
  rendering, internals — needs only a reason). A SEP whose requirements
  are *all* the second kind is exempt and needs no scenario. SHOULD
  checks report as warnings, not failures; MAY needs no row.
- **Specification text is authoritative.** Where a test and the spec
  disagree, the spec wins and the test is a bug. A `disputed` label
  parks a contested test out of tier assessments until resolved.
- **Test stability and tiering.** Tier assessments (SEP-1730) run
  against a **pinned conformance release**, not the tip of the repo, so
  new checks do not retroactively change an SDK's tier between tiering
  waves.

SEP-2484 **supersedes SEP-1627** (the older golden-trace conformance
proposal), adopting the scenario-and-checks model instead.

### 1.3 SEP-1730 / PR #1777 — the SDK tier system

[PR #1777 — "SEP-1730: SDK tiers definition"](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1777)
(Inna Harper, `final`, MERGED) documents the SDK Tiering System whose
substance lives at
[/community/sdk-tiers](https://modelcontextprotocol.io/community/sdk-tiers)
and is summarised on the [SDK index page](https://modelcontextprotocol.io/docs/sdk).
SDKs are classified into three tiers by feature completeness,
maintenance commitment, and documentation quality:

- **Tier 1 — Fully Supported.** 100% conformance pass rate; ships new
  protocol features before (or by) a new spec version's release on a
  per-release agreed timeline; issue triage within 2 business days;
  critical (P0) bugs fixed within 7 days; a stable release with clear
  versioning; comprehensive docs; published dependency and roadmap
  policies.
- **Tier 2 — Commitment to Full Support.** 80% conformance; new
  features within 6 months; triage within a month; critical bugs in two
  weeks; at least one stable release.
- **Tier 3 — Experimental.** No minimum conformance; no timeline
  commitments.

The conformance score is computed against **applicable required tests
only** — tests for the spec version the SDK targets, excluding pending,
skipped, experimental-feature, and legacy back-compat tests (unless the
SDK claims legacy support). Experimental features (Tasks) and
extensions (MCP Apps) are explicitly **not required for any tier**.
Tier advancement is requested by the SDK maintainer and approved by the
SDK Working Group; **relegation** happens automatically if conformance
tests on the latest stable release fail continuously for four weeks
(Tier 1 → 2 on any failure; Tier 2 → 3 above 20% failures).

The current published tier list matters to muster directly: the
official **Go SDK (`modelcontextprotocol/go-sdk`) is Tier 1**, TypeScript,
Python, C# are Tier 1; Java and Rust are Tier 2.

### 1.4 The conformance suite repository

The [modelcontextprotocol/conformance](https://github.com/modelcontextprotocol/conformance)
repository is "a framework for testing MCP client and server
implementations against the specification." Its shape is strikingly
close to muster's own BDD framework, which is why §3 can map them
directly:

- It runs in two modes. **Server testing** connects to a running
  server as an MCP client (`npx @modelcontextprotocol/conformance
  server --url http://localhost:3000/mcp`), sends requests, and runs
  checks against the responses. **Client testing** starts a test
  server per scenario, runs the client implementation against it, and
  captures the protocol interactions.
- **Scenarios** live in `src/scenarios/<scenario-name>/` and implement
  a `Scenario` interface (`start()`, `stop()`, `getChecks()`);
  **checks** are conformance validation functions. Scenarios are tagged
  by SEP number (e.g. `JsonSchema2020_12Scenario` for SEP-1613).
- **Suites and spec-version filtering.** `--suite` selects `core`,
  `extensions`, `backcompat`, `auth`, `metadata`, or `draft`
  (scenarios targeting the in-progress draft spec); `--spec-version`
  filters by spec version, with `draft` aliased to the current draft
  identifier (`DRAFT-2026-v1`).
- **Expected-failures baseline.** An SDK that does not yet pass
  everything can supply a YAML baseline (`conformance-baseline.yml`)
  listing known failures by mode; CI then passes on known failures,
  fails on new regressions, and fails on *stale* entries (a fixed test
  still listed). This is exactly the mechanism muster would use to
  adopt the suite incrementally.
- A **composite GitHub Action** (`modelcontextprotocol/conformance@v0.1.11`)
  and an `sdk` subcommand (clone an SDK at a ref, build, run) make it
  drop-in for CI.

### 1.5 The SEP guidelines, updated

The [SEP guidelines](https://modelcontextprotocol.io/community/sep-guidelines)
now encode SEP-2484's gate in the workflow itself: the
`Accepted → Final` edge reads "Reference implementation + conformance
test complete," the `accepted` status means "Approved, awaiting
implementation + conformance," and `final` means "Complete with
implementation and conformance." A new "Conformance Test Requirement"
section restates the scope, the traceability-file requirement, and the
sponsor/maintainer/author split. The SEP types (Standards Track,
Informational, Process) and the prototype requirement for acceptance
are unchanged.

## 2. Linked SEPs and PRs

- Announcement, "How the Protocol Evolves From Here":
  https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/
- SEP-2596 — Specification Feature Lifecycle and Deprecation Policy:
  https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2596
- SEP-2484 — Require Conformance Tests for Standards Track SEPs to Reach
  Final Status:
  https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2484
- PR #1777 — SEP-1730 SDK tiers definition:
  https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1777
- SDK tier docs: https://modelcontextprotocol.io/docs/sdk and
  https://modelcontextprotocol.io/community/sdk-tiers
- Conformance suite repository:
  https://github.com/modelcontextprotocol/conformance
- SEP guidelines: https://modelcontextprotocol.io/community/sep-guidelines
- SEP-2133 — Extensions framework (the third governance lever):
  https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133

## 3. Muster impact

The plan correctly flags this section as "mostly process/strategy."
Muster is a **host-and-aggregator, not an SDK**, so the SDK tier
percentages do not score muster directly. But two of the three
governance instruments produce concrete, trackable work:

1. SEP-2596 gives muster a vocabulary and a discipline for tracking the
   Active/Deprecated/Removed state of every MCP feature it forwards or
   implements.
2. SEP-2484's conformance suite is now a usable, authoritative test
   target for muster's two MCP roles, and muster's BDD scenarios in
   [internal/testing/scenarios/](../../../internal/testing) are the
   natural place to align with it.

### 3.1 Muster has two MCP roles, both now testable against the suite

The conformance suite tests an MCP **server** (connect as a client,
send requests) and an MCP **client** (start a test server, run the
client). Muster is both:

- **Muster-as-server.** The aggregator
  ([internal/aggregator/server.go](../../../internal/aggregator/server.go))
  exposes muster's own tools and the aggregated upstream tools over
  Streamable HTTP / SSE / stdio. The conformance `server` mode points
  at a `muster serve` endpoint exactly the way the suite's `--url`
  flag expects. Every wire-level change in the other section docs
  (the stateless transport in
  [01-stateless-protocol.md](01-stateless-protocol.md), JSON Schema
  2020-12 in [07-json-schema-2020-12.md](07-json-schema-2020-12.md),
  the auth hardening in
  [05-authorization-hardening.md](05-authorization-hardening.md)) is
  validated server-side by this mode.
- **Muster-as-client.** The outbound MCP clients
  ([internal/mcpserver/client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go),
  [client_sse.go](../../../internal/mcpserver/client_sse.go),
  [client_stdio.go](../../../internal/mcpserver/client_stdio.go),
  [client_dynamic_auth.go](../../../internal/mcpserver/client_dynamic_auth.go),
  [client_interface.go](../../../internal/mcpserver/client_interface.go))
  connect *out* to upstream MCP servers. The conformance `client` mode
  is what validates that muster, acting as a client, speaks the
  stateless transport, sends `_meta`, sets `Mcp-Method` / `Mcp-Name`,
  and handles `InputRequiredResult`.

Both roles ultimately run on the **`mark3labs/mcp-go` SDK**, which is
*not* one of the official SDKs in the tier table — the official Go SDK
is `modelcontextprotocol/go-sdk` (Tier 1). This is the single most
important strategic fact in this document and is picked up again in §5
and in [01-stateless-protocol.md](01-stateless-protocol.md): the tier
system gives muster a quality signal for the *official* SDKs but not
for the SDK muster actually depends on. Whatever conformance gaps
`mcp-go` has, muster inherits.

### 3.2 The BDD scenarios are muster's local conformance suite

Muster already has a mature, scenario-based test framework that is
structurally a sibling of the upstream conformance suite:

- 129 YAML scenarios in
  [internal/testing/scenarios/](../../../internal/testing), each with
  a `name`, `category` (`behavioral` / `integration`), `concept`
  (`workflow` / `mcpserver` / `service`), `tags`, and a list of
  `steps` that invoke `core_*` tools and assert `expected` outcomes
  (see [internal/testing/types.go](../../../internal/testing/types.go)
  for the `TestScenario` / `TestStep` types and
  [workflow_basic.yaml](../../../internal/testing/scenarios/workflow_basic.yaml)
  for the canonical shape).
- Each scenario runs in its own isolated `muster serve` instance via
  [cmd/test.go](../../../cmd/test.go) (`muster test --scenario <name>
  --verbose`), mirroring the suite's per-scenario server lifecycle.
- The framework already has the two ingredients SEP-2484 formalises:
  a **schema** (`muster test --generate-schema`) and a
  **validation pass** (`muster test --validate-scenarios`) — see the
  flags in [cmd/test.go](../../../cmd/test.go) (lines 41–45, 111–112,
  139–144). That is muster's analogue of the conformance harness's
  spec-version-aware checks.

The architecture rule that "BDD scenarios are truth — if a scenario
fails, fix the code, not the scenario" (`CLAUDE.md`,
[.cursor/rules/architecture.mdc](../../../.cursor/rules/architecture.mdc))
is the same posture SEP-2484 takes toward the upstream suite
("specification text is authoritative; the test is a bug only if it
contradicts the spec"). The difference is the *source of truth*:
muster's scenarios encode muster's intended behaviour; the upstream
suite encodes the spec. For the wire-level surfaces muster exposes,
those should converge.

Concretely, the alignment work is:

- **Tag muster scenarios by SEP where they exercise a spec behaviour.**
  The existing scenarios already cluster by concept; several
  (`mcpserver-streamable-http-tool-call-lifecycle.yaml`,
  `mcpserver-sse-tool-call-lifecycle.yaml`, the `oauth-*` family) map
  onto specific upstream SEPs. Adding a `sep:` tag (e.g.
  `["sep-2243", "transport"]`) makes the correspondence auditable, the
  same way the upstream traceability file makes SEP coverage auditable.
- **Add stateless-transport and JSON-Schema scenarios** as those
  changes land, so muster's local suite tracks the spec changes the
  same way SEP-2484 makes the upstream suite track them. The
  cross-references already exist: see the conformance follow-ups noted
  in [07-json-schema-2020-12.md](07-json-schema-2020-12.md) §4 and
  [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md)
  §4 item 7 (assert the aggregator does *not* advertise the deprecated
  `logging` capability).
- **Run the upstream suite against `muster serve` in CI** using the
  composite GitHub Action in `server` mode, with an
  `expected-failures` baseline for the surfaces muster does not yet
  implement. This gives muster an external, spec-derived signal that
  complements the internal BDD suite — and the baseline's stale-entry
  detection means a surface muster *starts* supporting forces the
  baseline to be trimmed.

### 3.3 Where the lifecycle policy lands in muster

SEP-2596 is a policy, not a wire change, so the muster impact is a
**place to record state** and a **discipline for adopting/retiring**:

- Muster forwards a large, opaque surface (every upstream tool,
  resource, prompt) and exposes its own `core_*` / `workflow_*` /
  `x_*` tools. As upstream features move Active → Deprecated → Removed,
  muster must know which state each one is in to decide whether to keep
  forwarding it. The aggregator's capability layer
  ([internal/aggregator/registry.go](../../../internal/aggregator/registry.go),
  where the `Capabilities` type and `refreshServerCapabilities` live)
  is where upstream capabilities are observed; it is the natural home
  for any "this upstream advertises a Deprecated capability" signal
  (the diagnostics tool floated in
  [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md)
  §5).
- The twelve-month window means muster never has to rush: a feature
  muster forwards stays forwardable for at least a year after its
  deprecation SEP is Final, and removal is a deliberate Core Maintainer
  decision, not a timer. Muster's adoption planning (the dates in
  [09-release-timeline.md](09-release-timeline.md)) can therefore
  treat deprecations as advisory and removals as the only hard
  deadlines.
- The `deprecated.mdx` registry is the upstream source muster should
  *watch* rather than reimplement. The
  `docs/explanation/mcp-2026-07-28/` series is itself muster's
  first pass at a feature-state record; keeping it current against the
  upstream registry is the lightweight version of "where to record
  muster's policy."

## 4. Required changes / migration notes

These are issues to file once all the section docs in this folder land.
None require code changes to adopt the governance policy itself; they
set up the *process and test scaffolding* that the wire-level docs
(01, 05, 07) then plug into. Ordered so earlier items unblock later
ones.

1. **Adopt the upstream conformance suite in CI (server mode).** Add a
   CI job that runs `modelcontextprotocol/conformance@<pinned>` in
   `server` mode against a `muster serve` endpoint, with a checked-in
   `conformance-baseline.yml` listing the surfaces muster does not yet
   implement (initially most of the `draft` suite). Pin the conformance
   release (per SEP-2484's "pinned conformance release" rule) so the
   baseline is stable between intentional upgrades. This is the single
   highest-leverage item: it gives every other 2026-07-28 doc an
   external pass/fail signal.

2. **Adopt the conformance suite in client mode for the outbound
   clients.** A second CI job in `client` mode exercises
   [internal/mcpserver/](../../../internal/mcpserver) client code
   against the suite's per-scenario test servers, validating
   muster-as-client behaviour (stateless transport, `_meta`, headers,
   `InputRequiredResult`). Depends on the
   [01-stateless-protocol.md](01-stateless-protocol.md) client work.

3. **Introduce a `sep:` tag convention in the BDD scenarios.** Extend
   the scenario `tags` convention so any scenario exercising a
   spec-defined behaviour carries its SEP number (e.g. `sep-2243`,
   `sep-2106`). No schema change is required — `tags` is already a free
   list (see [internal/testing/types.go](../../../internal/testing/types.go)).
   This makes muster's own SEP coverage auditable the way the upstream
   traceability file does, and lets `muster test --concept`/tag
   filtering select "all scenarios for SEP-N".

4. **Add a lightweight muster traceability record.** Mirror SEP-2484's
   traceability idea at muster's scale: a single
   `docs/explanation/mcp-2026-07-28/conformance-coverage.md` (or a
   front-matter block per scenario) that maps each adopted SEP to the
   muster scenario(s) that cover it and explicitly lists what muster
   *excludes* (e.g. MCP Apps rendering — not protocol-observable from
   muster; see [03-mcp-apps.md](03-mcp-apps.md)). This is the
   "where to record muster's policy" deliverable.

5. **Write the deprecated-capability guard scenarios.** Implement the
   scenarios already promised by the other docs: assert muster's
   `server/discover` response does not advertise
   `ServerCapabilities.logging`, and that outbound connections do not
   negotiate `ClientCapabilities.roots` / `.sampling`
   ([06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md)
   §4 item 7). These are the cheapest regression guards and double as
   muster's first SEP-tagged lifecycle scenarios.

6. **Decide muster's `mcp-go` tier-tracking policy.** Because muster
   depends on `mark3labs/mcp-go` (not the Tier 1
   `modelcontextprotocol/go-sdk`), record a standing decision: track
   `mcp-go`'s conformance posture against the `2026-07-28` suite, and
   evaluate whether/when muster should migrate to the official Go SDK
   to inherit its Tier 1 conformance guarantees. This is a strategic
   dependency decision, not a code task, but it gates how much
   transport/auth work muster has to do itself versus get from the SDK
   (the same open question raised in
   [01-stateless-protocol.md](01-stateless-protocol.md) and
   [05-authorization-hardening.md](05-authorization-hardening.md)).

7. **Document the Extensions escape hatch as policy.** Per the
   announcement, new capabilities should ship as opt-in extensions and
   stabilise there before (if ever) entering the core spec. Record that
   any muster-specific protocol behaviour that is not yet a spec feature
   should be modelled as an extension under a muster vendor prefix (per
   [02-extensions-first-class.md](02-extensions-first-class.md)),
   never as a non-standard core deviation. This keeps muster
   forward-compatible with the lifecycle policy.

8. **Add a CHANGELOG / release-note discipline tied to the lifecycle.**
   When muster adopts a `2026-07-28` surface, the CHANGELOG entry should
   name the SEP and its lifecycle state, so downstream operators can map
   muster behaviour onto the upstream Active/Deprecated/Removed registry
   without reading the spec. (Mirrors the CHANGELOG framing in
   [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md)
   §4 item 8.)

## 5. Open questions

- **Migrate to `modelcontextprotocol/go-sdk`, or stay on `mcp-go`?**
  The official Go SDK is Tier 1 and therefore carries a 100%
  conformance commitment and a "ship before the spec release" timeline
  obligation; `mark3labs/mcp-go` carries neither. Migrating would let
  muster inherit conformance for free but is a large, cross-cutting
  change touching every file in
  [internal/aggregator/](../../../internal/aggregator) and
  [internal/mcpserver/](../../../internal/mcpserver). Staying means
  muster must validate the `mcp-go` surface against the conformance
  suite itself (items 1–2 above). This decision should be made once,
  explicitly, and recorded — it is the biggest single lever over how
  much of the `2026-07-28` work muster owns.

- **Is the upstream suite's `draft` tag stable enough to gate muster
  CI before the Final spec ships on 2026-07-28?** The release candidate
  is locked as of May 21, 2026 but the Final spec is July 28, 2026. If
  muster wires the `draft` suite into a *blocking* CI gate too early it
  risks churn as draft scenarios change. The likely answer is to run
  the suite non-blocking (report-only) until the Final spec, then flip
  to blocking — but this should be decided in
  [09-release-timeline.md](09-release-timeline.md).

- **How granular should muster's own SEP traceability be?** SEP-2484
  requires a row per MUST/MUST NOT/SHOULD. Muster is not bound by that
  (it is not authoring SEPs), so the question is whether per-SEP
  coverage (item 4) is enough or whether muster should track per-MUST
  coverage for the surfaces it exposes. Per-SEP is almost certainly the
  right granularity for a host-and-aggregator; worth stating so it is
  not re-litigated per scenario.

- **Should muster contribute scenarios upstream?** Muster's BDD suite
  has unusually thorough multi-user / session-isolation and OAuth
  scenarios (e.g.
  [mcpserver-family-cross-session-corruption.yaml](../../../internal/testing/scenarios/mcpserver-family-cross-session-corruption.yaml),
  the `oauth-sso-*` family). Some of these exercise spec behaviour the
  upstream suite may not yet cover. The conformance repo welcomes
  scenario contributions for existing spec behaviour (not tied to a new
  SEP). Whether muster invests in upstreaming any of these — improving
  the ecosystem and reducing muster's own maintenance — is an open
  strategic call.

- **Where does the "deprecated upstream capability" signal live?**
  §3.3 and [06-…](06-deprecations-roots-sampling-logging.md) §5 both
  point at a `core_diagnostics_deprecated_capabilities` tool, but that
  is a feature, not a governance decision. The governance question is
  whether muster *commits* to surfacing the upstream `deprecated.mdx`
  state to operators at all, or treats it as out of scope and leaves
  operators to read the spec registry themselves.

## 6. References

- [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
  — announcement, section "How the Protocol Evolves From Here" (the
  three governance SEPs framing) and "Release Timeline and Validation"
  (the Tier 1 "ship within the window" expectation).
- [SEP-2596 — Specification Feature Lifecycle and Deprecation Policy (#2596)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2596)
  — accepted, MERGED, Process. Active/Deprecated/Removed states, the
  twelve-month minimum window measured from revision release, the
  deprecation-is-a-SEP / removal-is-not split, expedited removal, Tier 1
  SDK obligations, and the `deprecated.mdx` registry. Fetched via
  `gh pr view 2596` / `gh pr diff 2596`.
- [SEP-2484 — Require Conformance Tests for Standards Track SEPs to Reach Final Status (#2484)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2484)
  — accepted, MERGED, Process. The `Accepted → Final` conformance gate,
  observable-behavior scope, the `sep-NNNN.yaml` traceability file,
  exclusion flavours, disputes, and pinned-release tiering. Supersedes
  SEP-1627. Fetched via `gh pr view 2484` / `gh pr diff 2484`.
- [PR #1777 — SEP-1730 SDK tiers definition](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1777)
  — final, MERGED. The three-tier model, conformance thresholds (100% /
  80% / none), maintenance commitments, advancement and relegation.
  Fetched via `gh pr view 1777` / `gh pr diff 1777`.
- [SDK Tiering System (/community/sdk-tiers)](https://modelcontextprotocol.io/community/sdk-tiers)
  and [SDK index (/docs/sdk)](https://modelcontextprotocol.io/docs/sdk)
  — the published tier requirements table and the current tier
  assignments (Go SDK Tier 1; `mark3labs/mcp-go` is not listed).
- [Conformance suite repository (modelcontextprotocol/conformance)](https://github.com/modelcontextprotocol/conformance)
  — README: server/client test modes, scenario + check structure,
  suites and `--spec-version` filtering, the `expected-failures`
  baseline mechanism, the composite GitHub Action, and the `sdk` /
  `tier-check` subcommands.
- [SEP guidelines (/community/sep-guidelines)](https://modelcontextprotocol.io/community/sep-guidelines)
  — the updated `Accepted → Final` workflow with the conformance gate,
  the status table, SEP types, and the "Conformance Test Requirement"
  section.
- [Kubernetes deprecation policy](https://kubernetes.io/docs/reference/using-api/deprecation-policy/)
  and [RFC 8996](https://www.rfc-editor.org/rfc/rfc8996) — prior art
  SEP-2596 cites for feature-level deprecation alongside release
  versioning.
- Cross-section context within `docs/explanation/mcp-2026-07-28/`:
  - [01-stateless-protocol.md](01-stateless-protocol.md) — the
    wire-level changes the conformance `server`/`client` modes validate.
  - [02-extensions-first-class.md](02-extensions-first-class.md) —
    SEP-2133, the "ship it as an extension" governance lever.
  - [03-mcp-apps.md](03-mcp-apps.md) — an example of a
    not-protocol-observable surface muster would *exclude* from its
    traceability record.
  - [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md)
    — SEP-2596 terminology source and the deprecated-capability guard
    scenarios.
  - [07-json-schema-2020-12.md](07-json-schema-2020-12.md) — a
    concrete SEP (SEP-2106) whose conformance scenarios muster should
    track.
  - [09-release-timeline.md](09-release-timeline.md) — where the
    blocking-vs-report-only CI decision and the registry-watch
    follow-up belong.
- Muster code paths cited in this document:
  [internal/aggregator/server.go](../../../internal/aggregator/server.go),
  [internal/aggregator/registry.go](../../../internal/aggregator/registry.go),
  [internal/mcpserver/client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go),
  [internal/mcpserver/client_sse.go](../../../internal/mcpserver/client_sse.go),
  [internal/mcpserver/client_stdio.go](../../../internal/mcpserver/client_stdio.go),
  [internal/mcpserver/client_dynamic_auth.go](../../../internal/mcpserver/client_dynamic_auth.go),
  [internal/mcpserver/client_interface.go](../../../internal/mcpserver/client_interface.go),
  [internal/testing/types.go](../../../internal/testing/types.go),
  [internal/testing/scenarios/](../../../internal/testing),
  [internal/testing/scenarios/workflow_basic.yaml](../../../internal/testing/scenarios/workflow_basic.yaml),
  [internal/testing/scenarios/mcpserver-family-cross-session-corruption.yaml](../../../internal/testing/scenarios/mcpserver-family-cross-session-corruption.yaml),
  [cmd/test.go](../../../cmd/test.go).
