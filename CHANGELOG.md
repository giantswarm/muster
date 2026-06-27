# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Fixed

- Workflow listing no longer rebuilds the session tool set once per workflow. `getWorkflows` evaluated each workflow's availability independently, and every check resolved the caller's full session-scoped tool set (`GetAllToolsForSession` across all backend MCP servers) from scratch — an O(workflows) blow-up that made `core_workflow_list` take ~30 s for ~280 workflows. The list path now installs a request-scoped memo (`api.SessionToolMemo`) so the session tool set is resolved once for the whole request and shared across all per-workflow availability checks. Single-workflow paths are unchanged (no memo, same per-call behavior).

- M2M (no-actor) local-mint exchanges now mint with the granted subject. A workload group grant's `granted.subject` was dropped on the local-mint broker path, so an impersonating exchange minted the workload ServiceAccount's own `sub` instead of the configured impersonated user (granted groups were already applied). The broker now forwards the granted subject to the local-mint provider.

- local-mint backends are now treated as session-based for tool registration. `isServerSSOBased` did not recognize the local-mint auth mode, so the aggregator event handler attempted global registration for a local-mint backend (which has no global persistent client), failed on 401, and the backend's tools never reached the caller. local-mint now joins token forwarding and token exchange as a per-session auth mode.

- localMint backends connected during SSO bootstrap now mint on the on-behalf-of delegation path instead of falling back to M2M. When `initSSOForSession` rebuilt its detached background context it carried the subject bearer and ID token but dropped the inbound `X-Actor-Token`, so a bootstrap-established connection minted with no actor and the broker authorized the per-backend exchange on the human subject (`token_exchange_audience_not_allowed`) rather than the agent ServiceAccount. The per-request credential tokens (subject bearer, actor token, ID token) are now carried into the bootstrap context as one unit, so the bootstrap and live-request paths mint identically.

- Connected MCP clients now receive `notifications/{tools,resources,prompts}/list_changed` when a backend connects after the session was opened (localMint/OBO background bootstrap, post-restart re-init), so late-connecting backends no longer stay invisible for the session's lifetime.

- OBO sessions now connect localMint backends. After the emission fix that adds `email` to muster-minted OBO JWTs, `fireOnAuthenticated` fires for OBO requests, but the `onAuthenticated` callback returned early at the `idToken == ""` guard (written to avoid 403-spam for post-restart Dex sessions). The guard is now narrowed: OBO sessions (detected via `userInfo.ActorSubject`) are allowed through. The inbound OBO bearer is threaded into the detached `initSSOForSession` background context and used as the RFC 8693 subject token in `EstablishConnectionWithLocalMint`, falling back from the Dex ID-token lookup when none is present.

- On-behalf-of tool calls now reach the backend instead of failing closed. A muster self-issued OBO bearer (`sub=human`, `act=agent`) re-presented at the front door is signature-validated with no token-store entry, so it previously carried no session ID; backend tools that require session auth then rejected it and the connection fell back to the agent ServiceAccount. Bumping mcp-oauth to v0.18.0 makes the `ValidateToken` middleware assign a session to every validated token (`FamilyID` when present, else the deterministic bearer-derived ID), so the existing session lookup resolves the OBO bearer and the per-backend localMint runs as the human. No muster code change.

### Added

- `oauth.server.trustedIssuers[].allowPrivateIPJWKSHosts` (`[]string`): host-scoped alternative to `allowPrivateIPJWKS`. The issuer's `jwksUrl` may resolve to a private IP only when its hostname matches one of these values; all other hosts keep the SSRF guard. Maps to mcp-oauth's `TrustedIssuer.AllowPrivateIPJWKSHosts`. Prefer it over the blanket bool for a known in-cluster JWKS endpoint (e.g. a Dex fronted by an internal LB whose public hostname resolves to a private VIP).

- `oauth.server.tokenExchangeBroker.delegateToSelf` (default false): when enabled, a delegated (on-behalf-of) token exchange that carries an `actor_token` but omits the RFC 8707 `resource` is bound to muster's own `resourceIdentifier`, so an agent STS client that cannot set a resource itself still receives a token muster accepts back and re-mints per backend on the localMint path. Only the delegation path is affected; a resource-less plain exchange still errors. Requires mcp-oauth v0.16.0.

