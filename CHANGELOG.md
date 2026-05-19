# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- `internal/reconciler/agentgateway` domain package: typed `Config` / `Backend` / `Authn` / `Policy` / `HTTPTarget` / `StdioTarget` models built from `v1alpha1.MCPServer`. Pure functions only — no Kubernetes client, no filesystem, no `internal/api` dependencies — so downstream emitter adapters (cluster, filesystem) and translation tests can share one source of truth for what a "compiled MCPServer" looks like.
- `agentgateway.Applier` / `agentgateway.Deleter` port interfaces and `agentgateway.ApplierFor(mode)` factory. Lets `MCPServerReconciler` write agentgateway config through a single port whose concrete backend (cluster-mode k8s adapter, filesystem-mode yaml adapter) is selected at startup.
- `internal/reconciler/agentgateway/k8s` Applier: emits the `AgentgatewayBackend` / `HTTPRoute` / `AgentgatewayPolicy` CRD stack (`agentgateway.dev/v1alpha1`) for each MCPServer in cluster mode, with `OwnerReferences` set so deletion cascades. Stdio MCPServers in cluster mode are rejected with `k8s.ErrStdioNotSupportedInCluster` (per-MCPServer pod isolation deferred).
- `MCPServerReconciler` now compiles each reconciled MCPServer into an `agentgateway.Config` and applies it via the `agentgateway.Applier` port (cluster mode → `k8s` adapter, filesystem mode → `yaml` adapter). Cluster-mode wiring requires a `StatusUpdater` so the reconciler can surface unsupported-transport errors (stdio) on `MCPServer.status.conditions`; the resolved `OwnerReference` is cached for the lifetime of the reconciler.
- `agentgateway.Authn.RequiresPolicy() bool` and `agentgateway.HTTPTarget.Validate() error` on the agentgateway domain config.

### Changed

- Container image build no longer compiles the Go binary inside `docker buildx`. `go-build` now produces both `muster-linux-amd64` and `muster-linux-arm64` in one job (architect-orb `architectures` parameter) and the Dockerfile copies the matching binary from the workspace. Removes the duplicate compile and the QEMU-emulated arm64 cross-build on tag releases; `push-to-registries` auto-derives `--platform` from the workspace `.platforms` file.
- Build identifiers (`version`, `gitSHA`, `buildTimestamp`) now live in `pkg/project` instead of `main`. Both injection paths populate the same vars: goreleaser writes the semver tag + short commit + date for release archives, architect-orb's `go-build` writes the commit SHA + UTC timestamp for container images. `muster version` prefers the tag, falls back to the SHA, falls back to `dev`, and additionally prints the commit SHA and build timestamp on dedicated lines.
- Bump `giantswarm/architect` orb to `8.2.2` and re-enable cosign keyless chart signing (`sign: false` removed from every `push-to-app-catalog*` invocation). v8.2.2 ships [architect-orb#772](https://github.com/giantswarm/architect-orb/pull/772) which upgrades the `app-build-suite` executor image from `1.8.0-circleci` to `1.8.1-circleci` -- the new image includes the `cosign` binary that v8.2.0's chart signing defaults require. Closes [architect-orb#769](https://github.com/giantswarm/architect-orb/issues/769).
- Bump `giantswarm/architect` orb to `8.2.1` to pick up [architect-orb#767](https://github.com/giantswarm/architect-orb/pull/767): `image-login-to-registries` is now POSIX-portable, unblocking `architect/sync-china-registry` (the gsoci -> Aliyun mirror via the in-China `giantswarm/galaxy-runner`). The v8.1.0 refactor accidentally introduced bash-only `${!var}` indirect expansion in the shared login command, which BusyBox `/bin/sh` (used by the regctl executor) rejected with `bad substitution` -- so no Aliyun mirror has been happening since the migration to `split-china-push: true`. v8.2.x also enables cosign keyless signing, SLSA provenance, and SBOM attestations by default for public images and charts.
- Disable cosign keyless chart signing on the `push-to-app-catalog*` jobs (`sign: false`). The architect orb's `push-to-app-catalog` defaults `sign` to `true` since v8.2.0 and shells out to `cosign`, but this repo uses `executor: app-build-suite` (so the `app_build_suite` Python CLI is available to package the chart with metadata) and the `app-build-suite` image doesn't ship `cosign`. Without this opt-out, every chart push fails on the `Mint Sigstore OIDC token` step with `cosign: command not found`. To be removed once architect-orb makes `cosign-prepare` resilient to a missing binary (or ships cosign in the `app-build-suite` executor) -- tracked in [architect-orb#769](https://github.com/giantswarm/architect-orb/issues/769).
- Replace the `push-to-gsoci-release` + `push-to-all-registries-release` workaround pair with a single `push-to-registries-release` job using `split-china-push: true` and a companion `sync-china-registry` job. The cross-Pacific `docker buildx` push to the Aliyun mirror is replaced with `regctl image copy` (gsoci -> Aliyun) executed on the in-China `giantswarm/galaxy-runner` self-hosted CircleCI runner via the Singapore geo-replica. The chart catalog publish still does not gate on Aliyun.
- Migrate image pushes from the deprecated `architect/push-to-registries-multiarch` job to `push-to-registries` with `multiarch: true`. Picks up the orb v8.1.0 QEMU/binfmt auto-registration, hardened buildx bootstrap, and standard OCI image labels.

### Added

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

- Workflow execution tools were advertised twice — both as the documented `workflow_<workflow-name>` and as `core_action_<workflow-name>` — through `list_tools` / `list_core_tools` / `filter_tools`. The `core_action_*` variant is not part of the public surface and the aggregator's call routing does not recognize it (calls fail with "no handler found"), so clients that picked it up from discovery hit non-functional tools. The aggregator now rewrites the workflow provider's internal `action_<name>` tools to `workflow_<name>` (no `core_` prefix) when listing, matching the architecture spec; management tools (`workflow_list`, `workflow_get`, …) continue to be advertised as `core_workflow_*`. Pure listing fix — execution routing was already correct.
- `Workflow` and `ServiceClass` CRD validation rejected scalar values in step args (`spec.steps[*].args.<key>: must be of type object`), making the documented YAML form (`namespace: kube-system`, `limit: 30`, `allNamespaces: false`) unusable through `kubectl apply`. Step args, condition args, JSONPath maps, and `ArgDefinition.Default` now use `apiextensionsv1.JSON` instead of `runtime.RawExtension`, which controller-tools emits as `additionalProperties: {x-kubernetes-preserve-unknown-fields: true}` (no `type: object` constraint), so scalars, objects, and arrays all validate. Wire format and stored values are unchanged; existing workflows with object-only args keep working.
- OAuth server initialisation built its own text-format `slog.Logger` writing to stdout when `--debug` was set, so in-pod log lines from the `mcp-oauth` library (Valkey storage, redirect-URI security, OIDC discovery, rate limiters, audit, instrumentation) appeared as text on stdout instead of flowing through the project's JSON handler. `createOAuthServer` now uses `slog.Default()` and inherits the level set by `logging.Init`, so all in-pod log lines share one format and one writer.

### Removed

- **Breaking (external consumers of `pkg/oauth`):** `pkg/oauth.IDTokenClaims` struct and `ParseIDTokenClaims` function removed. Replaced by typed accessors in `pkg/oauth/jwt.go` — `Subject`, `Email`, `Expiry`, `Issuer`, `IsExpired` — each returning `(value, error)` so callers can distinguish "missing claim" from "decode failed".

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

[Unreleased]: https://github.com/giantswarm/muster/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/giantswarm/muster/compare/v0.0.236...v0.1.0
