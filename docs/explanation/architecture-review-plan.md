# Plan: muster as one binary with three modes

## Status

Architecture exploration. Inventory + target shape + open questions, designed to inform Miro architecture diagrams and subsequent migration sequencing.

## Context

Today muster (~75k LOC non-test) bundles ten roles into one binary. Forcing functions:

- **ADR-012** (PR #613): extract auth into a token broker, adopt agentgateway as front door
- **MCP spec 2025-11-25**: mandates RFC 8707 audience-locked tokens
- **Per-customer multi-tenancy**: agent identity / policy / audit move to gateway
- **Filesystem mode** has a real lab/local-dev use case and **stays**
- **ServiceClass** has no remaining product use case → **drop entirely**

Outcome: same monorepo, same binary, three subcommands. Internal seams (Go interfaces) so future binary splits are mechanical when scale triggers hit.

## The shape (and what each component does)

```
muster (binary, single Go module)
│
├── muster serve [--filesystem-mode]   pod: muster-server (K8s) or local lab process
│   │
│   │   The aggregator. Watches MCPServer / Workflow / WorkflowRun entity defs
│   │   (from K8s CRDs OR filesystem YAML), maintains backend MCP connections,
│   │   serves the MCP HTTP API, executes workflow steps. controller-runtime
│   │   is the library it uses internally for CRD watches in K8s mode. No
│   │   separate "controller" component — the aggregator IS the operator.
│   │
│   ├── EntityProvider          K8s mode: controller-runtime watches CRDs
│   │                           filesystem mode: fsnotify watches YAML files
│   │
│   ├── connection manager      Maintains backend MCP connections per
│   │                           MCPServer entity; writes status (CRD in K8s
│   │                           mode, in-memory in filesystem mode)
│   │
│   ├── MCP HTTP server         Serves /mcp to the agentgateway
│   │
│   ├── workflow execution      WorkflowRun Reconcile() runs steps in-process,
│   │                           calls into the aggregator's tool catalog
│   │
│   └── admin UI                CRD/entity inspector
│
├── muster broker                       pod: muster-broker (K8s only — lab uses
│                                       --no-auth on serve)
│   │
│   │   Credential service per ADR-012. OAuth RP, token storage, RFC 8693
│   │   exchange. gRPC server consumed by `muster serve` over mTLS. Same
│   │   image as `muster serve`, different command launched per pod.
│   │
│   ├── OAuth RP endpoints      /oauth/authorize, /token, /callback, /.well-known/*
│   ├── token store             in-memory or Valkey
│   ├── token exchange          RFC 8693 against per-MC Dexes
│   └── gRPC server             CredentialBroker interface
│
└── muster agent                        laptop binary, ships via brew/scoop
    │
    │   Narrowed role: with streamable-http MCP, IDEs connect to the
    │   agentgateway URL directly — no stdio shim needed in the steady
    │   state. The agent's remaining responsibilities are human-facing:
    │   the OAuth callback listener (browser-based PKCE flow needs a
    │   localhost listener), CLI commands, REPL, and local token store.
    │   The stdio MCP shim stays as a transitional adapter for IDEs that
    │   don't yet support streamable-http.
    │
    ├── PKCE callback listener         localhost:NNNN for OAuth code capture
    ├── XDG token store                local token persistence
    ├── CLI commands                    auth, list, get, call, check, ...
    ├── REPL                            `muster agent`
    └── stdio MCP shim (transitional)  `muster agent --mcp-server` for legacy IDEs
```

**One binary. One Go module. Same image deployed as two pods (server + broker) with different `command:`. Agent ships separately to laptops. agentgateway is external (CNCF), not in the muster binary.**

## Why no separate "muster-controller"

The aggregator IS the operator for MCPServer/Workflow/WorkflowRun CRDs. controller-runtime is the *library* it uses internally to watch CRDs and run reconcile loops. There's no separate process.

| Job | Where it runs |
|---|---|
| Watch MCPServer CRDs | aggregator (via controller-runtime in K8s mode, fsnotify in filesystem mode) |
| Maintain backend MCP connections | aggregator |
| Validate Workflow CRDs | aggregator |
| Run WorkflowRun steps | aggregator (`Reconcile()` of WorkflowRun controller) |
| Write CRD `.status` | aggregator |
| Serve MCP API | aggregator |

Splitting reconcile into a separate "controller" component would require muster-controller to call back into the aggregator for every step (network hop) and would split a single concern into two pods for no benefit. Same logic Tekton uses: TaskRun reconciliation IS the executor.

## Filesystem mode preservation

Filesystem mode survives the controller-runtime adoption by using a parallel `EntityProvider` impl, not by avoiding controller-runtime entirely.

```go
// Aggregator depends on this interface, not on controller-runtime directly
type EntityProvider interface {
    WatchMCPServers(ctx context.Context) <-chan EntityChange[MCPServer]
    WatchWorkflows(ctx context.Context) <-chan EntityChange[Workflow]
    WatchWorkflowRuns(ctx context.Context) <-chan EntityChange[WorkflowRun]
    UpdateStatus(ctx context.Context, kind, name string, status any) error  // no-op in filesystem mode
}

// Two implementations
type KubernetesEntityProvider struct { /* uses controller-runtime */ }
type FilesystemEntityProvider struct { /* uses fsnotify + YAML reader */ }
```

| Behavior | K8s mode (`muster serve`) | Filesystem mode (`muster serve --filesystem-mode`) |
|---|---|---|
| Entity defs source | CRDs in K8s | YAML files in `./.muster/` |
| Watch mechanism | controller-runtime informers | fsnotify (`internal/reconciler/filesystem_detector.go` logic) |
| Status persistence | CRD `.status` writes | None (logs + admin UI only) |
| Leader election | yes (controller-runtime mgr) | no (single-process lab) |
| WorkflowRun persistence | CRD | in-memory only |
| Auth | requires broker | `--no-auth` flag bypasses |
| Use case | per-customer cluster deploy | local lab, dev, demo |

The existing `internal/client/{kubernetes_client,filesystem_client}.go` MusterClient abstraction already does this dual-backend pattern, just messier. The refactor consolidates around the EntityProvider interface and lets controller-runtime own the K8s-side path properly.

## Code organization: DDD bounded contexts + hexagonal + controller-runtime idiomatic

Each bounded context (aggregation, workflow, broker, agent) owns its own domain + ports + adapters. Ports are defined by the consumer (named for what the consumer *needs*), not provider-side. Controllers are *K8s adapters* in hexagonal terms — not a separate concept.

### Directory structure

```
muster/
├── api/v1alpha1/                          CRD types (kubebuilder convention)
│   ├── mcpserver_types.go
│   ├── workflow_types.go
│   ├── workflowrun_types.go
│   └── zz_generated_deepcopy.go
│
├── cmd/muster/
│   ├── main.go                            cobra root
│   ├── serve.go                           wires aggregation context (composition root)
│   ├── broker.go                          wires broker context (composition root)
│   └── agent.go                           wires agent context (composition root)
│
├── internal/
│   ├── aggregation/                       BOUNDED CONTEXT: MCP aggregation
│   │   ├── aggregator.go                  domain
│   │   ├── capability_store.go            domain
│   │   ├── ports.go                       interfaces this context needs:
│   │   │                                    CredentialBroker, EntityProvider, WorkflowExecutor
│   │   ├── http/                          adapter: MCP HTTP server (driving side)
│   │   ├── kubernetes/                    adapter: K8s EntityProvider + controllers
│   │   │   ├── entity_provider.go         controller-runtime informers
│   │   │   ├── mcpserver_controller.go    K8s adapter — Reconcile drives aggregation
│   │   │   ├── workflow_controller.go
│   │   │   └── workflowrun_controller.go
│   │   ├── filesystem/                    adapter: EntityProvider impl using fsnotify
│   │   └── broker/                        adapter: gRPC client implementing CredentialBroker
│   │
│   ├── workflow/                          BOUNDED CONTEXT: workflow execution
│   │   ├── executor.go                    domain
│   │   ├── template.go                    domain (Sprig sandbox)
│   │   ├── ports.go                       ToolCaller — what workflow needs from aggregation
│   │   └── aggregator/                    adapter: in-process ToolCaller (calls aggregation)
│   │
│   ├── broker/                            BOUNDED CONTEXT: credential broker
│   │   ├── manager.go                     domain (token exchange routing logic)
│   │   ├── session.go                     domain
│   │   ├── exchange.go                    domain (RFC 8693 dispatch decisions)
│   │   ├── ports.go                       only ports the domain genuinely owns
│   │   └── grpc/                          adapter: gRPC server (driving side, public API)
│   │
│   │   The broker uses giantswarm/mcp-oauth as a library — not a port we
│   │   define. mcp-oauth already provides TokenStore (memory + Valkey with
│   │   AES-256-GCM), Dex IdP integration, OAuth RP endpoints, PKCE,
│   │   audit. The broker domain composes these and adds:
│   │     - cross-IdP token exchange routing (which Dex for which audience)
│   │     - per-customer client credentials handling
│   │     - gRPC server exposing GetCredential / BeginOAuthFlow / etc.
│   │
│   ├── agent/                             BOUNDED CONTEXT: laptop CLI
│   │   ├── repl.go                        domain
│   │   ├── ports.go                       MusterServerClient
│   │   ├── stdio/                         adapter: stdio MCP server
│   │   ├── http/                          adapter: client to muster serve / gateway
│   │   ├── oidc/                          adapter: laptop OIDC client
│   │   └── cli/                           adapter: cobra commands + table formatting
│   │
│   ├── logging/                           cross-cutting (slog wrapper if any value over stdlib)
│   └── config/                            cross-cutting (config types + loaders)
│
└── helm/, examples/, hack/, Makefile, etc.
```

Notes on the structure:
- **No `pkg/apis/`**. Only add when an external GS service genuinely imports muster's CRD types (klaus, mcp-prometheus). Speculative public promises are YAGNI.
- **No `internal/shared/`**. Flatten cross-cutting utilities (`logging`, `config`) at the top of `internal/`. "Shared" packages grow into kitchen sinks.
- **Controllers live inside `internal/aggregation/kubernetes/`**, not at top level. Controllers ARE the K8s adapter for aggregation; placing them here keeps hexagonal honest. (The kubebuilder convention of `controllers/` at root pre-dates muster's bounded-context structure and breaks the boundary.)

### Hexagonal rules (enforced via import-boundary CI)

1. `internal/aggregation/` domain imports nothing from `internal/workflow/`, `internal/broker/`, or `internal/agent/`. It depends only on the ports it defines.
2. Adapters import the domain they serve (e.g., `internal/workflow/aggregator/` imports `internal/aggregation`); domains never import adapters.
3. `internal/controller/` is a K8s adapter — imports `internal/aggregation` (driven side) + `api/v1alpha1` (CRD types).
4. `cmd/muster/serve.go` is the composition root: instantiates domains, picks adapters, wires them together. It's the only place that knows the full graph.
5. `cmd/muster/broker.go` wires only the broker context; doesn't initialize aggregation.
6. Cross-context calls happen only via ports defined by the consumer.

### Filesystem mode in this structure

Filesystem mode is an `EntityProvider` adapter at `internal/aggregation/filesystem/`. The composition root picks at startup:

```go
// cmd/muster/serve.go
var provider aggregation.EntityProvider
if filesystemMode {
    provider = filesystem.New(configPath)        // fsnotify + YAML reader
} else {
    provider = kubernetes.New(mgr.GetCache())    // controller-runtime informers
}
agg := aggregation.New(provider, brokerClient, ...)
```

The aggregation domain doesn't know which is wired. WorkflowRun in filesystem mode is in-memory only (no CRD persistence) — a property of the filesystem adapter, not a separate concept.

In filesystem mode, the controllers in `internal/controller/` are not started by the manager. The filesystem adapter directly drives the aggregation domain's connection updates as files change. Status reporting goes to logs and admin UI instead of CRD writes.

### Ports (defined per-context, not in a shared package)

| Context | Port | Implemented by |
|---|---|---|
| `internal/aggregation/` | `CredentialBroker` | `internal/aggregation/broker/` (gRPC client) |
| `internal/aggregation/` | `EntityProvider` | `internal/aggregation/kubernetes/` OR `internal/aggregation/filesystem/` |
| `internal/aggregation/` | `WorkflowExecutor` | `internal/workflow/aggregator/` (in-process) — or future gRPC client |
| `internal/workflow/` | `ToolCaller` | `internal/workflow/aggregator/` (in-process) — or future gRPC client |
| `internal/broker/` | (no IdentityProvider port) | uses giantswarm/mcp-oauth's Dex provider directly as a library |
| `internal/broker/` | (no TokenStore port) | uses giantswarm/mcp-oauth's TokenStore (memory + Valkey + AES-256-GCM) directly as a library |
| `internal/agent/` | `MusterServerClient` | `internal/agent/http/` |

When a future binary split happens, the in-process adapter is replaced with a gRPC adapter. The domain doesn't change. The composition root (`cmd/muster/<mode>.go`) picks the new adapter.

### Controller-runtime idiomatic specifics

- `api/v1alpha1/` instead of `pkg/apis/muster/v1alpha1/` — `api/` is what `kubebuilder init` produces and is the standard location for controller-runtime projects. Move during Phase 4.
- Each controller is a thin K8s adapter: `Reconcile(ctx, req)` translates `req` into a domain call on `internal/aggregation/`.
- Controllers receive `client.Client` and the aggregation domain via constructor — no service locator.
- `+kubebuilder:rbac` markers on controllers; RBAC manifests generated.
- EventRecorder from `mgr.GetEventRecorderFor()` — not a custom events package.
- Reconcile signature: `(ctx context.Context, req ctrl.Request) (ctrl.Result, error)` — standard.
- Webhook conversions (when CRDs evolve) in `api/v1alpha1/<kind>_webhook.go`.

## Bounded contexts in current muster (verified against import graph)

The actual coupling is more skewed than narrative suggested. Numbers from the import-graph analysis:

### Pure leaves (zero internal deps)
- `internal/admin` (603 LOC) — clean module, used only by aggregator
- `internal/config` (841 LOC) — used everywhere
- `internal/context` (456 LOC) — session/endpoint helpers
- `internal/template` (263 LOC) — templating engine, used by workflow + services + orchestrator + testing

### Service-locator-only (clean by convention)
- `internal/metatools` — only api
- `internal/client` — only api
- `internal/reconciler` — only api
- `internal/teleport` — only api (going away)

### Heavy coupling
- `internal/aggregator` (~10k LOC) — imports admin, api, config, **events, mcpserver, metatools, oauth, server**. **8 internal deps.** Most-coupled package.
- `internal/orchestrator` (~2k LOC) — imports api, config, mcpserver, services, template
- `internal/workflow` (~3.9k LOC) — imports api, client, config, events, template
- `internal/agent` (~10.7k LOC) — imports api, context, metatools, testing
- `internal/cli` (~4.7k LOC) — imports agent, api, config, context, metatools
- `internal/app` (~1.3k LOC) — imports 12 packages. Bootstrap.

### Smell to fix
- `internal/events` imports `internal/cli` — wrong layering

### Domain clusters
- **CLI cluster**: agent + cli + context + metatools (already isolated, → `muster agent` mode)
- **MCP runtime**: aggregator + admin + server + oauth + mcpserver + metatools + events
- **Workflow**: workflow + template (clean)
- **K8s glue**: reconciler + client (clean)
- **Going away**: teleport, serviceclass

## Component file mapping

### `muster serve` (the aggregator)

**Stays (post-cleanup):**
- `internal/aggregator/` minus auth/denylist (~6.5k after auth extraction)
- `internal/aggregator/capability_store*.go` (per-session, ADR-006 dependency)
- `internal/aggregator/session_connection_pool.go` (ADR-011)
- `internal/mcpserver/` connection-side bits
- `internal/workflow/` (executor + types) — workflow execution goroutines run inside WorkflowRun controller's Reconcile
- `internal/template/` (with Sprig sandbox)
- `internal/metatools/` — `filter_tools` only
- `internal/admin/` (CRD inspector UI)
- `internal/client/{kubernetes_client,filesystem_client}.go` consolidated under EntityProvider interface
- `internal/config/`, `internal/context/`
- `pkg/apis/muster/v1alpha1/` (shared types)

**Becomes thin (rewires through broker):**
- `internal/aggregator/auth_resource.go` (476 LOC → ~120; `WatchAuthEvents` consumer)
- `internal/aggregator/auth_tools.go` (469 LOC → ~120; `BeginOAuthFlow` / `RevokeSession` calls)
- `internal/aggregator/connection_helper.go` (1032 LOC → ~250)

**New:**
- Per-context `ports.go` files defining the interfaces each bounded context needs (`internal/aggregation/ports.go`, `internal/workflow/ports.go`, `internal/agent/ports.go`). Not a shared `pkg/contracts/` — ports belong to their consumer (DDD/hexagonal idiom).
- `WorkflowRun` CRD (does not exist today; required for K8s-native execution observability)
- `KubernetesEntityProvider` impl using controller-runtime (lives in `internal/aggregation/kubernetes/`)
- `FilesystemEntityProvider` impl using fsnotify + YAML reader (lives in `internal/aggregation/filesystem/`; logic extracted from existing reconciler)
- Constructor DI in `cmd/muster/serve.go`
- Import-boundary CI rules

### `muster broker`

Same binary, different command. Wires only:
- `internal/oauth/` (entire — token store, state store, token exchange, RP endpoints)
- `internal/server/oauth_http.go` (RP endpoints)
- gRPC server implementing `CredentialBroker`
- Session admin types from `pkg/apis`

`muster broker` doesn't initialize aggregator code at all. Different `cmd/muster/broker.go` entrypoint constructs only its own dependency graph.

### `muster agent` (laptop, narrowed)

Same binary, ships separately via brew/scoop. Most of `internal/agent/` (~10.7k LOC today) is stdio MCP shim plumbing. With streamable-http MCP, IDEs talk to the gateway directly — the shim becomes transitional. The narrowed agent keeps:
- `cmd/auth*`, `cmd/list`, `cmd/get`, `cmd/call`, `cmd/check`, `cmd/context`, `cmd/create`, `cmd/events`, `cmd/test` — CLI commands
- Local OIDC client (PKCE callback listener on `localhost:NNNN`, XDG token store)
- REPL (debugging convenience)
- Stdio MCP shim — kept while legacy IDEs (Cursor / Claude Desktop today) still default to stdio; flagged for removal once streamable-http is universal

`internal/cli/` (output formatters, table builders) remains. Net post-narrowing: ~3-4k LOC of essentials, down from 10.7k. The deletions are stdio-protocol plumbing tied to MCP-server-mode.

## Things deleted entirely (no destination, no migration)

| Path | Reason | LOC |
|---|---|---|
| `internal/serviceclass/` (entire) | No remaining use case | ~1,700 |
| ServiceClass BDD scenarios (31 files) | Same | scenario count |
| `pkg/apis/muster/v1alpha1/serviceclass_types.go` | CRD removed | ~200 |
| `internal/orchestrator/` (directory) | MCPServer lifecycle moves to `internal/aggregation/`; ServiceClass instance management deleted with ServiceClass; state machine collapses into `MCPServer.status` (K8s) or aggregator in-memory state (filesystem); dead parts (dependency graph, YAML persistence flag, static-service framing) deleted entirely | ~2,000 (split: ~700 relocates, ~1,300 deletes) |
| `internal/reconciler/` (entire) | Replaced by EntityProvider implementations (controller-runtime + fsnotify) | ~3,900 |
| `internal/events/` | controller-runtime EventRecorder in K8s mode; logs in filesystem mode | ~1,100 |
| `internal/services/{aggregator,mcpserver}/` | Wrappers around parent packages | ~700 |
| `internal/teleport/` | Cross-cluster app replaces | ~1,500 |
| `internal/api/` service-locator glue (types stay) | Constructor DI per binary mode | ~1,500 |
| `pkg/strings/` | Inline | ~30 |
| `cmd/selfupdate.go`, `cmd/standalone.go` | Vestigial | ~450 |
| `internal/aggregator/denylist.go` | Gateway CEL | 102 |
| Most of `internal/metatools/` (keep `filter_tools`) | Gateway speaks MCP natively | ~1,300 |
| Auth-related code in aggregator (metrics, rate limiter, session store, sso tracker) | Moves to `muster broker` | ~750 |
| Most of `auth_resource.go`, `auth_tools.go` bodies | Rewires to broker | ~700 |

**Filesystem mode (`internal/client/filesystem_client.go`) is NOT deleted** — refactored under the EntityProvider interface.

## Cross-component contracts

### `muster serve` ↔ `muster broker` — gRPC over mTLS
Already a network call (separate pod) per ADR-012. Hot path: `GetCredential` per backend call, cached by aggregator until `expires_at - 30s`. Stateful flows: `BeginOAuthFlow` / `CompleteOAuthFlow`. Auth events: `WatchAuthEvents` stream drives `auth://status` resource and ADR-006 sessionToolFilter.

### Aggregator ↔ workflow execution — in-process today
`WorkflowExecutor` interface in `pkg/contracts/`. Aggregator imports the interface; workflow execution provides the impl, registered at startup. When workflow extracts to its own pod (future trigger), the impl becomes a gRPC client; aggregator code unchanged.

### Aggregator ↔ EntityProvider — interface, two impls
`EntityProvider` interface. K8s impl uses controller-runtime; filesystem impl uses fsnotify. Aggregator picks the impl based on the `--filesystem-mode` flag at startup.

### Gateway ↔ `muster serve` — mTLS
Gateway forwards user JWT in `Authorization: Bearer`. **Aggregator validates JWT itself against Dex JWKS** — never trusts gateway-injected identity headers. Gateway-added metadata signed via httpsig (RFC 9421); aggregator verifies via gateway JWKS. Replay protection via httpsig nonce + 60s sliding window.

### Session ID correspondence
Aggregator's per-session capability store is keyed by `sessionID`. With agentgateway in front, the session ID muster sees must map deterministically to the gateway's view. Options:
1. Aggregator derives sessionID from validated JWT (`sub` + `client_id`)
2. Gateway forwards a stable session header signed via httpsig
3. Push capability store upstream into agentgateway eventually

Decision needed before broker rollout — capability store correctness depends on it.

### `muster agent` ↔ rest
Production: `muster agent` → gateway URL (talks MCP, validates session via Dex token).
Local lab: `muster agent --local-dev` → `muster serve --filesystem-mode --no-auth` directly.

## Per-customer Helm chart shape

| Resource | Today | Target |
|---|---|---|
| Deployments | 1 (muster) | 2 (muster-server, muster-broker) — same image, different command |
| ServiceAccount + RBAC | 1 set | per process (server needs CRD R/W; broker needs Secret read for IdP creds) |
| HTTPRoute | → muster | → agentgateway; gateway routes to muster-server |
| Cilium NetworkPolicy | agent → muster | gateway ↔ server ↔ broker; explicit deny otherwise |
| oauth-secret | mounted by muster | mounted by broker only |
| HPA | aggregator-style | server (sessions); broker (RPS) |
| PDB | one | per process |

agentgateway has its own chart (CNCF). Dex chart unchanged.

## Future-split design — when triggers hit

The architecture today is one binary, two server-side pods (`muster serve` + `muster broker`), one laptop binary (`muster agent`). Splits happen only when measurable triggers hit:

| Trigger | Action | Result |
|---|---|---|
| Workflow execution memory/duration measurably starves the server pod | Split workflow execution into a worker pool: `muster workflow-executor` becomes a horizontally-scaled pod that the WorkflowRun controller in `muster serve` dispatches to via `WorkflowExecutor` (now gRPC instead of in-process) | 3 server-side processes |
| Reconcile lag during deploys exceeds SLA (controller-runtime in `muster serve` blocked by aggregator pod restart) | Split CRD reconcile out: `muster operator` becomes its own pod running just controller-runtime + EntityProvider; `muster serve` keeps MCP routing and connections but no longer watches CRDs | 3 server-side processes |
| Compliance audit forces broker secret isolation across image | broker extracts to its own repo + image | broker leaves muster repo |
| agentgateway upstream absorbs ADR-006 / capability store / tool dedup / OAuth dispatch for non-mcp-oauth | aggregator's MCP-routing role dissolves; what remains is MCPServer/Workflow CRD reconciliation + workflow execution = same `muster serve` doing fewer things | Long-term endpoint |

### Long-term endpoint

When agentgateway reaches feature parity (no specific timeline — driven by upstream cooperation and contribution velocity), the data plane moves there. `muster serve` shrinks to just MCPServer/Workflow CRD reconciliation + workflow execution. Same component, smaller scope.

```
agentgateway (external)              data plane: MCP routing, sessions, dedup, ADR-006 filter
    │
    ├──→ muster serve                shrunk: CRD reconcile + workflow execution
    │         │                       (the aggregator dissolved into agentgateway upstream)
    │         │
    │         └──→ agentgateway      tool calls during workflow steps
    │
    └──→ muster broker               unchanged

muster agent                         unchanged
```

The aggregator is a **transient role**. It exists in `muster serve` today; over time, agentgateway absorbs it. There's never a moment where "aggregator" and "muster-controller" are separate components.

## Open architectural questions

1. **WorkflowRun CRD shape**: spec/status/field-ownership for the new CRD. Tekton-pattern (controller creates, runtime updates step status) vs. simpler.
2. **Session ID correspondence between gateway and `muster serve`**: derive-from-JWT vs. signed-gateway-header vs. push-capability-store-upstream.
3. **Gateway upstream contribution strategy**: which features push upstream into agentgateway, in what order? Solo relationship for review/merging?
4. **Filesystem mode coverage**: K8s mode supports CRD status writes; filesystem mode doesn't. Are there any features that REQUIRE status persistence (failure recovery, retry tracking) that need a filesystem fallback (e.g., a tiny SQLite or BoltDB store) — or is "no persistence in lab" acceptable?
5. **Broker scope**: Dex-specific token exchange + OAuth flows for muster only, or generic ext_authz interface for other GS services? Affects whether broker eventually extracts to its own repo.
6. **mcp-go library coupling**: `mark3labs/mcp-go v0.52.0` underpins ADR-006 via `mcpserver.WithToolFilter`. Library evolution affects feature movability when pushing upstream into agentgateway.
7. **Customer migration**: existing MCPServer CRDs with `auth.type: teleport` (going away). Deprecation window + conversion path required.
8. **Trigger metrics**: what exact monitoring signals trigger the future binary splits? Define SLOs upfront so the split is data-driven.

## Phased migration sequence

Diagrams (Miro) precede this; sequencing follows.

### Phase 0 — foundation
- OTel SDK; audit logs become OTel spans
- Sprig sandboxing in `internal/template/engine.go`
- Field ownership comments in `pkg/apis/muster/v1alpha1/*_types.go`
- Define `pkg/contracts/` interfaces (`WorkflowExecutor`, `ToolCaller`, `CredentialBroker`, `EntityProvider`)
- Set up import-boundary CI rules

### Phase 1 — gateway adoption (ADR-012 Step 1)
Adopt agentgateway. mTLS + JWT verify + httpsig metadata. Delete `denylist.go`, slim metatools to `filter_tools`, remove agent-facing OAuth RS code.

### Phase 2 — broker extraction (ADR-012 Step 2)
Define gRPC service. Aggregator becomes broker client. `muster serve` no longer wires `internal/oauth/` or `internal/server/oauth_http.go`; `muster broker` does. Same binary, different process.

### Phase 3 — drop dead features
ServiceClass (entire), Teleport, `cmd/selfupdate.go`, `cmd/standalone.go`, `pkg/strings/`, `internal/services/{aggregator,mcpserver}/` wrappers. Fix `internal/events` → `internal/cli` smell.

`localCommand` MCPServer type **stays** — it pairs with filesystem mode for lab/local-dev (muster spawns MCP server subprocesses so the lab user doesn't have to hand-manage HTTP processes). Not used in production K8s deployments where backends are pods reached via ingress.

### Phase 4 — controller-runtime adoption + EntityProvider refactor
Adopt `sigs.k8s.io/controller-runtime` `manager.Manager` properly inside `muster serve`. Add `WorkflowRun` CRD. Delete `internal/reconciler/` (logic preserved in two EntityProvider impls), `internal/orchestrator/`, `internal/events/`. Constructor DI replaces `internal/api/` service locator.

`KubernetesEntityProvider` uses controller-runtime; `FilesystemEntityProvider` uses fsnotify + YAML reader. Aggregator picks at startup based on `--filesystem-mode` flag. Filesystem mode preserved.

### Phase 5 — `muster agent` split
Same monorepo, separate binary. `goreleaser` produces three artifacts (`muster`, `muster-broker` could be aliases for the same binary, `muster agent`). Helm chart deploys `muster serve` + `muster broker` from `muster:vX`; brew/scoop ship `muster agent`. BDD harness moves to `muster agent test`.

### Phase 6 — per-customer rollout
Per-customer stack: agentgateway + muster-server + muster-broker + Dex. A2A peering with GS-allow CEL.

### Phase 7+ — long-tail upstreaming
Push aggregator features upstream into agentgateway: ADR-006 session filter, capability store, tool dedup, OAuth dispatch for non-mcp-oauth backends. As each lands, the corresponding aggregator code is deleted. `muster serve` shrinks gracefully.

## Estimated impact

| Phase | LOC out | Risk |
|---|---|---|
| 0 — foundation | 0 (additive) | low |
| 1 — gateway adoption | ~3,000 | medium (security boundary) |
| 2 — broker extraction (still in muster repo, separate process) | ~7,000 (relocates within repo to broker mode) | high (coordinated three-component release) |
| 3 — drop dead features | ~9,000 (hard deletes) | medium (breaking changes for ServiceClass users) |
| 4 — controller-runtime + WorkflowRun CRD + EntityProvider | ~7,000 net delete; refactor of ~3-4k more | medium (envtest needed; filesystem-mode parity to preserve) |
| 5 — `muster agent` split | ~15,000 (moves to separate binary, same repo) | low |
| 7+ — aggregator upstream long tail | ~5,000-10,000 (eventually shrinks aggregator) | medium (depends on upstream cooperation) |

End state Phase 5: ~25-30k LOC of muster server-side, deployed as 2 pods per customer (server + broker). End state Phase 7+: server shrinks further as aggregator dissolves.

## Verification

End-to-end after Phase 5:

1. `helm install` deploys two pods (muster-server, muster-broker) from one image, alongside agentgateway + Dex
2. `muster agent auth login` (laptop) drives OAuth flow against Dex via gateway URL
3. `muster agent call core_workflow_list` returns workflows; `muster agent call x_kubernetes_list_pods` proxies through gateway → muster-server → backend
4. `kubectl apply -f mcpserver.yaml` reconciled by `muster serve` (controller-runtime); status fields populated; `kubectl get events` shows reconcile
5. `kubectl apply -f workflow.yaml` validated against published catalog
6. `kubectl apply -f workflowrun.yaml` triggers execution; `kubectl get workflowrun -w` shows step progress
7. `muster serve` restart: live sessions reconnect (broker holds tokens; CRD status holds runtime state)
8. `muster broker` outage: new sessions fail closed; cached creds keep existing sessions working until expiry
9. Cross-tenant call (GS-Central → Customer A): A2A peering visible in customer's audit; broker token-exchange visible in GS-Central audit
10. **Lab/local-dev verification**: `muster serve --filesystem-mode --config-path ./.muster/ --no-auth` starts without K8s; `muster agent --local-dev` connects directly; tool calls work; YAML hot-reload works on file edit
11. `make test` (unit) and BDD scenarios green (both K8s and filesystem mode)
12. **Future-split readiness**: replacing `WorkflowExecutor` impl with a gRPC client (without changing aggregator code) compiles and passes integration tests — proves the interface boundary holds

## Test harness migration

The current 12k LOC custom BDD harness (`internal/testing/`) plus 1,356-LOC mock OAuth server reinvent ecosystem-standard tools. With controller-runtime adoption (Phase 4), the standard kubebuilder pattern is:

- **Unit tests:** stdlib `testing` + `testify/require` (already in repo)
- **Controller tests:** ginkgo + gomega + envtest — kubebuilder convention; controllers tested against a real in-process `kube-apiserver` + `etcd`, no kubelet
- **Integration / end-to-end:** ginkgo against a `kind` cluster
- **Mocks:** stdlib `httptest`, `oauth2-proxy/mockoidc` for OAuth, `mark3labs/mcp-go` server helpers for MCP backend mocks

The 167 YAML scenarios migrate to ginkgo specs over time. Most of the custom harness disappears. This matches GS's existing test idiom across kubebuilder operators (klaus, mcp-prometheus). Not a Phase 4 blocker — can be a long-tail migration.

## Out of scope

- Renaming muster
- Replacing the workflow engine with Tekton/Argo
- BDD harness migration in one shot (incremental scenario-by-scenario migration is fine)
- Removing filesystem mode (decided: stays)
- Splitting workflow / controller into their own binaries before trigger conditions are met
- Creating a separate "muster-controller" component (the aggregator IS the controller for muster CRDs)