- Chart values now document the `muster-valkey` persistence model as a deliberate cache-only choice (RDB-only is intentional; every critical record is reconstructable at startup), with a per-record-type recovery table, and explain why AOF is not enabled (it does not survive PVC loss, and a config-flip restart is a data-loss footgun). Documentation only; no behaviour change. ([#884](https://github.com/giantswarm/muster/issues/884))

- `workloadGroupGrants[].granted.subject`: when set, replaces the validated workload credential's `sub` in the minted token so the downstream sees a stable agent principal. `workloadGroupGrants[].granted.groups` is the new canonical location for injected groups (previously a top-level `groups` field). Both map to `oauthserver.WorkloadGrant.Granted`; requires mcp-oauth v0.14.0.

- App-owned CRDs: the `muster` application chart now ships its own `MCPServer` and `Workflow` CRDs in `helm/muster/crds/` (Helm 3 `crds/` directory), with `helm.sh/resource-policy: keep` baked in. Combined with Flux `install.crds: CreateReplace` / `upgrade.crds: CreateReplace` on the muster `HelmRelease`, the CRDs travel with the app at the same version and upgrade atomically on every release, removing CRD-vs-app drift. The standalone `muster-crds` chart is retained for non-Flux/standalone consumers; `make generate-crds` now writes both locations from the same Go-type source. Chart docs (NOTES, README, values) now explain the CRD handling for plain-Helm users: fresh `helm install` includes the CRDs automatically, while CRD upgrades must be applied out-of-band (`helm show crds giantswarm/muster | kubectl apply --server-side -f -`) because Helm does not manage `crds/` upgrades natively — an intentional, still-current Helm design decision (HIP-0011), unchanged in Helm 4. The previous bundle-split docs that claimed the app chart "no longer ships the CRDs" were corrected.
- Workflow result model overhaul ([#873](https://github.com/giantswarm/muster/issues/873), [#874](https://github.com/giantswarm/muster/issues/874), [#875](https://github.com/giantswarm/muster/issues/875)): step result referencing, LLM-facing output, and result shaping are now decoupled and use one expression language.
  - **Referencing decoupled from output ([#873](https://github.com/giantswarm/muster/issues/873)).** Every step result is now always referenceable by later steps as `{{ .results.<id>.<field> }}` — regardless of any flag — so chaining a single value from one step into the next no longer forces that step's entire result into the response. A new per-step/sub-step `output` flag controls whether a result is included in the returned document; `store` remains as a deprecated, backwards-compatible alias. `forEach`, `parallel`, and failure-path results are all referenceable consistently. Workflows that still use `store` now log a one-line deprecation warning naming the affected steps — both on the structured create/validate path and on the CRD reconciler (so a `kubectl apply`-ed workflow is nudged too).
  - **Output template ([#874](https://github.com/giantswarm/muster/issues/874)).** A workflow may declare a workflow-level `output` template: a templated object rendered once after all steps complete, against `.input` / `.results` / `.vars`, and returned in place of the default `{execution_id, workflow, status, input, steps[], ...}` response. It can select nested fields and combine values across steps while preserving JSON structure (numbers stay numbers, arrays stay arrays), letting a workflow return a small, shaped response. When omitted, the default response is returned unchanged. Because the output template replaces the response, per-step `output`/`store` flags no longer affect the returned document when an output template is declared; this is flagged with a one-line authoring warning naming the now-inert flags. **Type preservation (no lossy coercion):** an output template leaf's type comes from the value it evaluates to, never from how its rendered text looks. A *bare* reference path (e.g. `"{{ .results.pods.items }}"`) keeps its original JSON type; a single-action *computed* leaf keeps the real type of its result, so a numeric expression like `"{{ len .results.events.items }}"` is a number while a computed string keeps its exact string form. This means values whose form matters — versions, IDs, zero-padded values like `"08"` or `"1.20"` — are preserved as-is, with no coercion and no `quote` workaround. A leaf that mixes literal text with actions (e.g. `"v{{ .v }}"`) renders to a string. Non-finite values (`NaN`/`Inf`) are kept as strings.
  - **Unified expression language ([#875](https://github.com/giantswarm/muster/issues/875)).** Condition `jsonPath` / `expectNot.jsonPath` paths now use the same path navigator as step args and templates, gaining array indexing (e.g. `items[0].name`) and an optional full Go-template form where the result is exposed as `.result` (e.g. `"{{ (index .result.items 0).name }}"`). Existing dotted paths keep working. The duplicate `engine.resolvePath` and `getValueFromPath` navigators were collapsed onto a single implementation.
  - **Debug response escape hatch and non-discarding output-template errors ([#877](https://github.com/giantswarm/muster/issues/877)).** A workflow that declares an `output` template can now be inspected without temporarily removing the template: pass the reserved `_debug: true` execution argument and the full response (`execution_id`, `status`, and `steps[]` with **every** recorded step result, not just output-flagged ones) is returned with the rendered output template alongside it under `output`. The `_debug` arg is stripped before validation and step execution, so it never collides with a workflow's own arguments and is not passed to step tools. Separately, an output-template render error no longer discards the results of steps that already succeeded: the workflow still fails loud (the error is returned and the result is flagged `isError`), but the response now carries every recorded step result plus an `output_error` message, so the underlying data stays recoverable for debugging an output-template typo. Default (non-debug, template-renders-cleanly) behaviour is unchanged: only the rendered output template is returned when `output` is set, the default response otherwise.

- Cheap, ranked, faceted tool discovery tier ([#868](https://github.com/giantswarm/muster/issues/868)): `filter_tools` is now a discovery tier distinct from execution, so finding a tool no longer scales with the full descriptive weight of every candidate. Against a ~280-workflow fleet a broad `filter_tools(pattern="*workflow*")` call returns a bounded summary page (~3 KB) instead of the full-catalogue dump (~330 KB) — measured ~100x smaller.
  - **Bounded + summarised pages.** `filter_tools` gains `limit` (default 25) and `offset`, and the response carries `total` and a `truncated` flag. Discovery now defaults to a one-line `summary` per tool with no input schema; the authoritative full description and schema remain available via `describe_tool`, or by passing `include_schema=true`.
  - **Ranked query mode.** A new `query` argument relevance-ranks matches with a dependency-free lexical ranker (Okapi BM25 over name + summary) and returns them best-first with a `score`, dropping non-matching tools. Lexical ranking needs no embedding index; embeddings can be a later upgrade.
  - **Label facets.** `Workflow` CRD `metadata.labels` are now propagated onto the workflow's execution tool and can be filtered in discovery via a `labels` facet (key=value; all must match), letting clients scope a lookup to a labelled subset.
  - **Agent REPL parity.** The `filter` command (aliases `find`/`search`) now exposes the discovery tier: options are given as `key=value` pairs (`pattern`, `description`, `query`, `labels` as `k=v,k2=v2`, `case_sensitive`, `detailed`, `limit`, `offset`), with a bare token still accepted as the name pattern. It now reports the page size against the total match count and the catalogue size, and prints the next `offset` when more matches exist (previously it mislabelled the page size as the match count and hid truncation). Unknown options and stray extra arguments now fail loudly with the list of valid keys instead of being silently ignored, the name column auto-sizes to the longest tool name on the page, and tab-completion only offers options that have not been supplied yet.
  - Existing `list_tools` / `call_tool` / `describe_tool` behaviour is unchanged, as is `list_core_tools` (which keeps full descriptions and schemas).

- Workflow control flow ([#865](https://github.com/giantswarm/muster/issues/865)): four genuinely useful constructs are now implemented end to end (CRD types, internal API types, executor, structured create/validate path, and step JSON schema):
  - `condition.template` — a boolean Go-template gate evaluated in-process (e.g. `condition: {template: "{{ eq .input.env \"production\" }}"}`).
  - `forEach` — a sequential loop that runs a flat body of sub-steps once per item of a list, binding the current item to `{{ .vars.<as> }}` (default `item`) and the index to `{{ .vars.<as>_index }}`. A stored sub-step is addressable per iteration as `{{ .results.<id>_<index> }}` (the plain `{{ .results.<id> }}` keeps the last iteration's result).
  - `parallel` — a group of sub-steps executed concurrently; siblings are independent (each resolves arguments from the pre-group state).
  - `spec.onFailure` — best-effort cleanup/rollback sub-steps run when the workflow fails on a step that does not allow failure.
  - The "exactly one of `tool`, `forEach`, or `parallel`" rule is enforced by the CRD itself via a CEL validation rule, so a malformed step is rejected at `kubectl apply` time, not only through the structured create path.
  - Step/sub-step conditions are now validated structurally on every authoring path: a condition must set **exactly one** of `template`, `tool`, or `fromStep`, and a `tool`/`fromStep` condition must declare `expect` or `expectNot`. Both rules are enforced at `kubectl apply` time via CEL on the `Workflow` CRD and in the structured `workflow_create`/`workflow_validate` path. Previously a `tool`/`fromStep` condition without an expectation silently fell back to "expect the call to fail", and a kubectl-applied condition was not checked at all.
- `GET /health` now responds 200 on the aggregator port regardless of OAuth configuration, so Kubernetes liveness/readiness probes work without patching the chart.
- `RegisterServer` and `DeregisterServer` aggregator events and MCPServer reconcile entry are now logged at Info level, making freshly-restarted pod lifecycle visible without `--debug`.
- `oauth.server.allowedOrigins` (comma-separated) is now wired into the mcp-oauth CORS `AllowedOrigins` list. Previously declared but never read; empty value keeps CORS disabled (default).
- `oauth.server.trustedIssuers[].acceptedTypHeaders`: accepted JWT `typ` header values for Bearer tokens from a trusted issuer. Empty keeps the RFC 9068 default (`at+jwt`). Kubernetes ServiceAccount tokens carry no `typ` header; use `[""]` to accept them.
- `oauth.server.trustedIssuers[].subjectClaim`: sources the canonical subject (the `sub` of any token minted from the identity, and the value matched by `actorDelegationPolicy`) from a claim other than `sub`. Empty keeps the standard `sub`. Set it to `email` for Dex, whose `sub` is opaque, so an OBO token minted from a Dex subject token carries the user's email.
- Brokered RFC 8693 token exchange ([#831](https://github.com/giantswarm/muster/issues/831)): external confidential clients can POST a token-exchange request with an `audience` parameter to `/oauth/token` and receive a token minted by the audience's downstream Dex. New `oauth.server.tokenExchangeBroker` config block (per-client audience allowlist, audience → downstream Dex target mapping with per-target scopes and credential secret refs). Requires mcp-oauth >= v0.3.0; subject tokens are validated against `trustedIssuers`.
- `oauth.server.tokenExchangeBroker.workloadAudiences`: per-workload allowlist for workload-authenticated RFC 8693 token exchange (no confidential-client credentials). Keys are workload subjects (`system:serviceaccount:<ns>:<name>`; globs supported), values are the audiences each workload may request. Delegation uses the actor subject; impersonation uses the subject token's sub claim. Enforcement is performed by mcp-oauth before the credential provider is invoked.
- `oauth.server.tokenExchangeBroker.targets[].type`: credential provider discriminator for broker targets. Defaults to `oidc-exchange` (downstream Dex RFC 8693 exchange) when omitted; additional provider types will be added in future releases.
- `oauth.server.tokenExchangeBroker.targets[].type: github-app` mints GitHub App installation tokens. Configure via `githubApp.appId`, `githubApp.installationId` (or `githubApp.owner` + `githubApp.repo` for auto-discovery), `githubApp.privateKeyRef` (RSA PEM in a Kubernetes Secret), and optional `githubApp.repositories` / `githubApp.permissions` scope restriction.
- `oauth.server.tokenExchangeBroker.targets[].type: local-mint` mints a muster-signed RFC 9068 JWT locally. Requires `enableJWTMode: true`. The issued token carries `sub` = the validated human subject, the subject's `email` and `groups` claims (plus any broker-granted groups), and `act` = the validated agent SA nested over any prior delegation chain on the subject token, signed by muster's own access-token key. Downstream services that trust muster as an issuer receive the subject's identity and the full delegation chain without a separate Dex exchange, so group-to-tenant and email-to-org routing resolve correctly.
- `oauth.server.tokenExchangeBroker.actorDelegationPolicy`: allowlist of `(actorIssuer, actorSubject, subjectIssuer, subjectSubject)` entries that permit RFC 8693 delegated token exchange. An empty or absent list denies all delegated exchanges (mcp-oauth default). Required when any broker target uses `type: local-mint` and the exchange carries an `actor_token`.
- `muster events --follow` now honors `--output`: `json` streams newline-delimited JSON (one object per line, ready for `jq`), `yaml` streams one YAML document per event, and the default table/wide formats stream an aligned one-line-per-event view. Previously the output format was silently ignored in follow mode. The human follow line now orders fields to match the static `muster events` table (timestamp, type, resource, reason, message) and highlights `Warning` events when stdout is a terminal.

### Fixed

- Kubernetes events now carry their structured detail. The `api.EventManagerHandler` boundary previously dropped everything but the object name/namespace, so every rendered event message lost its contextual fields — failure events showed no error, "created with N steps" showed no count, and step events had empty step IDs/tools. A new `CreateEventWithData` carries structured `EventData` (error, step count, duration, step ID/tool, execution ID, condition result, tool names) end-to-end so the message template renders the real detail; all emitters (MCPServer service/aggregator, workflow, token exchange/forwarding) were updated. The event type (Normal/Warning) is now consistently derived from the reason.
- Kubernetes events for MCPServer runtime lifecycle (start/stop/fail/health/recovery) are now associated with the MCPServer CRD in the configured muster namespace instead of being hardcoded to `default`. On a real cluster the events previously landed in `default` with an empty object UID, orphaned from the CRD and invisible to `kubectl describe mcpserver`.
- `muster events --follow` now works via real server-side streaming. It previously blocked forever waiting on an MCP notification the server never sent (the streaming path was an unimplemented stub). Follow is now implemented end-to-end: `core_events` with `follow=true` returns the events seen so far and registers a per-session watch on the aggregator; subsequent events are pushed to the client as `notifications/muster/event` MCP notifications, sourced from a native Kubernetes watch (Kubernetes mode) or an fsnotify watch on the on-disk event log (filesystem mode) — no client-side polling. The stream is torn down when the client disconnects, starts a new follow, or the server shuts down.
- localMint now refuses to mint when the subject token carries a non-empty `email` whose `email_verified` is not true, on both the delegation (`X-Actor-Token`) path and the M2M path. Previously the check ran only on the delegation path, so a pre-exchanged subject token already carrying an `act` chain (a single bearer, no actor header) could mint with an unverified email. A ServiceAccount token, which carries no email, is unaffected.
- Workflow CRD CEL cost budget ([#865](https://github.com/giantswarm/muster/issues/865) follow-up): the `Workflow` CRD from 0.8.0 was rejected by the Kubernetes API server (k8s >= 1.34) with *"x-kubernetes-validations estimated rule cost total for entire OpenAPIv3 schema exceeds budget"* — the `WorkflowStep` and `WorkflowCondition` "exactly one of" guards used a `[...].filter(x, x).size() == 1` list-comprehension form whose estimated cost, multiplied across the four nested condition sites (`steps[]`, `steps[].forEach.steps[]`, `steps[].parallel[]`, `onFailure[]`), blew the schema-wide budget (3.2x over). Rewritten to the cheap additive form `(has(self.a)?1:0) + (has(self.b)?1:0) + (has(self.c)?1:0) == 1`, which estimates far lower and applies cleanly on k8s 1.34 while enforcing identical semantics. Without this, `muster-crds` 0.8.0 cannot install and the muster 0.8.0 rollout stalls.
- Workflow documentation ([#865](https://github.com/giantswarm/muster/issues/865)): `docs/how-to/workflow-creation.md` and `docs/how-to/ai-workflow-optimization.md` were rewritten to describe only features the engine implements; all example templates now use the correct `{{ .input.<arg> }}` context (the engine renders with `missingkey=error`, so the previously documented `{{ .<arg> }}` form errored at runtime). `docs/reference/crds.md` template syntax was corrected, the stray `spec.name` removed from examples, and the dead `outputs` field replaced with `store: true` guidance.
- Documentation: removed all references to a hallucinated `muster configure ...` CLI from `docs/how-to/ai-troubleshooting.md` and `docs/how-to/ai-agent-integration.md`. muster has no configuration CLI — configuration is file-based (`~/.config/muster/config.yaml`), entities are created with `muster create`, and debugging uses `muster serve --debug`. The invented context/tool-suggestion and alert/cache/limit configuration blocks built around that command were dropped.
- Documentation: completed a repo-wide accuracy pass removing the remaining hallucinated CLI surface and config from the docs. `docs/how-to/ai-troubleshooting.md` and `docs/how-to/troubleshooting.md` were rewritten around the real command set; the invented `muster status`, `muster logs`, `muster describe`, `muster validate`, `muster restart`, `muster metrics`, `muster backup`/`restore`, `muster support-bundle`, `muster config show`, the fictional `muster serve --port/--host` flags, and the `kind: Config` logging CRD were replaced with the real equivalents (`muster get`/`list`/`check`/`call`, `muster serve --debug`/`--silent`, the `/health` endpoint, OpenTelemetry/OTLP for metrics and traces, and file-based config). The same fixes were applied in `docs/how-to/mcp-server-management.md` (`muster metrics`/`muster logs` mcpserver, and a Cursor `mcpServers` entry that used `curl` as the MCP command) and `docs/reference/events.md` (`muster restart mcpserver`).
- Documentation: corrected workflow template/feature hallucinations beyond #865 in `docs/how-to/advanced-scenarios.md`, `docs/how-to/ai-agent-integration.md`, `docs/reference/mcp-tools.md`, `docs/reference/configuration.md`, `docs/reference/api.md`, `docs/explanation/orchestration.md`, and `docs/explanation/design-principles.md`: example templates now use the real `{{ .input.<arg> }}` / `{{ .results.<step-id> }}` context (engine renders with `missingkey=error`), the unsupported `enum`/`examples`/`pattern` arg keywords and stray `spec.name`/`outputs` fields were removed, and the fabricated `spec.triggers` event-driven workflows, custom `TemplateFuncs` map (templates use the Sprig library), invented `muster_*` metric names, and non-existent per-step `retry`/`on_failure`/`error_handling` fields were replaced with the real `condition.template`, workflow-level `onFailure`, and OpenTelemetry behaviour.
- Cross-cluster RFC 8693 token exchange now requests an `id_token` (was `access_token`). The exchanged token is forwarded as the downstream bearer and must serve as the user's OIDC identity; Dex's default access token is opaque, so `mcp-kubernetes` (strict `--downstream-oauth`) could not use it for Kubernetes OIDC and denied tool calls with `authentication required: please log in to access this resource`, even though the connection reported `Connected [SSO: Exchanged]`. Requesting an `id_token` yields a JWT whose `aud` carries the configured `requiredAudiences`, so `mcp-oauth` accepts it via the forwarded-ID-token (SSO) path — mirroring the token-forwarding behaviour. `mcp-prometheus` and other identity-only downstreams were unaffected and keep working.
- localMint now carries a human on-behalf-of identity to the backend. When an agent reaches muster with a muster-issued on-behalf-of token (`sub=human, act=agent`), localMint presents it as the exchange subject and the broker re-binds it to the backend audience, preserving the delegation chain, so the backend sees `sub=human, act=agent` instead of falling back to the agent ServiceAccount. The agent ServiceAccount must be authorized for the backend audience in `tokenExchangeBroker.workloadGroupGrants`. Requires mcp-oauth v0.17.0.
- localMint connections no longer 401-loop on their background listen stream. The streaming listener runs on a context with no inbound headers, so the mint previously failed closed and the backend rejected the unauthenticated stream once per second. The connection's subject and actor are now bound at creation and used as the fallback when the request context carries none.

### Changed

- Kubernetes event emission is now always on. The previous opt-in gate (`--enable-events` flag and `events:` config field) and its "disabled by default" framing were removed; events are a core observability feature that works in both Kubernetes (native Events) and filesystem (on-disk log) modes. The `muster serve --enable-events` flag is retained as a hidden, deprecated no-op so existing scripts and unit files keep working after upgrade (it prints a deprecation notice and has no effect); the removed `events:` config key is silently ignored.
- Kubernetes event spam reduction: the high-volume per-session `MCPServerTokenForwarded`/`MCPServerTokenExchanged` Normal events now log at debug level instead of writing a Kubernetes Event on every session's connection to every SSO server. Token *failures* are still surfaced as Warning events. The Kubernetes backend now emits events through a client-go `EventRecorder`/`EventBroadcaster` so duplicate events aggregate into a single object with a `Count` and get per-key rate limiting, and the per-poll `MCPServerHealthCheckFailed` emission is gated on the healthy→unhealthy transition rather than firing every 30s for every unhealthy server. These were the dominant event-volume sources that previously made the feature noisy.
- Update mcp-oauth to v0.13.2: the trusted-issuer JWKS client used for `allowPrivateIPJWKS` now honors the process CA bundle, so a backend validating muster's in-cluster JWKS over TLS with an internal CA no longer fails with `x509: unknown authority`.
- Workflow field-name casing is now consistent across authoring surfaces ([#865](https://github.com/giantswarm/muster/issues/865)): the structured `workflow_create`/`workflow_update`/`workflow_validate` tool path now accepts the canonical camelCase names used by the CRD and the documentation (`allowFailure`, `fromStep`, `expectNot`, `jsonPath`) in addition to the previously released snake_case aliases (`allow_failure`, `from_step`, `expect_not`, `json_path`), and the advertised tool JSON schema now lists the camelCase names. Previously a workflow authored from the (camelCase) documentation was silently mis-parsed through the MCP tool path — e.g. `allowFailure` was dropped, so a step meant to tolerate failure halted the workflow.
- Broker credential minting extracted behind a `CredentialProvider` interface and an `oidc-exchange` provider dispatched through a registry (`internal/oauth`). No behaviour change; the oidc-exchange provider preserves per-(endpoint, connector, user) token caching.
- Update mcp-oauth to v0.9.0: `server.TrustedIssuer.SubjectClaim` sources the canonical subject from a configurable claim, wired through `oauth.server.trustedIssuers[].subjectClaim`.
- Update mcp-oauth to v0.8.0: `server.AcceptTrustedIssuerToken` for accepting a TrustedIssuers-validated bearer as a forwarded credential with the same `ext-<hex>` session-ID derivation as `AcceptForwardedIDToken`.
- Update mcp-oauth to v0.7.1: `server.LocalMintExchanger` for local RFC 9068 JWT minting; RFC 8693 `actor_token` validation and `act` claim support; `ActorDelegationPolicy` for (actor, subject) pair enforcement; `providers.UserInfo.ActorIssuer`/`ActorSubject`/`IsDelegated()`; `oidc.IDTokenClaims.Act` auto-decoded from `act`.
- Update mcp-oauth to v0.4.0.
- Update mcp-oauth to v0.3.1: forwarded ID tokens (`trustedAudiences`) are no longer hard-rejected by the trusted-issuer Bearer branch when the same issuer is also configured in `trustedIssuers` — fixes Backstage AI-chat SSO token forwarding returning 401 (`typ header is "", expected "at+jwt"`) on deployments with the token-exchange broker enabled.
- Update mcp-oauth to v0.3.0 (server-side RFC 8693 token-exchange grant with pluggable `Exchanger`).
- `muster version` now derives its version from the Go build info (`runtime/debug`) stamped from the VCS tag, instead of a hand-maintained literal in `pkg/project`. Release builds run on the tagged commit, so the binaries report the clean tag version; off-tag builds report a pseudo-version and tag-less builds report `dev`. `gitSHA`/`buildTimestamp` are still injected by architect's `go-build` ldflags. Removes the need to bump the version literal on every release. The scratch files architect's `go-build` writes into the worktree (the per-arch `muster-<os>-<arch>` binaries, `.ldflags`, and `.platforms`) are now gitignored so an untracked artifact doesn't mark the build `+dirty` in the embedded version. Validated end-to-end in CI: all six architectures embed the clean tag version with `vcs.modified=false`.

### Removed

- Removed the `--enable-events` flag on `muster serve` and the `events:` config/Helm value. Event emission is no longer gated; the flag and field are gone (existing configs that still set `events:` are harmlessly ignored).
- Pruned six event reasons that were defined, templated, and documented but never emitted anywhere: `MCPServerReconnected`, `WorkflowAvailable`, `WorkflowToolsDiscovered`, `WorkflowToolsMissing`, `WorkflowToolUnregistered`, and the legacy `WorkflowExecuted`. Their constants, message templates, and reference-doc entries were removed so the documented event set matches what muster actually emits.
- Removed the dead `CreateEvent`/`CreateEventForCRD` methods from the `api.EventManagerHandler` interface (replaced by `CreateEventWithData`); the documented `doc.go` examples that called them with the invalid reason `"Created"` were corrected.
- `WorkflowStep.outputs` ([#865](https://github.com/giantswarm/muster/issues/865)): the field was present on the CRD and copied by the adapter but never read by the executor (dead code). Removed from the CRD, internal types, conversion, and step JSON schema. Use `store: true` and reference the result as `{{ .results.<step_id> }}` instead.
- `MCPServer.status.consecutiveFailures`, `.lastAttempt`, and `.nextRetryAfter` are no longer updated by the reconciler; the retry state machine that drove them was removed in a prior release. The fields remain on the CRD for forward compatibility.
- `oauth.server.enableHSTS`, `oauth.server.tlsCertFile`, and `oauth.server.tlsKeyFile` config fields removed; they were declared and YAML-parsed but never read anywhere in the codebase.

### Fixed

- Setting `oauth.server.tokenExchangeBroker.workloadAudiences` no longer prevents the OAuth server from starting. The generated `WorkloadGrant`s now carry `Issuer: "*"` (any trusted issuer); mcp-oauth rejects an empty grant `Issuer`, which previously made the server fail to start and return 503.
- `forwardToken: true` (and `tokenExchange`) MCPServers now work for machine-to-machine callers (forwarded ServiceAccount tokens, no browser auth-code flow). The first request for a new M2M session connects the session's SSO backends synchronously before the request's MCP handler runs, so the caller's initial `tools/list` and `call_tool` succeed instead of returning no tools (`auth_required` / "authentication context missing, no active session"). Concurrent first requests for the same session are deduplicated.
- Raw Kubernetes ServiceAccount projected tokens validated via `oauth.server.trustedIssuers` now drive per-target RFC 8693 token exchange directly. Previously, `injectExternalIDToken` only tried the `TrustedAudiences` path (`AcceptForwardedIDToken`), which returns `ErrTrustedAudienceMismatch` for SA tokens (their `aud` is muster's own resource identifier, not a TrustedAudiences entry); the bearer was dropped and the downstream exchange failed with an empty subject token. The fix adds a fallback to the new `AcceptTrustedIssuerToken` API (mcp-oauth >= v0.8.0) on mismatch. The same `ext-<hex>` session-ID derivation is used, preserving cross-hop audit-log correlation. Closes #805 Issue 3.
- Bump `mcp-oauth` to v0.4.2, which makes the trusted-issuer JWKS cache rotation-safe: a subject token presenting a `kid` absent from the cached JWKS now triggers a single bounded refetch (rate-limited per JWKS URI) and retries verification before rejecting. Previously, a Dex signing-key rotation made the token-exchange broker reject **every** current user token with `subject_token_validation_failed` until the muster pod was restarted (the shared broker took down all downstream audiences at once). Closes #847.
- Bump `mcp-oauth` to v0.4.1, which RFC 6749 §2.3.1-encodes client credentials in token-exchange Basic auth. Cross-cluster SSO token exchange previously failed with `invalid_client` for downstream clusters whose `muster-token-exchange-*` Dex client secret contained `+` (decoded to a space on the wire); base64-std secrets with only `/` and `=` were unaffected, which is why some clusters worked and others did not.
- `ssoPoolMissNeedingInit` now detects pool misses for token-forwarding servers in addition to token-exchange servers, so warm sessions (authAlive=true after pod restart) trigger `initSSOForSession` for forwarding servers with empty connection pools. Previously, forwarding servers registered during a restart were inaccessible until the user manually re-authenticated to muster.
- `establishSSOConnection` now treats a pool miss as stale state for token-forwarding servers (clearing the Valkey auth entry and re-establishing the connection), matching the existing behaviour for token-exchange servers.
- After an idle period, `getIDTokenForForwarding` now attempts an in-process upstream provider refresh (`Server.RefreshSession`) when the proxy store has no valid ID token. On success the store is repopulated by `TokenRefreshHandler` and the fresh token is forwarded, avoiding `401 Unauthorized` errors without requiring re-authentication. Closes #549.

## [0.3.12] - 2026-06-10

### Changed

- Release binaries now include darwin/amd64, darwin/arm64, windows/amd64, and windows/arm64 alongside the existing linux targets. Windows binaries are named `muster-windows-<arch>.exe`.

### Fixed

- Update mcp-oauth to v0.2.199: JWT access tokens issued for grants without an RFC 8707 `resource` parameter now carry an `aud` claim defaulting to the server's resource identifier (RFC 9068 §2.2), instead of an empty audience that JWT-validating gateways (e.g. agentgateway) reject with `401 InvalidAudience`. Existing grants self-heal on their next token refresh.

### Added

* AllowedClaims in TrustedIssuer, drop KubernetesSATrusts, fix JWT signing key wiring ([#772](https://github.com/giantswarm/muster/issues/772)) ([04b5bd2](https://github.com/giantswarm/muster/commit/04b5bd2e1bdfe9b982d778f14f700893b96f0e7f))
- `muster.oauth.server.dex.allowPrivateIPOIDC`: allows Dex OIDC discovery to reach issuer URLs that resolve to private/loopback IPs (e.g. Azure internal load balancers). Requires mcp-oauth#427. Emits a CWE-918 startup warning.

### Fixed

* **deps:** update module github.com/giantswarm/mcp-oauth to v0.2.186 ([ca46984](https://github.com/giantswarm/muster/commit/ca469849a428d1f94de6740179aa1766932f63fa))
* **deps:** update module github.com/giantswarm/mcp-toolkit to v0.2.5 ([#780](https://github.com/giantswarm/muster/issues/780)) ([bcce33a](https://github.com/giantswarm/muster/commit/bcce33a1a4affd2cc156dc39a009f6bd2d95e52b))
- CiliumNetworkPolicy egress now reaches an OIDC issuer (Dex) / HTTP MCP server fronted by a Cilium-managed ingress gateway VIP (LB-IPAM / L2, typical on-prem). New `networkPolicy.cilium.ingressGateway` rule allows egress to the gateway backend endpoints on their target ports (default: `10080`/`10443`, selector: `app.kubernetes.io/name=envoy` in `envoy-gateway-system`).

### Changed

* attach release binaries to GitHub releases ([#785](https://github.com/giantswarm/muster/issues/785)) ([77dbb0f](https://github.com/giantswarm/muster/commit/77dbb0ffa67e3d8d36a8aa0abfd907c401ee2123))
* **deps:** update go toolchain directive to v1.26.4 ([#783](https://github.com/giantswarm/muster/issues/783)) ([ba9c3fd](https://github.com/giantswarm/muster/commit/ba9c3fd49e6159c1535daa6753646d46f5bd5ac4))

## [0.1.231](https://github.com/giantswarm/muster/compare/v0.1.230...v0.1.231) (2026-06-03)


### Fixed

* **deps:** update module github.com/giantswarm/mcp-oauth to v0.2.185 ([#769](https://github.com/giantswarm/muster/issues/769)) ([ca46984](https://github.com/giantswarm/muster/commit/ca469849a428d1f94de6740179aa1766932f63fa))
* **deps:** update module github.com/giantswarm/mcp-toolkit to v0.2.5 ([#780](https://github.com/giantswarm/muster/issues/780)) ([bcce33a](https://github.com/giantswarm/muster/commit/bcce33a1a4affd2cc156dc39a009f6bd2d95e52b))


### Changed

* attach release binaries to GitHub releases ([#785](https://github.com/giantswarm/muster/issues/785)) ([77dbb0f](https://github.com/giantswarm/muster/commit/77dbb0ffa67e3d8d36a8aa0abfd907c401ee2123))
* **deps:** update dependency architect to v9 ([#768](https://github.com/giantswarm/muster/issues/768)) ([a4c790a](https://github.com/giantswarm/muster/commit/a4c790a370f8b3378795fd648864157cf7cbbd72))
* **deps:** update go toolchain directive to v1.26.4 ([#783](https://github.com/giantswarm/muster/issues/783)) ([ba9c3fd](https://github.com/giantswarm/muster/commit/ba9c3fd49e6159c1535daa6753646d46f5bd5ac4))
* **main:** release 0.1.227 ([#779](https://github.com/giantswarm/muster/issues/779)) ([84fca35](https://github.com/giantswarm/muster/commit/84fca350bcf73f6b282f47427a29d17a5d8421df))
* **main:** release 0.1.228 ([#781](https://github.com/giantswarm/muster/issues/781)) ([05f5aeb](https://github.com/giantswarm/muster/commit/05f5aeb5e1569a8a9d8a8a7e9be9bedccdceca80))
* **main:** release 0.1.229 ([#782](https://github.com/giantswarm/muster/issues/782)) ([3cf5800](https://github.com/giantswarm/muster/commit/3cf5800622719a2eb25d973c3fbf420ba7c06a19))
* **main:** release 0.1.230 ([#784](https://github.com/giantswarm/muster/issues/784)) ([7975401](https://github.com/giantswarm/muster/commit/797540104e3cfe47be37c677e01c84833b2ba3d5))

## [0.1.230](https://github.com/giantswarm/muster/compare/v0.1.229...v0.1.230) (2026-06-03)


### Fixed

* **deps:** update module github.com/giantswarm/mcp-oauth to v0.2.185 ([#769](https://github.com/giantswarm/muster/issues/769)) ([ca46984](https://github.com/giantswarm/muster/commit/ca469849a428d1f94de6740179aa1766932f63fa))
* **deps:** update module github.com/giantswarm/mcp-toolkit to v0.2.5 ([#780](https://github.com/giantswarm/muster/issues/780)) ([bcce33a](https://github.com/giantswarm/muster/commit/bcce33a1a4affd2cc156dc39a009f6bd2d95e52b))


### Changed

* **deps:** update dependency architect to v9 ([#768](https://github.com/giantswarm/muster/issues/768)) ([a4c790a](https://github.com/giantswarm/muster/commit/a4c790a370f8b3378795fd648864157cf7cbbd72))
* **deps:** update go toolchain directive to v1.26.4 ([#783](https://github.com/giantswarm/muster/issues/783)) ([ba9c3fd](https://github.com/giantswarm/muster/commit/ba9c3fd49e6159c1535daa6753646d46f5bd5ac4))
* **main:** release 0.1.226 ([#778](https://github.com/giantswarm/muster/issues/778)) ([a0ea312](https://github.com/giantswarm/muster/commit/a0ea312a4389e5293f9d52e1f42d26081a3ea981))
* **main:** release 0.1.227 ([#779](https://github.com/giantswarm/muster/issues/779)) ([84fca35](https://github.com/giantswarm/muster/commit/84fca350bcf73f6b282f47427a29d17a5d8421df))
* **main:** release 0.1.228 ([#781](https://github.com/giantswarm/muster/issues/781)) ([05f5aeb](https://github.com/giantswarm/muster/commit/05f5aeb5e1569a8a9d8a8a7e9be9bedccdceca80))
* **main:** release 0.1.229 ([#782](https://github.com/giantswarm/muster/issues/782)) ([3cf5800](https://github.com/giantswarm/muster/commit/3cf5800622719a2eb25d973c3fbf420ba7c06a19))

## [0.1.229](https://github.com/giantswarm/muster/compare/v0.1.228...v0.1.229) (2026-06-02)


### Fixed

* **deps:** update module github.com/giantswarm/mcp-oauth to v0.2.185 ([#769](https://github.com/giantswarm/muster/issues/769)) ([ca46984](https://github.com/giantswarm/muster/commit/ca469849a428d1f94de6740179aa1766932f63fa))
* **deps:** update module github.com/giantswarm/mcp-toolkit to v0.2.4 ([#777](https://github.com/giantswarm/muster/issues/777)) ([9f915d6](https://github.com/giantswarm/muster/commit/9f915d6f80ec31496eb3014d643683caa5164730))
* **deps:** update module github.com/giantswarm/mcp-toolkit to v0.2.5 ([#780](https://github.com/giantswarm/muster/issues/780)) ([bcce33a](https://github.com/giantswarm/muster/commit/bcce33a1a4affd2cc156dc39a009f6bd2d95e52b))


### Changed

* **deps:** update dependency architect to v9 ([#768](https://github.com/giantswarm/muster/issues/768)) ([a4c790a](https://github.com/giantswarm/muster/commit/a4c790a370f8b3378795fd648864157cf7cbbd72))
* **main:** release 0.1.226 ([#778](https://github.com/giantswarm/muster/issues/778)) ([a0ea312](https://github.com/giantswarm/muster/commit/a0ea312a4389e5293f9d52e1f42d26081a3ea981))
* **main:** release 0.1.227 ([#779](https://github.com/giantswarm/muster/issues/779)) ([84fca35](https://github.com/giantswarm/muster/commit/84fca350bcf73f6b282f47427a29d17a5d8421df))
* **main:** release 0.1.228 ([#781](https://github.com/giantswarm/muster/issues/781)) ([05f5aeb](https://github.com/giantswarm/muster/commit/05f5aeb5e1569a8a9d8a8a7e9be9bedccdceca80))

## [0.1.228](https://github.com/giantswarm/muster/compare/v0.1.227...v0.1.228) (2026-06-02)


### Fixed

* **deps:** update module github.com/giantswarm/mcp-oauth to v0.2.185 ([#769](https://github.com/giantswarm/muster/issues/769)) ([ca46984](https://github.com/giantswarm/muster/commit/ca469849a428d1f94de6740179aa1766932f63fa))
* **deps:** update module github.com/giantswarm/mcp-toolkit to v0.2.4 ([#777](https://github.com/giantswarm/muster/issues/777)) ([9f915d6](https://github.com/giantswarm/muster/commit/9f915d6f80ec31496eb3014d643683caa5164730))


### Changed

* **deps:** update dependency architect to v9 ([#768](https://github.com/giantswarm/muster/issues/768)) ([a4c790a](https://github.com/giantswarm/muster/commit/a4c790a370f8b3378795fd648864157cf7cbbd72))
* **main:** release 0.1.225 ([#776](https://github.com/giantswarm/muster/issues/776)) ([d00cc90](https://github.com/giantswarm/muster/commit/d00cc903219512cecdac4fba10ddc9688a81093d))
* **main:** release 0.1.226 ([#778](https://github.com/giantswarm/muster/issues/778)) ([a0ea312](https://github.com/giantswarm/muster/commit/a0ea312a4389e5293f9d52e1f42d26081a3ea981))
* **main:** release 0.1.227 ([#779](https://github.com/giantswarm/muster/issues/779)) ([84fca35](https://github.com/giantswarm/muster/commit/84fca350bcf73f6b282f47427a29d17a5d8421df))

## [0.1.227](https://github.com/giantswarm/muster/compare/v0.1.226...v0.1.227) (2026-06-02)


### Fixed

* **deps:** update module github.com/giantswarm/mcp-toolkit to v0.2.3 ([#770](https://github.com/giantswarm/muster/issues/770)) ([7649ee8](https://github.com/giantswarm/muster/commit/7649ee8236a56ca88d076dedb7470bdddc2203a7))
* **deps:** update module github.com/giantswarm/mcp-toolkit to v0.2.4 ([#777](https://github.com/giantswarm/muster/issues/777)) ([9f915d6](https://github.com/giantswarm/muster/commit/9f915d6f80ec31496eb3014d643683caa5164730))


### Changed

* **deps:** update dependency architect to v9 ([#768](https://github.com/giantswarm/muster/issues/768)) ([a4c790a](https://github.com/giantswarm/muster/commit/a4c790a370f8b3378795fd648864157cf7cbbd72))
* **main:** release 0.1.225 ([#776](https://github.com/giantswarm/muster/issues/776)) ([d00cc90](https://github.com/giantswarm/muster/commit/d00cc903219512cecdac4fba10ddc9688a81093d))
* **main:** release 0.1.226 ([#778](https://github.com/giantswarm/muster/issues/778)) ([a0ea312](https://github.com/giantswarm/muster/commit/a0ea312a4389e5293f9d52e1f42d26081a3ea981))

## [0.1.226](https://github.com/giantswarm/muster/compare/v0.1.225...v0.1.226) (2026-06-02)


### Fixed

* **deps:** update module github.com/giantswarm/mcp-toolkit to v0.2.3 ([#770](https://github.com/giantswarm/muster/issues/770)) ([7649ee8](https://github.com/giantswarm/muster/commit/7649ee8236a56ca88d076dedb7470bdddc2203a7))
* **deps:** update module github.com/giantswarm/mcp-toolkit to v0.2.4 ([#777](https://github.com/giantswarm/muster/issues/777)) ([9f915d6](https://github.com/giantswarm/muster/commit/9f915d6f80ec31496eb3014d643683caa5164730))


### Changed

* **main:** release 0.1.224 ([#775](https://github.com/giantswarm/muster/issues/775)) ([311f7b5](https://github.com/giantswarm/muster/commit/311f7b5e40f98fcd7b0e73ac29538eff38fee42f))
* **main:** release 0.1.225 ([#776](https://github.com/giantswarm/muster/issues/776)) ([d00cc90](https://github.com/giantswarm/muster/commit/d00cc903219512cecdac4fba10ddc9688a81093d))

## [0.1.225](https://github.com/giantswarm/muster/compare/v0.1.224...v0.1.225) (2026-06-02)


### Fixed

* **deps:** update module github.com/giantswarm/mcp-toolkit to v0.2.3 ([#770](https://github.com/giantswarm/muster/issues/770)) ([7649ee8](https://github.com/giantswarm/muster/commit/7649ee8236a56ca88d076dedb7470bdddc2203a7))


### Changed

* **deps:** update actions/checkout action to v6.0.3 ([#774](https://github.com/giantswarm/muster/issues/774)) ([59a91dc](https://github.com/giantswarm/muster/commit/59a91dc9fa4940afbf2041ad452a9647f723c17d))
* **main:** release 0.1.224 ([#775](https://github.com/giantswarm/muster/issues/775)) ([311f7b5](https://github.com/giantswarm/muster/commit/311f7b5e40f98fcd7b0e73ac29538eff38fee42f))

## [0.1.224](https://github.com/giantswarm/muster/compare/v0.1.223...v0.1.224) (2026-06-02)


### Changed

* **deps:** update actions/checkout action to v6.0.3 ([#774](https://github.com/giantswarm/muster/issues/774)) ([59a91dc](https://github.com/giantswarm/muster/commit/59a91dc9fa4940afbf2041ad452a9647f723c17d))
* **main:** release 0.1.223 ([#771](https://github.com/giantswarm/muster/issues/771)) ([4b20779](https://github.com/giantswarm/muster/commit/4b20779e3bcc882c8398d44cbbf1b8826e793003))

## [0.1.223](https://github.com/giantswarm/muster/compare/v0.1.222...v0.1.223) (2026-06-02)


### Changed

* align files according to platform standards ([#767](https://github.com/giantswarm/muster/issues/767)) ([d7b7c9a](https://github.com/giantswarm/muster/commit/d7b7c9a7a63a809e644d6991e3806095a53ed938))

## Shipped between v0.1.223 and v0.3.11 (previously misfiled as Unreleased)

### Fixed

- `enableJWTMode: true` now issues RFC 9068 signed JWT access tokens. Set `muster.oauth.server.jwtSigningKey` (PEM-encoded EC P-256 or RSA key) or `existingSecret` with key `jwt-signing-key`; `helm template` fails if neither is provided when `enableJWTMode: true`.
- CiliumNetworkPolicy egress now reaches an OIDC issuer (Dex) / HTTP MCP server that is fronted by a **Cilium-managed ingress gateway VIP** (LB-IPAM / L2, typical on-prem). With `kube-proxy-replacement`, Cilium DNATs the LoadBalancer VIP to the gateway backend pod on its *target* port (e.g. `443`→`10443`) **before** egress policy is evaluated, so neither `toEntities: world` nor `toEntities: cluster` on `443` matched and OIDC discovery failed with `context deadline exceeded`. A new `networkPolicy.cilium.ingressGateway` rule allows egress to the gateway backend endpoints on their target ports (default: Giant Swarm `envoy-gateway` proxies on `10080`/`10443`). Clusters whose gateway VIP is an external cloud LB (e.g. AWS ELB) were already covered by the `world` rule and are unaffected (the new rule is a no-op there); set `ingressGateway: null` to disable. Fixes the OAuth/`OIDC discovery failed` startup warning on affected clusters.
- Workflow availability (`core_workflow_available` / `core_workflow_list` / `workflow_available`) is now session-aware. Previously, availability for SSO / auth-protected family tools (e.g. multi-instance `kubernetes` / `prometheus` servers) was computed from the process-global family routing index, which is unioned across sessions and only populated as a side effect of a prior `list_tools` call. This produced two symmetric defects: a **false negative** — `muster list workflows` / `muster get workflow` reported workflows `Unavailable` until some session listed tools, while `muster agent` (which lists tools on connect) reported them available, so the answer depended on call ordering; and a **false positive** — once any session listed tools, the family entry leaked process-wide, so a session that never authenticated to the family still saw the workflow as available. When the request carries a session, availability now resolves each step tool against that session's own accessible tools (hydrated from the `CapabilityStore`); core / meta tools resolve by name, and only session-less calls fall back to the process-global view. Closes #764.
- Workflow availability is now transitive across nested workflows. A workflow step that calls another workflow (`workflow_<name>`) was always treated as available because the availability check matched the `workflow_` prefix without consulting the registry, so a workflow referencing a non-existent or transitively broken nested workflow was wrongly reported `Available` and only failed at execution time. Nested workflow steps now require the referenced workflow to exist and to be itself available; the check descends through the whole chain (with cycle detection) and reports the actual unavailable tool. The `workflow_` management meta-tools (`workflow_list`, `workflow_available`, ...) are unaffected.

### Changed

- Bump `giantswarm/mcp-oauth` to `v0.2.184`. New Helm values `muster.oauth.server.{trustedIssuers,trustedProxyCIDRs,enableJWTMode,resourceIdentifier}` wire trusted external OIDC issuers for RFC 8693 token exchange (id_token / access_token / jwt), DPoP trusted-proxy CIDRs, RFC 9068 JWT access tokens, and RFC 8707 resource-server audience binding. `trustedIssuers` entries now support `allowedClaims` (claim name to glob-pattern map) for Kubernetes ServiceAccount and GitHub Actions trust. Also enables the OIDC userinfo endpoint, PII-redacted audit logging, and CIMD metadata-fetch rate limiting. Encryption-at-rest is now wired on the store constructor (`valkey.WithEncryptor` / `memory.WithEncryptor`) rather than as a server option.

### Added

- New standalone `muster-crds` Helm chart (`helm/muster-crds`) shipping the `MCPServer` and `Workflow` CustomResourceDefinitions. The CRDs are loaded from `files/crds/*.yaml` by `templates/crds.yaml` (regular chart templates, not the Helm 3 `crds/` directory), so they remain upgradable on `helm upgrade` and keep the `helm.sh/resource-policy: keep` annotation. This decouples the CRD lifecycle from the application chart so a downstream `agentic-platform-crds` umbrella can own it independently. Install or upgrade `muster-crds` **before** `muster`.
- Degraded-mode startup when the Dex/OIDC issuer is unreachable at boot time. muster now starts immediately and serves MCP aggregation, reconcilers, and all non-OAuth paths regardless of Dex availability. A background goroutine retries OIDC discovery with exponential backoff (1 s → 30 s cap); once discovery succeeds the OAuth server activates transparently. Until then, OAuth and MCP-over-OAuth endpoints return `503 Service Unavailable` with a `Retry-After: 30` header. The `/health` endpoint always returns `200` with `{"status":"degraded","reason":"oidc-discovery-pending"}` during the window. Closes #730.
- `networkPolicy.flavor` selects between `cilium` (CiliumNetworkPolicy) and `kubernetes` (`networking.k8s.io/v1 NetworkPolicy`). The kubernetes flavor is best-effort: no entity selectors, no FQDN egress. CIDR replacements live under `networkPolicy.kubernetes.{apiServerCIDR,clusterCIDR,worldExcludedCIDRs}`. `clusterCIDR: ""` disables the in-cluster ingress egress rule (kubernetes-flavor equivalent of cilium `allowClusterIngress`).
- `crds.annotations` (object) is merged into each CRD's `metadata.annotations` by the loader. Default `{helm.sh/resource-policy: keep}` keeps CRDs (and the `MCPServer` / `Workflow` CRs that depend on them) around on `helm uninstall`.
- `revisionHistoryLimit` (default `3`) on the muster Deployment.
- `resources.{requests,limits}.ephemeral-storage` (50Mi / 100Mi) — Kyverno's resource-limits policy on Giant Swarm workload clusters audits / rejects pods without explicit ephemeral-storage when `/tmp` is an emptyDir.
- Egress to `app.kubernetes.io/name=agentgateway:8080` in the release namespace so muster can dial the agentgateway data-plane on the upstream-proxy path (both NetworkPolicy flavors). No-op when agentgateway isn't deployed.

### Removed

- `muster.oauth.server.kubernetesSATrusts` Helm value and `K8sSATrustConfig` Go type are removed. Kubernetes ServiceAccount trust is now expressed via `trustedIssuers` with an `allowedClaims` entry (`sub: "system:serviceaccount:<namespace>:*"`) and `allowPrivateIPJWKS: true` when the JWKS endpoint is in-cluster. The `jwt` subject_token_type covers projected SA tokens without a separate trust list.

- `ciliumNetworkPolicy.*` is replaced by `networkPolicy.*`. `ciliumNetworkPolicy.enabled` → `networkPolicy.enabled` + `networkPolicy.flavor: cilium` (default). `ciliumNetworkPolicy.allowClusterIngress` → `networkPolicy.cilium.allowClusterIngress`. `ciliumNetworkPolicy.{labels,annotations}` → `networkPolicy.{labels,annotations}`.

### Changed

- The muster application chart no longer renders the CRDs. `helm/muster/templates/crds.yaml` was removed and the CRDs moved to the new `muster-crds` chart. `crds.install` now defaults to `false` and the whole `crds` block is deprecated (inert compatibility shim, removed next release) — it is kept only so a downstream that explicitly sets `muster.crds.install: false` still validates. Operators must install/upgrade `muster-crds` before `muster`.
- CRD source files moved from `helm/muster/files/crds/` to `helm/muster-crds/files/crds/`. `files/` has no Helm 3 special-case, so the CRDs stay upgradable on `helm upgrade`. `controller-gen` output path updated in `Makefile.crd.mk`; CI drift check in `.github/workflows/ci.yaml` follows the new path.

- Container image build no longer compiles the Go binary inside `docker buildx`. `go-build` now produces both `muster-linux-amd64` and `muster-linux-arm64` in one job (architect-orb `architectures` parameter) and the Dockerfile copies the matching binary from the workspace. Removes the duplicate compile and the QEMU-emulated arm64 cross-build on tag releases; `push-to-registries` auto-derives `--platform` from the workspace `.platforms` file.
- Build identifiers (`version`, `gitSHA`, `buildTimestamp`) now live in `pkg/project` instead of `main`. Both injection paths populate the same vars: goreleaser writes the semver tag + short commit + date for release archives, architect-orb's `go-build` writes the commit SHA + UTC timestamp for container images. `muster version` prefers the tag, falls back to the SHA, falls back to `dev`, and additionally prints the commit SHA and build timestamp on dedicated lines.
- Bump `giantswarm/architect` orb to `8.2.2` and re-enable cosign keyless chart signing (`sign: false` removed from every `push-to-app-catalog*` invocation). v8.2.2 ships [architect-orb#772](https://github.com/giantswarm/architect-orb/pull/772) which upgrades the `app-build-suite` executor image from `1.8.0-circleci` to `1.8.1-circleci` -- the new image includes the `cosign` binary that v8.2.0's chart signing defaults require. Closes [architect-orb#769](https://github.com/giantswarm/architect-orb/issues/769).
- Bump `giantswarm/architect` orb to `8.2.1` to pick up [architect-orb#767](https://github.com/giantswarm/architect-orb/pull/767): `image-login-to-registries` is now POSIX-portable, unblocking `architect/sync-china-registry` (the gsoci -> Aliyun mirror via the in-China `giantswarm/galaxy-runner`). The v8.1.0 refactor accidentally introduced bash-only `${!var}` indirect expansion in the shared login command, which BusyBox `/bin/sh` (used by the regctl executor) rejected with `bad substitution` -- so no Aliyun mirror has been happening since the migration to `split-china-push: true`. v8.2.x also enables cosign keyless signing, SLSA provenance, and SBOM attestations by default for public images and charts.
- Disable cosign keyless chart signing on the `push-to-app-catalog*` jobs (`sign: false`). The architect orb's `push-to-app-catalog` defaults `sign` to `true` since v8.2.0 and shells out to `cosign`, but this repo uses `executor: app-build-suite` (so the `app_build_suite` Python CLI is available to package the chart with metadata) and the `app-build-suite` image doesn't ship `cosign`. Without this opt-out, every chart push fails on the `Mint Sigstore OIDC token` step with `cosign: command not found`. To be removed once architect-orb makes `cosign-prepare` resilient to a missing binary (or ships cosign in the `app-build-suite` executor) -- tracked in [architect-orb#769](https://github.com/giantswarm/architect-orb/issues/769).
- Replace the `push-to-gsoci-release` + `push-to-all-registries-release` workaround pair with a single `push-to-registries-release` job using `split-china-push: true` and a companion `sync-china-registry` job. The cross-Pacific `docker buildx` push to the Aliyun mirror is replaced with `regctl image copy` (gsoci -> Aliyun) executed on the in-China `giantswarm/galaxy-runner` self-hosted CircleCI runner via the Singapore geo-replica. The chart catalog publish still does not gate on Aliyun.
- Migrate image pushes from the deprecated `architect/push-to-registries-multiarch` job to `push-to-registries` with `multiarch: true`. Picks up the orb v8.1.0 QEMU/binfmt auto-registration, hardened buildx bootstrap, and standard OCI image labels.

### Added

- `muster serve --extra-ca-file <path>` flag: appends a PEM file to the system trust pool at startup, so outbound HTTP (MCP backends, token exchange, OAuth proxy) trusts an internal CA without per-MCPServer plumbing. Exposed in the chart as `muster.extraCaFile.{path,secret.name,secret.key}`; the chart mounts the named Secret and passes the flag when `secret.name` is set. Use case: tunnelport's SPIFFE-issued tunnel certificates on a Giant Swarm consumer MC.
- `MCPServer.spec.family` — optional object `{name, instanceArg}` grouping equivalent MCPServers under a shared exposed surface. When set, the aggregator exposes tools as `x_<family.name>_<tool>` with a required parameter (named by `family.instanceArg`) selecting the providing instance. Both fields are required when `family` is set. The parameter is always required even for single-instance families so skills written against the family name remain stable as instances are added or removed. When unset, today's per-server prefixing applies (no behavior change for existing CRs).
- `MCPServer.spec.family` is configurable via the `core_mcpserver_create` / `core_mcpserver_update` / `core_mcpserver_validate` tools.
- `muster.oauth.server.trustedPublicRegistrationRedirectURIs` — HTTPS redirect-URI allowlist for unauthenticated dynamic client registration, passed through to mcp-oauth (`Config.TrustedPublicRegistrationRedirectURIs`). Strict exact-match after RFC 3986 normalization. Default: `[]` (opt-in per URI).
- `oauth-secret` `fail` guard accepts a non-empty `trustedPublicRegistrationRedirectURIs` as a third valid escape valve.

### Changed

- The shared OpenTelemetry identifiers (`TracerName`, `AttrToolName`) move to `pkg/observability`, a leaf package with no internal/* dependencies that any package can import without going through the service locator. The `internal/aggregator/instrument` subpackage is flattened into `internal/aggregator`: `Logging`, `Metrics`, `StartToolSpan`, and the formerly-exported `MCPServerOptions` (now unexported `mcpServerOptions`) all live alongside `server.go` so the aggregator's MCP-server middleware sits in one place. External imports of `github.com/giantswarm/muster/internal/aggregator/instrument` move to `github.com/giantswarm/muster/pkg/observability` (constants only).
- `aggregator.Register` / `aggregator.RegisterPendingAuth` and their manager-level / `api.AggregatorHandler` counterparts now take a `ServerRegistration` / `PendingAuthRegistration` struct rather than five-to-six positional `(name, url, toolPrefix, family, authInfo, authConfig)` arguments. The previous `RegisterServerPendingAuthWithConfig` is collapsed into the single `RegisterServerPendingAuth(registration)` form — `AuthConfig` is now a nullable field inside the struct. Internal API change; no behavior change for existing CRs.
- Aggregator OpenTelemetry tracing adopts mcp-go's native server-tracing hooks via the `github.com/mark3labs/mcp-go/otel` adapter, replacing muster's per-tool-handler middleware. The aggregator now emits `mcp.<method>` spans (server kind) around every dispatched JSON-RPC method and `tool.<name>` spans (internal kind) around tool handlers, with W3C trace-context propagation extracted from inbound headers. Custom `instrument.Tracing()` middleware is removed; `instrument.StartToolSpan` is retained for the internal `CallToolInternal` dispatch path used by workflows and direct API entries.
- Outbound MCP clients (stdio, SSE, streamable-http, dynamic-auth) install mcp-go's OTEL tracer via the `mcp-go/otel.WithClientTracing` adapter so the muster → backend leg inherits the inbound trace context and a W3C `traceparent` is emitted on every outgoing JSON-RPC frame. Combined with server-side tracing, a single trace now covers the whole caller → muster → backend chain.
- mcp-oauth bumped to `v0.2.140`. The OAuth HTTP handler (`Handler`, `New`, `OAuthRoutesOptions`, `UserInfoFromContext`, `SessionIDFromContext`) moved to a `handler` subpackage; muster's `internal/server` and `internal/aggregator` import the new path. No user-facing config change.
- mcp-oauth bumped to `v0.2.125`. Internal API migrated to functional options; `server.NewOAuthHTTPServer` now takes `...oauth.ServerOption`. Security-event log emission is rate-limited (1/s, burst 5). No user-facing config change.

### Fixed

- `MCPServer.spec.family` tool emission now deep-copies nested JSON schema sub-trees (object `properties`, array `items`, nested `required`) so caller mutations of an exposed tool's schema no longer leak into the per-server cache and corrupt later `tools/list` results.
- `tools/list` order is now deterministic across calls for family-grouped tools. Previously the assembly iterated Go maps directly, producing shifting orders between calls and spurious `tools/list_changed` diffs downstream.
- When `family.instanceArg` collides with a property name already declared in the tool's own `InputSchema.Properties`, the aggregator now falls back to per-server prefixing for that specific tool. Previously the family-grouped emission silently overwrote the operator-declared property with the instance-selector enum, losing the original property's description, type, and constraints.
- Workflow execution tools were advertised twice — both as the documented `workflow_<workflow-name>` and as `core_action_<workflow-name>` — through `list_tools` / `list_core_tools` / `filter_tools`. The `core_action_*` variant is not part of the public surface and the aggregator's call routing does not recognize it (calls fail with "no handler found"), so clients that picked it up from discovery hit non-functional tools. The aggregator now rewrites the workflow provider's internal `action_<name>` tools to `workflow_<name>` (no `core_` prefix) when listing, matching the architecture spec; management tools (`workflow_list`, `workflow_get`, …) continue to be advertised as `core_workflow_*`. Pure listing fix — execution routing was already correct.
- `Workflow` and `ServiceClass` CRD validation rejected scalar values in step args (`spec.steps[*].args.<key>: must be of type object`), making the documented YAML form (`namespace: kube-system`, `limit: 30`, `allNamespaces: false`) unusable through `kubectl apply`. Step args, condition args, JSONPath maps, and `ArgDefinition.Default` now use `apiextensionsv1.JSON` instead of `runtime.RawExtension`, which controller-tools emits as `additionalProperties: {x-kubernetes-preserve-unknown-fields: true}` (no `type: object` constraint), so scalars, objects, and arrays all validate. Wire format and stored values are unchanged; existing workflows with object-only args keep working.
- OAuth server initialisation built its own text-format `slog.Logger` writing to stdout when `--debug` was set, so in-pod log lines from the `mcp-oauth` library (Valkey storage, redirect-URI security, OIDC discovery, rate limiters, audit, instrumentation) appeared as text on stdout instead of flowing through the project's JSON handler. `createOAuthServer` now uses `slog.Default()` and inherits the level set by `logging.Init`, so all in-pod log lines share one format and one writer.

### Removed

- **Breaking (MCPServer CRD):** Teleport authentication support removed from muster — moved to a separate operator. `MCPServerAuth.type` no longer accepts `teleport`; the `teleport` field (`TeleportAuthConfig` with `identityDir` / `identitySecretName` / `identitySecretNamespace` / `appName`) is removed from the CRD. Existing CRs with `auth.type: teleport` or an `auth.teleport` block will be rejected by validation and must be migrated to the new operator. The `internal/teleport` package, the `api.TeleportClientHandler` / `api.RegisterTeleportClient` / `api.GetTeleportClient` / `api.TeleportClientConfig` / `api.TeleportAuth` / `api.AuthTypeTeleport` API surface, the `OAuthHandler.ExchangeTokenForRemoteClusterWithClient` method, the `TokenExchanger.ExchangeWithClient` method, the `mcpserver.MCPClientConfig.HTTPClient` field, and the `NewStreamableHTTPClientWithHTTPClient` / `NewStreamableHTTPClientWithHeaderFuncAndHTTPClient` constructors are removed.
- **Breaking (external consumers of `pkg/oauth`):** `pkg/oauth.IDTokenClaims` struct and `ParseIDTokenClaims` function removed. Replaced by typed accessors in `pkg/oauth/jwt.go` — `Subject`, `Email`, `Expiry`, `Issuer`, `IsExpired` — each returning `(value, error)` so callers can distinguish "missing claim" from "decode failed".
- Per-config CA-file knobs removed: `OAuthMCPClientConfig.CAFile`, `DexConfig.CAFile` (Go config), and the `muster.oauth.server.dex.caFile` Helm value. These were redundant after `--extra-ca-file` (which augments the system trust pool) and the OAuth one was a footgun: it built a fresh cert pool from a single file, silently dropping system roots and narrowing trust to that one CA. **Operator caveat — trust scope changes:** the removed paths *narrowed* trust to a single CA; the replacement `--extra-ca-file` / `muster.extraCaFile` is *additive* on top of the system pool. Anyone who deliberately relied on the narrower scope no longer has that option here. None of giantswarm's deployed configs set these values, so no migration is required.

### Changed

- Logging bootstrap now lives in `cmd/serve.go`. The serve command calls `logging.Init` once at startup, defers the `Shutdown`, then constructs the application. `NewApplication` no longer touches the logger; non-serve `muster` subcommands rely on the nil-guard in `pkg/logging` (the previous in-bootstrap init was vestigial there too).
- `app.NewConfig` signature drops the `silent` parameter and the corresponding `Config.Silent` field. Both were set but never read — `--silent` is enforced by swapping the writer to `io.Discard` directly in `cmd/serve.go`. Module-internal change (`internal/app` is not importable from outside the module).
- In-pod muster logs are now JSON by default (auto-detected via `KUBERNETES_SERVICE_HOST`) instead of text. The text path remains for local `muster` CLI invocations and tests.
- The aggregator's `Hooks` (`AddAfterInitialize`, `AddAfterListTools`, `AddBeforeCallTool`, `AddAfterCallTool`, `AddOnError`) emit log lines via the new `*WithAttrsCtx` variants so per-request trace correlation lands on the `MCP-Protocol` subsystem.
- The Valkey storage URL in startup logs is now redacted via `mcp-toolkit/logging.RedactHost`, which strips IPv4/IPv6 addresses and URL userinfo. The local `redactURL` helper, which only stripped userinfo, is removed.
- Consolidated scattered JWT-claim decoders into typed accessors in `pkg/oauth/jwt.go`: `Subject`, `Email`, `Expiry`, `Issuer`, `IsExpired`, plus `ErrTokenExpMissing` for callers that need to distinguish "missing exp" from "decode failed". The accessors share a single `golang-jwt/jwt/v5` parser; consumers in `internal/aggregator`, `internal/cli`, and `internal/oauth` no longer touch `encoding/base64`, `encoding/json`, or the JWT library directly. The admin diagnostic UI keeps its own segment decoder (different concern: lenient display of operator-pasted tokens, including 2-part inputs missing the signature segment). The previous defensive `RawStdEncoding` fallback for non-spec base64 is intentionally dropped — every IdP muster integrates with emits RFC 7515-compliant `RawURLEncoding`.

### Added

- `pkg/logging.Init(ctx, level, output, serviceName, serviceVersion) (Shutdown, error)` initialises logging via `mcp-toolkit/logging` and returns a `Shutdown` for the OpenTelemetry `LoggerProvider`. When any of `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`, `OTEL_EXPORTER_OTLP_ENDPOINT`, or `OTEL_LOGS_EXPORTER` is set, log records flow through OTLP and carry the active span's TraceID/SpanID for log ↔ trace correlation in Grafana. Otherwise the handler auto-selects JSON (in a Kubernetes pod) or text (local dev) and the `Shutdown` is a no-op. `InitForCLI` stays as a non-OTLP convenience.
- `pkg/logging.{Debug,Info,Warn,Error}Ctx` and `{Debug,Info,Warn}WithAttrsCtx` thread a `context.Context` through the slog handler so the OTLP path can pull the active span's TraceID/SpanID off the call's context. Existing ctx-less variants remain.
- OpenTelemetry tracing for every MCP tool call. The aggregator's mcp-go middleware opens a `tool.<name>` span at the meta-tool layer, and `CallToolInternal` opens an inner span carrying the real workload tool name (`x_kubernetes_*`, `workflow_*`, `service_*`, …) so Tempo trace trees pivot on the actual tool. W3C TraceContext + Baggage propagators are installed unconditionally so inbound `traceparent` headers are honoured even when no exporter is configured. Tracer- and meter-provider lifecycles are handled by `mcp-toolkit/tracing` and `mcp-toolkit/metrics` at the composition root.
- OpenTelemetry metrics for every MCP tool call: `muster.tool_calls` (counter, exports as `muster_tool_calls_total`) and `muster.tool_call.duration` (histogram, exports as `muster_tool_call_duration_seconds`), each with `tool` and `outcome` attributes (`ok` / `error` / `error_result`).
- Helm: `muster.observability.metrics.exporter` switches the metric backend (`otlp`, `prometheus`, `console`, `none`, comma-combinations). Selecting `prometheus` exposes `/metrics` on port 9464 and (when `muster.observability.metrics.prometheus.serviceMonitor.enabled`) renders a `ServiceMonitor`.
- Structured per-tool-call log line on subsystem `MCP-Tool` with `tool`, `outcome`, `duration_s`, and `error` fields, for log/metric/trace correlation in dashboards.
- `muster.observability.otel.{endpoint,protocol,headers,resourceAttributes}` Helm values configuring the OTLP exporter. Empty `endpoint` (default) leaves muster in propagator-only mode; setting it enables both traces and metrics over the same OTLP endpoint. `K8S_NODE_NAME` is now exposed via the downward API alongside the existing `K8S_NAMESPACE` / `K8S_POD_NAME` so resource attributes carry `k8s.node.name`. `OTEL_RESOURCE_ATTRIBUTES` is set whenever either OTLP or a metrics exporter is configured (previously OTLP-only), so Prometheus-only mode also gets `k8s.namespace.name` / `k8s.pod.name` / `k8s.node.name` resource attribution.
- `docs/explanation/observability.md` documenting the trace contributions, signal configuration, instrument and log-field shape, and a Tempo/Mimir/Loki query catalog.
- Add `muster call` command for direct MCP tool invocation from the CLI. Supports `--key=value` arguments and `--json` for complex payloads, with tab completion for tool names.
- Add `ciliumNetworkPolicy.allowClusterIngress` Helm value to allow egress to in-cluster services on HTTP/HTTPS ports (e.g. Dex OIDC via ingress LoadBalancer IP).
- OAuth encryption keys can now be supplied as either base64 (`openssl rand -base64 32`) or hex (`openssl rand -hex 32`); the format is auto-detected.
- Agent OAuth client now validates the RFC 9207 `iss` parameter on the authorization callback (defense-in-depth against AS mix-up attacks). Servers that omit `iss` are still accepted.
- Authorization-server discovery now also serves `/.well-known/openid-configuration` and per-path Protected Resource Metadata at `/.well-known/oauth-protected-resource/mcp` (additive — RFC 9728 / OpenID Connect Discovery).
- BDD scenarios `workflow-conditional-static` and `service-state-static` to preserve coverage of workflow conditional features (inline tool conditions, `from_step`, `allow_failure`, `expect_not`, `condition_evaluation`, step skipping) and static-service state-machine semantics (`core_service_restart` happy-path, `core_service_start` on already-running, `core_service_stop` on already-stopped). The deleted ServiceClass-based scenarios bundled this coverage with SC-instance lifecycle; the replacements drive the same workflow-engine and orchestrator code paths against a static MCPServer service.
- `MCPServer.spec.auth.authorizationServer` lets operators pin the OAuth issuer when the backend doesn't publish RFC 9728 metadata (Atlassian's hosted MCP being the prompting case). The override applies to `core_auth_login` only and is verified against the AS metadata's `issuer` field per RFC 8414 §3.3 to fail closed on a wrong pin. Fixes [#599](https://github.com/giantswarm/muster/issues/599).

### Changed

- Extracted validation and template-resolution helpers from the 930-line `internal/workflow/executor.go` into dedicated `validation.go` and `template.go` files. `executor.go` is now ~600 lines; `ExecuteWorkflow` itself remains 500 lines and a follow-up tracks breaking it into per-concern helpers (`executeStep`, `evaluateCondition`, `processStepResult`) — that's a behavioral refactor with test impact, not a file split. ([#140](https://github.com/giantswarm/muster/issues/140))
- Move the 547-line `internal/client/kubernetes_client.go` into a new `internal/client/kubernetes/` subpackage, split per domain (`client.go`, `mcpserver.go`, `serviceclass.go`, `workflow.go`, `events.go`). The core file keeps the type, constructor, scheme, lifecycle methods, and discovery-based CRD validation. Pure refactor: the `MusterClient` interface stays in the parent `client` package, the dispatcher calls `kubernetes.New(restConfig)` directly, and external consumers are unaffected. ([#140](https://github.com/giantswarm/muster/issues/140))
- Move the 1233-line `internal/client/filesystem_client.go` into a new `internal/client/filesystem/` subpackage, split per domain (`client.go`, `mcpserver.go`, `serviceclass.go`, `workflow.go`, `events.go`). Each file is under 400 lines. Pure refactor: the `MusterClient` interface stays in the parent `client` package, the dispatcher calls `filesystem.New(basePath)` directly, and external consumers are unaffected. ([#140](https://github.com/giantswarm/muster/issues/140))
- Collapse per-CRD duplication in both client adapters into shared `store.go` helpers built on `client.Object` / `client.ObjectList`. Each per-CRD file shrinks from ~165 LOC (filesystem) / ~70 LOC (kubernetes) to ~40 LOC of thin wrappers; error wrapping and namespace handling are now uniform across both surviving CRDs. The kubernetes `CreateEventForCRD` double switch (kind→GVK + kind→Get-method) collapses to a single `crdFactories` map. Pure refactor: no public method signatures change, no behaviour change. ([#140](https://github.com/giantswarm/muster/issues/140))
- Restore `groups` scope in `DefaultOAuthCIMDScopes` -- required for group-based RBAC in downstream services. Provider-level scope filtering in mcp-oauth (e.g., `filterGoogleScopes`, `filterDexScopes`) handles provider differences.
- Bump `mcp-oauth` to v0.2.117. Adopts `oauth.NewServerWithCombined` and `Handler.RegisterOAuthRoutes` to simplify server wiring; the authorization callback now includes the RFC 9207 `iss` parameter automatically. **Operational note:** mcp-oauth now rejects low-entropy AES-256 token-encryption keys (fewer than 16 distinct byte values). Real keys generated with `openssl rand -base64 32` or `openssl rand -hex 32` are unaffected; placeholder keys (all zeros, repeated bytes) will fail at startup with a clear error — rotate before upgrading.

### Removed

- ServiceClass CRD, API types, and Helm RBAC narrowing (third PR of the ServiceClass removal — see #632).
  - Deleted: `pkg/apis/muster/v1alpha1/serviceclass_types.go`, `helm/muster/crds/muster.giantswarm.io_serviceclasses.yaml`, `internal/api/{serviceclass,serviceinstance}.go`, `ServiceClassManagerHandler` interface and `Register/GetServiceClassManager`.
  - Helm RBAC drops `serviceclasses` and `serviceclasses/status` from the ClusterRole's `resources` lists.
  - **Operational note (REQUIRED before upgrading past this PR):** delete any `ServiceClass` custom resources in your cluster — they will be orphaned when the CRD is removed:

    ```
    kubectl delete serviceclasses.muster.giantswarm.io --all -A
    ```

    `MCPServer` and `Workflow` CRs are unaffected.
- ServiceClass-related MCP tools and CLI surface (first PR of the ServiceClass removal — see #632 for the rest).
  - MCP tools: `core_serviceclass_*`, `core_service_create`, `core_service_delete`, `core_service_get`, `core_service_validate`. Service inspection still works via `core_service_status`.
  - CLI subcommands: `muster create service`, `muster create serviceclass`, `muster check serviceclass`, `muster get serviceclass`, `muster list serviceclass`. The `service` and `serviceclass` values for `muster events --resource-type` and `muster test --concept` are also gone.
  - BDD scenarios: 22 `serviceclass-*` / `serviceclass_*` scenarios, 20 `service-*` scenarios that exercised user-creatable service instances (`service-create-*`, `service-delete*`, `service-get*`, `service-validate`, `service-lifecycle`, `service-persistence`, `service-restart`, `service-state-transitions`, `service-{start,stop}*`), and 6 cross-cutting end-to-end scenarios that depended on ServiceClass (`behavior-developer-onboarding-journey`, `example_with_mock`, `reconciler-status-sync`, `user-journey-platform-setup`, `workflow-conditional-service-check`, `workflow-run-with-serviceclass`) — 48 scenarios total. `service-get-non-existent` is renamed to `service-status-non-existent` and now exercises `core_service_status`.

  Note: the `ServiceClass` runtime, CRD, and Helm RBAC are still in place after this PR; they are removed in subsequent PRs tracked in #632.
- `api.RegisterConfig` and `api.GetConfig` deprecated wrappers (use `RegisterConfigHandler` / `GetConfigHandler` directly). All call sites already suppressed with `//nolint:staticcheck`; both are gone now along with the suppressions. ([#140](https://github.com/giantswarm/muster/issues/140))

### Fixed

- Aggregator-side PRM discovery (used by `core_auth_login`) now follows the MCP 2025-11-25 spec: it parses `WWW-Authenticate: ... resource_metadata=` from a 401, probes the path-based well-known URL (`<host>/.well-known/oauth-protected-resource<path>` — using the raw MCP URL path so `/v1/mcp` is preserved) before the root form, and exposes the RFC 9728 `resource` field on the parsed result. The previous implementation was root-only and silently dropped both signals.
- `pkg/oauth.Client.DiscoverMetadata` now handles path-bearing issuer URLs (e.g. `https://login.microsoftonline.com/<tenant>/v2.0`, Auth0 / Okta orgs with paths) per MCP 2025-11-25 §"Authorization Server Metadata Discovery": tries RFC 8414 path-insert, OIDC path-insert, then OIDC append. Previously these issuers fell through to a single no-path probe and failed; now they succeed.
- SSO token forwarding no longer hands downstream MCP servers a JWT whose `exp` is past the current time. Both ID-token storage paths — `storeIDTokenForSSO` (muster-issued tokens) and the forwarded-bearer mirroring in `injectExternalIDToken` (SSO-passthrough) — read the JWT's `exp` claim and persist it as the entry's `ExpiresAt`, so `IsExpiredWithMargin` evicts stale tokens after idle periods instead of treating zero `ExpiresAt` as never-expiring. Tokens without a parseable `exp` are refused (logged at warn level) — they were always malformed for muster's flow but the previous shape would have stored them with zero `ExpiresAt`, recreating the same leak. ([#549](https://github.com/giantswarm/muster/issues/549))
- Bump `mcp-oauth` to v0.2.86 with Dex scope filtering: non-standard client scopes like `claudeai` (sent by Claude) are now stripped before forwarding to Dex, preventing `invalid_scope` errors. Also includes Google scope filtering and `openid` force-merge from v0.2.84.
- CRD validation now uses the discovery API instead of listing `MCPServer` resources in the `default` namespace. With namespace-scoped RBAC (a `Role` limited to muster's own namespace), the previous probe failed with `Forbidden`, silently fell back to filesystem mode, and left configured `MCPServer` CRs unstarted (visible in logs as `Found 0 MCPServer definitions for auto-start processing` followed by `Deleting MCPServer service: <name>`).
- `call_tool` meta-tool now forwards the underlying tool's `isError` flag on the outer response. Previously the top-level `isError` was always `false` even when the wrapped tool returned an error, which was misleading for MCP clients that only inspect the top-level flag.
- Per-server OAuth flow and agent OAuth flow both now refuse to proceed when the authorization server's metadata does not advertise S256 in `code_challenge_methods_supported`, per MCP 2025-11-25 §"Authorization Code Protection". Previously an absent list was treated as "S256 OK" (the OAuth 2.1 default), which let muster start a flow that the AS could silently downgrade or reject at the token endpoint with a confusing error. `Metadata.SupportsPKCE` is renamed to `SupportsS256PKCE` to match the new semantics — only `pkg/oauth`-internal callers existed.

## [0.1.0] - 2026-02-23

### Changed

- **Session duration reduced from 90 days to 30 days.** The refresh token TTL now
  matches Dex's `absoluteLifetime` (720h). Previously, muster's 90-day refresh token
  outlived Dex's 30-day session, causing confusing failures when auto-refresh silently
  stopped working after day 30. Users who were logging in once every ~2 months will now
  need to re-authenticate every 30 days.
- **`muster auth status` now shows session expiry.** Instead of `Refresh: Available`,
  the output now shows `Session: ~29 days remaining (auto-refresh)`, giving users a
  concrete estimate of when re-authentication will be required.
- Access token TTL is now explicitly set to 30 minutes (matching Dex's `idTokens`
  expiry) instead of relying on the library default of 1 hour.
- Session duration is now configurable via `oauth.server.sessionDuration` in
  `config.yaml` (default: `720h` / 30 days).
- Kubernetes event emission is now disabled by default (alpha feature). Use `--enable-events` flag on `muster serve` or set `events: true` in `config.yaml` to opt in.
- Switch CI to `push-to-registries-multiarch` (`architect-orb@6.14.0`) with
  amd64-only on branches for faster PR feedback and full multi-arch on release
  tags. Chart tests now run before publishing to the app catalog.
- Update Dockerfile to multi-stage build with native cross-compilation support
  for multi-architecture images.

> **Note:** The Server-Side Meta-Tools Migration below is a **breaking change** that will be released as part of the next major version. External integrations should prepare for this change.

### Breaking Changes

#### Server-Side Meta-Tools Migration

Meta-tools (`list_tools`, `call_tool`, `describe_tool`, etc.) have moved from the agent to the aggregator server. This is a fundamental architectural change.

**What Changed:**

| Component | Before | After |
|-----------|--------|-------|
| **Agent** | Exposed 11 meta-tools + bridged to aggregator | Transport bridge only (OAuth shim + stdio↔HTTP) |
| **Aggregator** | Exposed 36+ core tools directly | Exposes ONLY meta-tools - no direct tool access |
| **Tool Access** | Direct tool calls to aggregator | All tool calls go through `call_tool` meta-tool |

**What Continues Working (Transparent Migration):**
- CLI commands (`muster list`, `muster get`, etc.) - client wraps calls automatically
- Agent REPL (`muster agent --repl`) - uses same client with transparent wrapping
- BDD test scenarios - test client wraps calls automatically
- MCP native protocol methods (`tools/list`, `resources/list`) - not affected

**What Breaks (Requires Update):**
- External integrations calling tools directly via HTTP
- Custom clients connecting directly to aggregator

**Migration for External Clients:**

```json
// Before: Direct tool call
{"method": "tools/call", "params": {"name": "core_service_list", "arguments": {}}}

// After: Wrap through call_tool
{"method": "tools/call", "params": {
  "name": "call_tool",
  "arguments": {"name": "core_service_list", "arguments": {}}
}}
```

**Benefits:**
- OAuth-capable clients can connect directly to server without agent
- Simpler agent architecture (~200 lines vs ~700 lines)
- Consistent tool visibility across all clients
- Centralized meta-tool logic

See [ADR-010](docs/explanation/decisions/010-server-side-meta-tools.md) for design details.

**Known External Integrations Affected:**
- Any HTTP clients calling the aggregator directly
- Custom MCP clients not using `muster agent`
- CI/CD pipelines with direct tool calls

**Recommended Migration Timeline:**
1. Review your integration code for direct tool calls
2. Update to wrap calls through `call_tool` meta-tool
3. Test with the new Muster version before deploying

### Changed
- **MCPServer CRD State Exposes Auth Required** - The MCPServer CRD now shows `Auth Required` state when a remote server returns 401 Unauthorized ([#337](https://github.com/giantswarm/muster/issues/337))
  - **Before**: 401 response mapped to `Connected` (hiding auth requirement)
  - **After**: 401 response shows as `Auth Required` in CRD state
  - This gives operators clear visibility into which servers need authentication
  - CLI output updated: `muster list mcpserver` now shows `Auth Required` state
  - SESSION column values updated: `OK` → `Authenticated`, `Required` → `Pending Auth`
  - Column header renamed: `AUTH` → `SESSION` to match `muster auth status` output

### Added
- **Reconciliation Framework** - Automatic synchronization between resource definitions (CRDs/YAML) and running services
  - Supports both Kubernetes mode (using controller-runtime informers) and filesystem mode (using fsnotify)
  - Auto-detects operating mode based on environment
  - Configurable per-resource-type enable/disable
  - Work queue with deduplication and exponential backoff
  - Status tracking and API for observability
  - See [ADR 007](docs/explanation/decisions/007-crd-status-reconciliation.md) for design details
- **StateChangeBridge** - Real-time sync of runtime state changes to CRD status subresources
  - Subscribes to orchestrator service state changes
  - Triggers reconciliation to update CRD status when services start/stop/crash

### Changed
- **BREAKING: Consolidated OAuth Configuration Naming** - OAuth configuration structure has been reorganized for clarity ([#324](https://github.com/giantswarm/muster/issues/324))
  - **Before**: `aggregator.oauth` (client/proxy) + `aggregator.oauthServer` (server protection)
  - **After**: `aggregator.oauth.mcpClient` (MCP client/proxy) + `aggregator.oauth.server` (server protection)
  - Both OAuth roles now live under a single `oauth` section with explicit `mcpClient`/`server` sub-sections
  - The `mcpClient` name makes it clear this is for authenticating TO remote MCP servers
  - CLI flags renamed: `--oauth` → `--oauth-mcp-client`, `--oauth-public-url` → `--oauth-mcp-client-public-url`
  - Helm values updated: `muster.oauth.*` → `muster.oauth.mcpClient.*`, `muster.oauthServer.*` → `muster.oauth.server.*`
  - CIMD configuration moved to nested structure: `cimdPath`/`cimdScopes` → `cimd.path`/`cimd.scopes`
  - Migration: Update configuration files and Helm values to use the new structure
- **BREAKING: CRD Status Field Changes** - Status fields have been redesigned for session-aware tool availability
  - **MCPServerStatus**: Removed `availableTools` (session-dependent), added `lastConnected` and `restartCount`
  - **ServiceClassStatus**: Replaced `available`/`requiredTools`/`missingTools`/`toolAvailability` with `valid`/`validationErrors`/`referencedTools`
  - **WorkflowStatus**: Replaced `available`/`requiredTools`/`missingTools`/`stepValidation` with `valid`/`validationErrors`/`referencedTools`/`stepCount`
  - Tool availability is now computed per-session at runtime, not stored in CRs
  - Existing CRs will have stale status fields that will be updated on first reconciliation
- Added Chart annotations to support OCI repositories.

### Fixed
- **Helm CiliumNetworkPolicy**: Fixed incorrect values path for OAuth storage check (now uses `.Values.muster.oauth.server.storage`)

### Added
- **Remote MCP Server Support for Kubernetes Environments**
  - Added comprehensive support for `stdio`, `streamable-http` and `sse` transport protocols
  - **Enhanced CRD Schema**: Updated `MCPServerSpec` to support all MCP server types
    - Added new config for `streamable-http` and `sse`: `url`, `headers` and `timeout` fields
    - Added mutual exclusion validation and required field validation using kubebuilder annotations
  - **New CLI Commands**: Added subcommands to use new type system
    - `muster create mcpserver <name> --type stdio` for local MCP servers
    - `muster create mcpserver <name> --type streamable-http` for HTTP remote servers
    - `muster create mcpserver <name> --type sse` for SSE remote servers
  - **Updated Examples**: Enhanced example files to demonstrate both local and remote configurations
  - **Kubernetes Deployment Ready**: Enables deployment patterns where Muster aggregator runs in cluster and connects to MCP servers deployed as separate Kubernetes services
- **Systemd Socket Activation Support**
  - Added `muster.socket` unit file for socket-activated systemd deployment
  - Modified `muster.service` to use socket activation on localhost:8090
  - Updated `scripts/setup-systemd.sh` and `scripts/dev-restart.sh` to handle socket activation
  - Make use of new dependency `github.com/coreos/go-systemd` to handle socket activation
- **Service Health Monitoring**
  - Added health checks for MCP servers using the `tools/list` JSON-RPC method
  - Added health checks for port forwards by testing TCP connectivity
  - Health checks run every 30 seconds for all running services
  - Health status is reported through the StateStore and displayed in the TUI
  - Created `ServiceHealthChecker` interface for extensible health checking
- **Improved State Reconciliation**
  - Implemented proper `ReconcileState()` method that syncs TUI state with StateStore
  - Updates service statuses, ports, PIDs, and error states from centralized store
  - Synchronizes cluster health information from K8sStateManager
  - Ensures UI consistency after startup and state changes
- **K8s Connections as Services**
  - Kubernetes connections are now modeled as services in the dependency graph
  - K8s connection health monitoring is now handled by dedicated K8s connection services
  - Unified service management architecture - all services (K8s, port forwards, MCPs) follow the same lifecycle
  - K8s connections can be stopped/restarted like other services with proper cascade handling
- Cascading stop functionality: stopping a service automatically stops all dependent services
- K8s connection health monitoring with automatic service lifecycle management
- Port forwards now depend on their kubernetes context being authenticated and healthy
- The kubernetes MCP server depends on the management cluster connection
- When k8s connections become unhealthy, dependent services are automatically stopped
- Manual stop (x key) now uses cascading stop to cleanly shut down dependent services
- New `StartServicesDependingOn` method in ServiceManager to restart services when dependencies recover
- New `orchestrator` package that manages application state and service lifecycle for both TUI and non-TUI modes
- New `HealthStatusUpdate` and `ReportHealth` for proper health status reporting
- Health-aware startup: Services now wait for their K8s dependencies to be healthy before starting
- Add comprehensive dependency management system for services
  - Services now track why they were stopped (manual vs dependency cascade)
  - Automatically restart services when their dependencies recover
  - Ensure correct startup order based on dependency graph
  - Prevent manually stopped services from auto-restarting
- **Phase 1 of Issue #45: Message Handling Architecture Improvements**
  - Added correlation ID support to `ManagedServiceUpdate` for tracing related messages and cascading effects
  - Implemented configurable buffer strategies for TUI message channels:
    - `BufferActionDrop`: Drop messages when buffer is full
    - `BufferActionBlock`: Block until space is available
    - `BufferActionEvictOldest`: Remove oldest message to make room for new ones
  - Added priority-based buffer strategies to handle different message types differently
  - Introduced `BufferedChannel` with metrics tracking (messages sent, dropped, blocked, evicted)
  - Enhanced orchestrator with correlation tracking for health checks and cascading operations
  - Updated service manager to use new correlation ID system for better debugging
  - Added comprehensive test coverage for buffer strategies and correlation tracking
- **Phase 2 of Issue #45: State Consolidation**
  - Implemented centralized `StateStore` as single source of truth for all service states
  - Added `ServiceStateSnapshot` for complete state information with correlation tracking
  - Introduced state change subscriptions with `StateSubscription` for reactive updates
  - Enhanced `ServiceReporter` interface with `GetStateStore()` method for direct state access
  - Updated `TUIReporter` and `ConsoleReporter` to use centralized state management
  - Migrated `ServiceManager` from local state tracking to centralized `StateStore`
  - Added comprehensive metrics tracking for state changes and subscription performance
  - Implemented state change event system with old/new state tracking
  - Added support for filtering services by type and state
  - Maintained full backwards compatibility while eliminating state duplication
- **Phase 3 of Issue #45: Structured Event System**
  - Implemented comprehensive event hierarchy with semantic event types:
    - `ServiceStateEvent` for service lifecycle changes with old/new state tracking
    - `HealthEvent` for cluster health status updates
    - `DependencyEvent` for cascade start/stop operations
    - `UserActionEvent` for user-initiated actions
    - `SystemEvent` for system-level operations
  - Added `EventBus` interface with publish/subscribe functionality
  - Implemented flexible event filtering system with composable filters:
    - Filter by event type, source, severity, correlation ID
    - Combine filters with AND/OR logic for complex subscriptions
  - Created `EventBusAdapter` for backwards compatibility with existing `ServiceReporter` interface
  - Added comprehensive event metrics tracking (published, delivered, dropped events)
  - Implemented both handler-based and channel-based event subscriptions
  - Added event severity levels (trace, debug, info, warn, error, fatal) for better categorization
  - Enhanced correlation tracking with event metadata support
  - Provided thread-safe concurrent event publishing and subscription management
  - Added extensive test coverage for all event types and bus functionality
- **Phase 4 of Issue #45: Testing and Polish**
  - Added comprehensive integration tests covering end-to-end event flows
  - Implemented performance monitoring utilities with `PerformanceMonitor` and metrics tracking
  - Created event batching system with `EventBatchProcessor` for high-volume scenarios
  - Built `OptimizedEventBus` with configurable performance optimizations
  - Added object pooling system with `EventPoolManager` to reduce GC pressure
  - Implemented extensive error recovery testing including panic handling
  - Added memory usage monitoring and subscription cleanup verification
  - Created comprehensive documentation covering architecture, usage, and best practices
  - Fixed race conditions in event bus concurrent access patterns
  - Enhanced thread safety across all components with proper synchronization
  - Provided migration guides and troubleshooting documentation
  - Achieved high test coverage with robust integration and unit tests
- **Improved Dependency Management for Service Restarts**
  - When restarting a service, its dependencies are now automatically restarted if they're not active
  - This ensures services always have their requirements satisfied (e.g., restarting Grafana MCP will also restart its port forward if needed)
  - Dependencies are restarted regardless of their stop reason to guarantee service requirements
  - Clear manual stop reason when restarting a service to allow proper dependency management
- **Implemented Issue #46: Improved State Management Between TUI and Orchestrator**
  - **Phase 1: Unified State Management**
    - Added helper methods to TUI Model to use StateStore as single source of truth
    - Implemented state reconciliation on TUI startup to ensure consistency
    - Updated TUI controller to use StateStore instead of directly updating model maps
    - Eliminated state duplication between TUI Model and StateStore
  - **Phase 2: Message Sequencing**
    - Added sequence numbers to `ManagedServiceUpdate` for proper message ordering
    - Implemented `MessageBuffer` for handling out-of-order messages
    - Added global sequence counter with atomic operations for thread safety
  - **Phase 3: Enhanced Correlation Tracking**
    - Added `CascadeInfo` type for tracking cascade relationships between services
    - Added `StateTransition` type for tracking state changes with full context
    - Enhanced StateStore to record state transitions and cascade operations automatically
    - Updated orchestrator to record cascade operations for better observability
  - **Phase 4: Improved Error Handling**
    - Added retry logic for critical updates that are dropped due to buffer overflow
    - Implemented `BackpressureNotificationMsg` for user notifications about dropped messages
    - Added configurable retry attempts with exponential backoff
    - Enhanced TUIReporter with retry queue processing and user feedback
- **Comprehensive Documentation Suite**
  - Added [Architecture Overview](docs/architecture.md) documenting system design, components, and principles
  - Created [Quick Start Guide](docs/quickstart.md) for new users to get up and running quickly
  - Added [Troubleshooting Guide](docs/troubleshooting.md) with common issues and solutions
  - Enhanced development documentation with recent architectural improvements
  - Documented dependency management, state management, and message flow in detail
- **Configurable Namespace for CR Discovery**
  - Added `namespace` configuration option to `config.yaml` for Kubernetes CR discovery
  - Allows specifying which namespace to use for MCPServer, ServiceClass, and Workflow resources
  - Defaults to `"default"` when not specified
  - Enables muster to work properly in multi-namespace Kubernetes environments

### Changed
- **Aggregator Config**
  - Drop the "Enabled" field (always enabled in modes where it's used)
- **Service Manager Refactoring**
  - ServiceManager now accepts an optional KubeManager parameter for K8s connection services
  - Added support for K8s connection services in the service lifecycle management
  - Improved service stop handling to report "Stopping" state before closing channels
- **Orchestrator Improvements**
  - Removed old health monitoring methods in favor of K8s connection services
  - Updated dependency graph to use service labels for K8s connections (e.g., "k8s-mc-mymc" instead of "k8s:context-name")
  - Improved service restart logic to properly handle dependencies
- Dependency graph now includes K8sConnection nodes as fundamental dependencies
- Service manager's StopServiceWithDependents method handles cascading stops
- Health check failures trigger automatic cleanup of dependent services
- Non-TUI mode now uses the orchestrator for health monitoring and dependency management
- TUI mode no longer performs its own health checks - the orchestrator handles all health monitoring and the TUI only displays results
- Proper separation of concerns: orchestrator manages health checks and service lifecycle, TUI only displays status
- Orchestrator now performs initial health check before starting services
- Refactored TUI message handling system
  - Introduced specialized controller/dispatcher for better separation of concerns
  - Controllers now focus on single responsibilities
  - Better error handling and logging throughout the message flow
- Improved startup behavior - the UI now shows loading state until all clusters are fully loaded
- Port forwards no longer start before K8s health checks pass - orchestrator now checks K8s health before starting dependent services
- `ManagedServiceUpdate` now includes `CorrelationID`, `CausedBy`, and `ParentID` fields for tracing
- `TUIReporter` now uses configurable buffered channels instead of simple channels
- Service state updates now include correlation information in logs
- Orchestrator operations (stop/restart) now generate and track correlation IDs
- Removed unused `DependsOnServices` field from `MCPServerDefinition` - MCP servers never depend on other MCP servers
- Enhanced `RestartService` to use the new `startServiceWithDependencies` method for dependency-aware restarts
- Updated `handleServiceStateUpdate` to properly restart services with their dependencies
- **Improved Service Monitoring**
  - Fixed `monitorAndStartServices` to respect `StopReasonDependency` - services stopped due to dependency failure won't be restarted until their dependencies are restored
  - Added automatic restart of dependent services when a dependency becomes healthy again
  - Added 1-second delay before restarting services to ensure ports are properly released

### Fixed
- **Exit CLI on standalone server failure**
  - When the mcp-aggregator service (server) fails, the CLI now terminates gracefully
- **Port Forwarding State Issue**
  - Fixed issue where port forwarding services would get stuck in "Stopping" state
  - ServiceManager now properly reports the "Stopping" state before closing the stop channel
  - Port forwarding processes correctly transition to "Stopped" state
- **Code Cleanup**
  - Removed commented-out `mcpServerProcess` struct that was marked for deletion
  - Removed duplicate `updatePortForwardFromSnapshot` and `updateMcpServerFromSnapshot` methods
  - Cleaned up unused code and improved code organization
- **Dependency-Related Fixes**
  - Fixed issue where MCP servers would restart even when their port forward dependencies were stopped
  - Services with `StopReasonDependency` now properly wait for their dependencies to be restored
  - When a service becomes healthy, its dependent services that were stopped due to dependency failure are automatically restarted
  - Fixed "address already in use" errors by adding proper restart delay
- **Fixed spurious error logs when stopping MCP servers**
  - Suppressed expected "file already closed" errors that occurred when stopping MCP server processes
  - Added proper error handling for both stdout and stderr pipe closures during shutdown
  - These were harmless errors but created unnecessary noise in the logs
- **Fixed cascade stops not triggering when K8s connections fail**
  - When a K8s connection transitions to Failed state (e.g., due to network issues), all dependent services (port forwards and MCP servers) are now properly stopped
  - This prevents orphaned services from continuing to run when their underlying K8s connection is no longer healthy
  - Services will automatically restart when the K8s connection recovers
- Set config directory early to avoid bugs handling the empty string (those should be fixed with this change as well)

### Documentation
- Added comprehensive documentation about dependency graph implementation
- Enhanced dependency management documentation with detailed examples
- Added explanation of dependency rules and startup/restart behavior
- Documented the relationship between stop reasons and automatic recovery
- Created comprehensive architecture documentation covering all major components
- Added troubleshooting guide with detailed debugging techniques
- Created quick start guide for new users
- Updated development guide with recent architectural improvements
- Documented the entire dependency management system with visual diagrams
- **Updated outdated documentation sections**
  - Removed obsolete "Package Design for Shared Core Logic" section from development.md
  - Updated development.md to reference the unified service architecture
  - Fixed test examples in development.md to match current implementation
  - Updated README.md prerequisites to remove mcp-proxy requirement
  - Clarified non-TUI mode behavior in README.md
  - Rewritten MCP Integration Notes in README.md to reflect YAML configuration system

### Technical Details
- New helper functions: `NewManagedServiceUpdate()`, `WithCause()`, `WithError()`, `WithServiceData()`
- New types: `BufferStrategy`, `BufferedChannel`, `ChannelMetrics`, `ChannelStats`
- Backwards compatibility maintained for existing interfaces
- All existing tests updated and new comprehensive test suite added

## [Previous]

### Added
- Enhanced MCP server configuration and management capabilities

### Changed
- MCP server configuration now only supports `localCommand` type for simplicity and reliability

### Technical Details
- Streamlined MCP server architecture by removing container support
- Simplified MCP server lifecycle management

## [0.6.0] - 2025-01-15

[Unreleased]: https://github.com/giantswarm/muster/compare/v0.3.12...HEAD
[0.3.12]: https://github.com/giantswarm/muster/compare/v0.1.0...v0.3.12
[0.1.0]: https://github.com/giantswarm/muster/compare/v0.0.236...v0.1.0
