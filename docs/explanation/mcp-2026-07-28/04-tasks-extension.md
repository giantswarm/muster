# Tasks extension (MCP 2026-07-28)

In `2025-11-25`, **Tasks** shipped as an *experimental* core utility
([Tasks spec — 2025-11-25](https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/tasks))
that let a receiver respond to a request with a durable poll handle
rather than a final result. Production use surfaced enough redesign
that the right home for it is no longer the core specification but a
first-class **extension**, negotiated through the SEP-2133 framework
described in
[02-extensions-first-class.md](02-extensions-first-class.md). The
release-candidate announcement makes the move explicit in its "Tasks
graduates to an extension" subsection:

> The Tasks extension reshapes the lifecycle around the stateless
> model: a server can answer `tools/call` with a task handle, and the
> client drives it with `tasks/get`, `tasks/update`, and
> `tasks/cancel`. Task creation is server-directed: the client
> advertises the extension and the server decides when a call should
> run as a task. `tasks/list` is removed because it can't be scoped
> safely without sessions.
> ([announcement, "Tasks graduates to an extension"](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/))

The whole redesign is carried by
[SEP-2663 — Tasks Extension (#2663)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2663),
merged on the Extensions Track with the `final` label and an
`Implementations Recommended` posture for SDKs.

For muster the relevant fact is that **the workflow engine already
behaves like a Tasks server**: every `action_<workflow-name>` call
goes through
[internal/workflow/api_adapter.go](../../../internal/workflow/api_adapter.go)
and
[internal/workflow/execution_tracker.go](../../../internal/workflow/execution_tracker.go),
which mint a UUID-shaped `execution_id`, persist progress through
[internal/workflow/execution_storage.go](../../../internal/workflow/execution_storage.go),
and expose `workflow_execution_list` / `workflow_execution_get` for
out-of-band lookup. SEP-2663 is the wire-level shape muster's
homegrown execution lifecycle should map onto when it becomes the
upstream-of-record for a Tasks-capable host.

## 1. What the spec says

### 1.1 Why Tasks moved from core to an extension

[SEP-2663](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2663)
opens with three independent reasons to redesign the
`2025-11-25` Tasks utility ([the version being migrated from](https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/tasks)):

- **The handshake is fragile.** `2025-11-25` Tasks combine
  method-level capabilities (`tasks.requests.tools.call`), tool-level
  declarations (`execution.taskSupport: "required" | "optional" |
  "forbidden"`), **and** an opt-in `task` parameter on each request.
  A client that wants to attach `task` MUST first warm up its state
  with a `tools/list` to learn each tool's `execution.taskSupport`
  value, because attaching `task` to a `forbidden` tool is a protocol
  violation. SEP-2663 calls this "confusing, implicit, and easy to
  get wrong".
- **`tasks/result` is a blocking trap.** The old flow required the
  client to call `tasks/result` to give the server an SSE channel for
  mid-flight elicitations and then **blocks** until the operation
  completes. SEP-2260 (server-initiated requests must be tied to a
  client request — see
  [01-stateless-protocol.md](01-stateless-protocol.md) §1.2) makes
  that side-channel illegal, and many production deployments cannot
  hold a persistent connection open per long-running call anyway.
- **`tasks/list` cannot be scoped safely.** Without sessions (removed
  by SEP-2567, see
  [01-stateless-protocol.md](01-stateless-protocol.md) §1.1) and
  without a normative authorization-context model, exposing a
  cross-task listing endpoint risks leaking task IDs between callers.

A structural problem closes the case: SEP-1686's client-hosted tasks
(for elicitation and sampling) are no longer expressible at all under
SEP-2260, because every server-to-client polling request would be
unsolicited.

The conclusion is to redesign the feature *and* move it out of core
into an extension, so it can incubate independently of the
specification's release cadence and graduate back into core once it
has stabilised. SEP-2663 frames this as
[the SEP-2133 Extensions framework](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133)
being used for exactly the purpose it was designed for — see
[02-extensions-first-class.md](02-extensions-first-class.md) for
that context.

### 1.2 The extension on the wire

The SEP-2663 PR diff publishes a new
[extensions/tasks/overview page](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2663)
and a `docs/seps/2663-tasks-extension.mdx` reference document. Both
agree on the wire shape; what follows is summarised directly from the
SEP body (`gh pr view 2663 --repo modelcontextprotocol/modelcontextprotocol`).

**Extension identifier.** The extension is identified as
`io.modelcontextprotocol/tasks`. (The
[docs/extensions/tasks/overview.mdx](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2663)
page emphasises that the extension's living source-of-truth is the
[experimental-ext-tasks repository](https://github.com/modelcontextprotocol/experimental-ext-tasks)
referenced from the `ext-*` repository pattern in
[SEP-2133](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133).)

**Capability negotiation.** Per SEP-2575 (see
[01-stateless-protocol.md](01-stateless-protocol.md)), client
capabilities travel in `_meta` on every request, not in a one-shot
`initialize`. A Tasks-aware client declares support per request:

```jsonc
{
  "params": {
    "_meta": {
      "io.modelcontextprotocol/clientCapabilities": {
        "extensions": {
          "io.modelcontextprotocol/tasks": {}
        }
      }
    }
  }
}
```

A Tasks-aware server declares support in its `server/discover`
response:

```jsonc
{
  "result": {
    "capabilities": {
      "extensions": {
        "io.modelcontextprotocol/tasks": {}
      }
    }
  }
}
```

An empty object indicates "supported, no settings"; SEP-2663 reserves
the right to define settings later. **A server MUST NOT return a
`CreateTaskResult` to a client that did not include the extension
capability on its request**, regardless of any earlier declarations.
If a server cannot service the request without creating a task, it
returns `-32003 Missing Required Client Capability` with the missing
extension echoed in `data.requiredCapabilities.extensions`.

**Supported methods.** Only `tools/call` is task-augmentable in this
revision; the SEP designs the discriminator so that other request
types can join later: clients MUST be prepared to receive either a
standard result or a `CreateTaskResult` for *any* future
task-augmented request.

**Polymorphic results.** Task creation is signalled by the
`resultType` discriminator introduced by
[SEP-2322 (Multi Round-Trip Requests)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2322)
(also referenced from
[01-stateless-protocol.md](01-stateless-protocol.md) §1.2). SEP-2663
adds the value `"task"`:

```typescript
type ResultType = "complete" | "input_required" | "task";
```

A `CreateTaskResult` is the union of the standard `Result` shape and
a `Task`:

```typescript
type CreateTaskResult = Result & Task;
```

so a `tools/call` can return *either* a `CallToolResult` (the
familiar synchronous shape, `resultType: "complete"`) *or* a
`CreateTaskResult` (`resultType: "task"`). Servers MUST NOT use
`resultType: "task"` on any other result shape.

**The `Task` shape.** A `Task` carries operational metadata:

```typescript
interface Task {
  taskId: string;
  status: "working" | "input_required" | "completed" | "cancelled" | "failed";
  statusMessage?: string;
  createdAt: string;       // ISO 8601
  lastUpdatedAt: string;   // ISO 8601
  ttlMs: number | null;    // null = unlimited
  pollIntervalMs?: number;
}
```

Five terminal-or-non-terminal statuses replace the looser
`2025-11-25` set. `completed`, `failed`, and `cancelled` are
terminal; once entered, the state does not change.

`tasks/get` and `notifications/tasks` return derived shapes
(`WorkingTask`, `InputRequiredTask`, `CompletedTask`, `FailedTask`,
`CancelledTask`), which inline the status-specific payload — for
example `CompletedTask` adds a `result: JSONObject` field, and
`FailedTask` adds an `error: JSONObject`.

**Durable creation.** SEP-2663 introduces a normative requirement
that did not exist in the experimental flow:

> A server MUST NOT return `CreateTaskResult` until the task is
> durably created — that is, until a `tasks/get` for the returned
> `taskId` would resolve. In eventually-consistent environments, the
> server MUST wait for consistency before responding.

That is, the create response is the strong-consistency point;
`tasks/update` and `tasks/cancel` may be eventually consistent, but
the existence of the task by the time the create-response is sent
must be guaranteed.

**Polling — `tasks/get`.** Pure read; idempotent; clients SHOULD
respect `pollIntervalMs`; servers MAY rate-limit faster pollers;
clients SHOULD persist `taskId`s to durable storage so polling
survives a crash. The result is a `DetailedTask` with status-specific
fields inlined.

**Mid-flight input — `tasks/update`.** When the task moves to
`input_required`, `tasks/get` includes an `inputRequests` map (shape
defined by [SEP-2322](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2322)).
The client fulfils them by sending one or more `tasks/update`s
carrying `inputResponses`. Keys in `inputRequests` are unique over a
task's lifetime; servers MUST NOT reuse a key after it has been
answered, and MUST ignore `inputResponses` for unknown or
already-satisfied keys. `tasks/update` returns an empty ack
(`UpdateTaskResult = Result`).

This is the single most important change for muster's mental model
(see §3): the elicitation-during-task path is **never** a
server-initiated push. It is a *server-recorded request inside the
task object* that the client *pulls* and then *responds to via a
write RPC*. Combined with SEP-2260, this means a stateless server
never has to "wake up" a sleeping client.

**Cancellation — `tasks/cancel`.** Ack-only; cooperative; the server
is not obligated to honour the cancel and the task may still finish.
The `notifications/cancelled` notification MUST NOT be used for tasks
— `tasks/cancel` is the only path.

**Notifications — `notifications/tasks`.** Servers MAY push the full
`DetailedTask` over notifications instead of (or in addition to)
serving polls. Clients subscribe via
`subscriptions/listen` (see
[SEP-2575](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2575)
and [01-stateless-protocol.md](01-stateless-protocol.md) §1.1)
with a list of `taskIds` they care about:

```typescript
interface SubscriptionsListenRequest extends Request {
  method: "subscriptions/listen";
  params: {
    notifications: {
      taskIds?: string[];
    };
  };
}
```

The server returns the accepted `taskIds` in its
`notifications/subscriptions/acknowledged` notification. A client that
asks for task notifications without having declared the
`io.modelcontextprotocol/tasks` extension capability gets `-32003
Missing Required Client Capability`. `notifications/progress` and
`notifications/message` are explicitly **not supported** on the
subscription stream for a task — Tasks own their own status channel.

**Streamable HTTP routing.** Per SEP-2243 (see
[01-stateless-protocol.md](01-stateless-protocol.md) §1.3), every
Streamable HTTP request carries `Mcp-Method` and `Mcp-Name` headers.
SEP-2663 mandates that `tasks/get`, `tasks/update`, and `tasks/cancel`
set `Mcp-Name` to the `taskId`. That is the routing key intermediaries
use to pin task traffic to the server instance holding the task's
state — exactly the kind of "explicit handle on every request" pattern
the stateless rework calls for.

**Errors.** SEP-2663 distinguishes two error mechanisms:

- *Protocol errors* — JSON-RPC errors at the transport level. An
  unknown / expired `taskId` is `-32602 Invalid Params` (NOT the
  legacy `-32002`; see SEP-2164 in
  [07-json-schema-2020-12.md](07-json-schema-2020-12.md) for the
  more general error-code re-alignment).
- *Task execution errors* — the task itself reaches `failed` with an
  inlined `error` field and SHOULD include a `statusMessage`. A
  `tools/call` that completed at the protocol level but returned
  `isError: true` reaches `completed`, **not** `failed`. The
  `failed` status is strictly for JSON-RPC-level execution errors.

**Reservations.** SEP-2663 reserves the `tasks/` method prefix, the
`notifications/tasks/` prefix, the `resultType` value `"task"`, and
the label `io.modelcontextprotocol/tasks` to the extension.

### 1.3 What is *not* in the extension

Compared with `2025-11-25` Tasks, SEP-2663 deliberately drops:

- **`tasks/list`** — no safe scoping rule exists in the
  sessionless protocol.
- **`tasks/result`** — collapsed into `tasks/get` + `tasks/update`;
  the read/write split avoids the non-idempotent retry trap.
- **`tasks.requests.*` and `tasks.cancel` / `tasks.list` capability
  flags** — replaced by the single `io.modelcontextprotocol/tasks`
  extension capability.
- **The per-request `task` opt-in parameter on `tools/call`** —
  servers receiving it MUST ignore it.
- **Tool-level `execution.taskSupport`** — server decides
  per-request, not per-tool.
- **Client-hosted tasks for elicitation / sampling** — SEP-2260
  forbids the unsolicited server-to-client polling that would
  require, and sampling is in any case deprecated (SEP-2577, see
  [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md)).

The PR's "Backward Compatibility" section is explicit: the experimental
and extension surfaces are **not wire-compatible**. A client speaking
the experimental `tasks/result` against a server that has migrated to
the extension under `2026-06-30` (the version SEP-2663 lands in) MUST
receive `-32601 Method Not Found`. Bridging is an SDK-level shim
problem, not something the protocol negotiates.

### 1.4 Where the worked example fits

The SEP's "Example Message Flow" walks through `hello_world` — a
`tools/call` that needs an elicitation for the user's name and
eventually returns `Hello, <name>!`. The interesting structural moves
in that example are:

- The client attaches `io.modelcontextprotocol/clientCapabilities`
  with `extensions.io.modelcontextprotocol/tasks: {}` to the initial
  `tools/call`.
- The server returns `resultType: "task"` immediately with
  `status: "working"`.
- The client polls `tasks/get`. While `working`, the response is just
  the metadata.
- When the server needs input, the next `tasks/get` returns
  `status: "input_required"` with an `inputRequests.name` entry
  whose `method` is `elicitation/create` and whose `params` are the
  ordinary elicitation params. (The SEP notes that surfacing
  elicitations via `inputRequests` keeps them subject to the same
  trust model as direct `elicitation/create` requests.)
- The client sends `tasks/update` with
  `inputResponses.name = { action: "accept", content: { input: "Luca" } }`.
  The server acks; the task moves back to `working`; the client keeps
  polling; eventually `status: "completed"` arrives with the
  `CallToolResult` inlined into `result`.

This flow is what muster's workflow engine will, in concept, need to
speak when an inbound MCP client wants long-running execution
semantics out of an `action_<workflow>` call (§3.1).

## 2. Linked SEPs and PRs

- [SEP-2663 — Tasks Extension (#2663)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2663)
  — the primary PR; merged, status `Final`, Extensions Track. Author
  Luca Chang on behalf of the Agents Working Group.
- [2025-11-25 experimental Tasks spec](https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/tasks)
  — the design being migrated *from*; the source of the
  `tasks.requests.*` capability tree, `tasks/result`, and tool-level
  `execution.taskSupport`. SEP-2663 removes this from core.
- [experimental-ext-tasks repository](https://github.com/modelcontextprotocol/experimental-ext-tasks)
  — the `ext-*` repo (per
  [SEP-2133](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133))
  that will host the living extension specification post-merge.
- [SEP-2133 — Extensions framework (#2133)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133)
  — defines what an MCP extension *is*; SEP-2663 is one of two
  inaugural Extensions Track SEPs (the other is
  [SEP-1865 MCP Apps](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/1865),
  covered in [03-mcp-apps.md](03-mcp-apps.md)).
- [SEP-2575 — Make MCP Stateless (#2575)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2575)
  — removes the `initialize` handshake and provides per-request
  `_meta`; the carrier for the Tasks capability advertisement.
- [SEP-2567 — Sessionless MCP via Explicit State Handles (#2567)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2567)
  — removes the protocol-level session; the reason `tasks/list`
  could not be salvaged.
- [SEP-2260 — Server requests associated with client requests (#2260)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2260)
  — forbids unsolicited server-to-client requests; the reason
  `tasks/result` cannot host mid-flight elicitation any more.
- [SEP-2322 — Multi Round-Trip Requests (#2322)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2322)
  — defines the `resultType` discriminator that Tasks adds
  `"task"` to, and the `inputRequests` / `inputResponses` shape that
  `tasks/get` / `tasks/update` reuse.
- [SEP-2243 — HTTP header standardisation (#2243)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2243)
  — `Mcp-Method` / `Mcp-Name` headers; `Mcp-Name = taskId` is the
  per-task routing key.
- [Announcement, section "Tasks graduates to an extension"](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
  — the user-facing framing.
- Cross-section context in this folder:
  [01-stateless-protocol.md](01-stateless-protocol.md),
  [02-extensions-first-class.md](02-extensions-first-class.md),
  [07-json-schema-2020-12.md](07-json-schema-2020-12.md) (for the
  `-32002` → `-32602` error-code re-alignment),
  [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md)
  (sampling deprecation context).

## 3. Muster impact

Muster sits in the middle of two Tasks-relevant conversations:

- **Inbound (muster as MCP server).** Hosts that connect to muster
  (the agent's MCP-server mode for AI assistants, Cursor, Claude
  Code, custom hosts) will start advertising
  `io.modelcontextprotocol/tasks` and expect to discover task
  semantics on long-running tools — and muster's workflow execution
  tools (`workflow_<workflow-name>` to inbound clients, named
  `action_<workflow-name>` internally per
  [internal/aggregator/tool_factory.go](../../../internal/aggregator/tool_factory.go)
  lines 178–191) are the obvious candidates.
- **Outbound (muster as MCP client of upstreams).** Upstream MCP
  servers that advertise the extension will start returning
  `CreateTaskResult` for some `tools/call`s muster forwards.
  Muster's aggregator MUST be able to either (a) speak the lifecycle
  transparently (forwarding `tasks/get` etc. to the upstream and
  surfacing the final result to the inbound caller) or (b) declare
  to the upstream that it cannot accept tasks (omit the extension
  capability) and live with whatever fallback the upstream offers.

### 3.1 Workflow engine as a Tasks server

Muster's workflow engine already implements *almost exactly* the
Tasks lifecycle, just under a different naming.

**Lifecycle parallels.** The status enum in
[internal/api/types.go](../../../internal/api/types.go) lines 321–332
defines:

- `WorkflowExecutionInProgress = "inprogress"` — direct counterpart to
  SEP-2663's `working` status.
- `WorkflowExecutionCompleted = "completed"` — identical name and
  meaning to SEP-2663's `completed`.
- `WorkflowExecutionFailed = "failed"` — identical name and meaning
  to SEP-2663's `failed`.

The two states SEP-2663 adds that muster's workflows currently lack
on the protocol surface are `input_required` (mid-flight elicitation
inside a workflow) and `cancelled` (cooperative cancel signalled from
the inbound caller).

**Durable handle and storage.** SEP-2663's "durable creation" rule
maps onto
[internal/workflow/execution_tracker.go](../../../internal/workflow/execution_tracker.go)'s
`TrackExecution` (lines 54–109): every workflow execution gets a
UUIDv4 `ExecutionID`, an initial record is stored via
`ExecutionStorage.Store(ctx, execution)` *before* the workflow body
runs (lines 75–79), and the final record is stored once the body
returns. The storage is filesystem-backed by
[internal/workflow/execution_storage.go](../../../internal/workflow/execution_storage.go)'s
`ExecutionStorageImpl`. The "by the time the response goes out, a
`tasks/get` for this id MUST resolve" rule is — modulo renaming
`ExecutionID` to `taskId` — already what the tracker does, because
the initial `Store` happens synchronously before the executor returns.

**Lookup endpoints.** Muster already exposes two of the three Tasks
protocol methods, just under different names:

- `core_workflow_execution_get(execution_id, include_steps?, step_id?)`
  ([internal/workflow/api_adapter.go](../../../internal/workflow/api_adapter.go)
  lines 1005–1028 for the tool metadata, 1408–1496 for the handler) is
  the muster-internal equivalent of `tasks/get`. The shape differs
  (muster returns full step-by-step execution detail, SEP-2663 wants
  a `DetailedTask` with status-specific inlined fields), but the
  intent — "look up the state of a long-running execution by handle"
  — is identical.
- `core_workflow_execution_list` is the muster-internal equivalent of
  the now-removed `tasks/list`. It works only because muster's API
  layer is *not* the wire MCP protocol — SEP-2663 removed
  `tasks/list` precisely because the protocol cannot scope it; muster
  still can, because it has a richer authorization model.
- There is **no** equivalent of `tasks/update` (mid-flight input)
  today, because workflow elicitation isn't wired through into
  workflows. Steps in
  [internal/workflow/operations.go](../../../internal/workflow/operations.go)
  run tool calls; there is no `input_required` state on the workflow
  execution itself.
- There is also **no** equivalent of `tasks/cancel`. Once a workflow
  is `inprogress` the only way it terminates is by running to
  completion or by the orchestrator dying.

**Action-tool routing.** The user-facing surface for executing a
workflow is `workflow_<workflow-name>` on the aggregator, mapped from
the internal `action_<workflow-name>` by
[internal/aggregator/tool_factory.go](../../../internal/aggregator/tool_factory.go)
lines 178–191 (`mapWorkflowToolName`) and routed back in
`callCoreToolDirectly`. Inside the workflow adapter,
[internal/workflow/api_adapter.go](../../../internal/workflow/api_adapter.go)
lines 1066–1069 dispatch `action_<workflow-name>` to
`ExecuteWorkflow(ctx, workflowName, args)`. `ExecuteWorkflow`
([internal/workflow/api_adapter.go](../../../internal/workflow/api_adapter.go)
lines 69–191) wraps `executor.ExecuteWorkflow` in `TrackExecution` and
returns a `CallToolResult` whose JSON content has been enhanced with
`execution_id` (line 121 / `enhanceResultWithExecutionID` at lines
785–810). **This is the natural injection point** for the Tasks
extension: today `ExecuteWorkflow` blocks until the workflow
finishes; SEP-2663 wants it to be able to *immediately* return a
`CreateTaskResult` whose `taskId` is the existing `execution_id` and
whose status is the existing `WorkflowExecutionInProgress`.

**What changes structurally.** SEP-2663 forces a per-call decision
between two response shapes for `action_<workflow>` invocations:

- *Synchronous, short workflows.* Return the standard `CallToolResult`
  ("`resultType: complete`"). Today's behavior. Indistinguishable
  from a tool that doesn't implement Tasks at all. SEP-2663 explicitly
  allows this — "A server that returns the standard `CallToolResult`
  shape — i.e., never elects to create a task — remains fully
  spec-compliant under this extension."
- *Long-running workflows.* Return a `CreateTaskResult` with the
  existing `execution_id` aliased as `taskId`, `status: working`, a
  `pollIntervalMs` (suggested), and a `ttlMs` (the workflow's max
  runtime, or `null` for unbounded). Subsequent `tasks/get` calls
  return a `DetailedTask` derived from the
  `api.WorkflowExecution` record; on success the `result` field
  inlines the final `CallToolResult`.

The server-directed character of SEP-2663 maps cleanly onto a
**per-workflow opt-in** in the muster CRD: a new `taskBehavior` field
on `WorkflowSpec` (or equivalent on
[musterv1alpha1.WorkflowSpec](../../../pkg/apis/muster/v1alpha1))
can declare whether `action_<this-workflow>` returns synchronously or
as a task — without exposing that choice on the client side, which is
exactly the server-directed model SEP-2663 wants.

### 3.2 Aggregator forwarding for upstream Tasks servers

When an upstream MCP server advertises
`io.modelcontextprotocol/tasks` and returns a `CreateTaskResult` for
a `tools/call` muster forwarded, the aggregator must decide between
three behaviors:

- **Transparent passthrough.** Forward the `CreateTaskResult` to the
  inbound caller verbatim. The inbound caller drives `tasks/get` /
  `tasks/update` / `tasks/cancel` against muster, and muster
  re-routes those to the same upstream that minted the `taskId`. This
  requires per-upstream taskId routing — `Mcp-Name = taskId` is the
  hint the spec wants intermediaries to use (§1.2 above), but muster
  still has to know *which* upstream a given `taskId` belongs to.
  Two reasonable options: (a) prefix or tag the `taskId` with an
  upstream identifier as it crosses muster, and unwrap it on the way
  back, or (b) keep a small in-memory `taskId → upstream` map in the
  aggregator. Option (b) re-introduces server-side state and runs
  counter to the SEP-2575 direction; option (a) preserves the
  "all routing info on the request" property and is probably the
  right call.
- **Collapse to synchronous.** Muster polls `tasks/get` against the
  upstream itself and returns the final `CallToolResult` to the
  inbound caller, hiding the task lifecycle. Required when the
  inbound caller did *not* declare the
  `io.modelcontextprotocol/tasks` extension capability (per the
  SEP, the upstream MUST NOT have returned a `CreateTaskResult` in
  that case — but a misbehaving upstream might; muster has to be
  defensive). This is also the only viable mode for clients of
  muster's `core_*` meta-tools.
- **Reject with `-32003`.** When the upstream needs a task to make
  progress (it returned `-32003 Missing Required Client Capability`
  asking for `io.modelcontextprotocol/tasks`) but the inbound
  caller did not declare the extension, muster propagates the same
  `-32003` so the inbound caller can either declare the extension or
  give up. This is also the right answer when no upstream
  configuration permits muster to declare the extension on the
  inbound side.

Persisting the upstream's advertised `extensions` map is a
prerequisite for any of this, and is already an item in
[02-extensions-first-class.md](02-extensions-first-class.md) §3.2;
without it, the aggregator can't even tell whether an upstream is
Tasks-capable.

The outbound MCP clients in
[internal/mcpserver/client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go),
[internal/mcpserver/client_sse.go](../../../internal/mcpserver/client_sse.go),
[internal/mcpserver/client_stdio.go](../../../internal/mcpserver/client_stdio.go),
and
[internal/mcpserver/client_dynamic_auth.go](../../../internal/mcpserver/client_dynamic_auth.go)
will need new code paths to send `tasks/get` / `tasks/update` /
`tasks/cancel` (with `Mcp-Name = taskId` per §1.2). The current
[internal/mcpserver/client_interface.go](../../../internal/mcpserver/client_interface.go)
surface (`CallTool`, `ListTools`, `ListResources`, etc.) does not have
a Tasks-shaped method on it; SEP-2663 will likely require adding one
once mcp-go grows the types.

### 3.3 Notifications and `subscriptions/listen`

SEP-2663's `notifications/tasks` rides
[SEP-2575's](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2575)
`subscriptions/listen` channel. Muster's inbound transport currently
relies on long-lived SSE for change notifications via
[internal/aggregator/notification_subscriber.go](../../../internal/aggregator/notification_subscriber.go);
in `2026-07-28`, that path is replaced wholesale by
`subscriptions/listen` (see
[01-stateless-protocol.md](01-stateless-protocol.md) §1.1). When
that work happens, plumbing `taskIds` through alongside the existing
`toolsListChanged` / `resourcesListChanged` notification subscriptions
is the cheap way to also support
`notifications/tasks` — both inbound (muster pushes task updates from
its own workflow engine to subscribed clients) and outbound (muster
forwards an upstream's task notifications to the inbound caller that
asked for them).

Importantly, the SEP forbids `notifications/progress` and
`notifications/message` on a task's notification stream. That maps
onto muster having to *not* leak workflow step events
([internal/workflow/api_adapter.go](../../../internal/workflow/api_adapter.go)
lines 1890–1937, `GenerateStepEvent`) as MCP `notifications/progress`
for a Tasks-exposed workflow. Workflow step events stay in muster's
internal event stream (Kubernetes events, etc.); they do not become
Tasks-protocol progress notifications.

### 3.4 Migration from any `2025-11-25` experimental usage

Muster does **not** currently implement the experimental Tasks
utility — neither inbound nor outbound. A grep for
`tasks/list`, `tasks/cancel`, `tasks/result`, `taskId`, and
`execution.taskSupport` finds no hits outside the
`docs/explanation/mcp-2026-07-28/` folder itself. The only "tasks"
namespace in the codebase is the muster-internal
`workflow_execution_*` family (§3.1) which is unrelated to the MCP
Tasks utility and is not exposed under the `tasks/` method prefix.

That gives muster a clean slate: there is no experimental-to-extension
migration *inside the codebase*, but the migration matters for any
user-facing documentation that talks about long-running execution
semantics, and for any custom MCP servers users have already deployed
behind muster that *did* implement the experimental Tasks utility.
Those servers will, post-`2026-07-28`, return either nothing
task-shaped (most likely — the experimental capabilities will simply
go unadvertised against muster's outbound clients) or `-32601 Method
Not Found` if muster were to call `tasks/result` against them. Muster
never calls `tasks/result`, so the practical risk is nil.

### 3.5 Tooling impact on muster's BDD scenarios

Workflow scenarios in
[internal/testing/scenarios/](../../../internal/testing) currently
assert *synchronous* `CallToolResult` shapes from `workflow_<name>`
invocations. Any scenario that exercises a long-running workflow (or
that future scenarios add for Tasks-aware behaviour) will need to
choose:

- assert against the current synchronous shape and *not* declare the
  Tasks extension capability on the BDD client's `_meta` (the current
  baseline), or
- declare the extension and assert against `CreateTaskResult` plus a
  follow-up `tasks/get` flow.

SEP-2663 explicitly supports both modes; muster's test client just
has to make a choice per scenario. Once the conformance suite
([08-protocol-evolution.md](08-protocol-evolution.md), SEP-2484)
ships Task scenarios, those become the upstream reference muster
should align against.

## 4. Required changes / migration notes

Issues to file once the section docs in this folder all land. Items
are ordered so that earlier items unblock later ones.

1. **Persist upstream `extensions` map.** Prerequisite for Tasks
   forwarding and identical to
   [02-extensions-first-class.md](02-extensions-first-class.md) §4
   item 2. The aggregator's
   [Capabilities](../../../internal/aggregator/capability_store.go)
   (lines 11–27) and the per-server view in
   [internal/aggregator/registry.go](../../../internal/aggregator/registry.go)'s
   `refreshServerCapabilities` (line 1052) must retain the
   upstream's advertised `extensions` so the aggregator can detect
   the `io.modelcontextprotocol/tasks` claim at all.
2. **Add a `taskBehavior` field to the workflow CRD.** Extend
   `musterv1alpha1.WorkflowSpec` (referenced from
   [internal/workflow/api_adapter.go](../../../internal/workflow/api_adapter.go)
   lines 239–253) with an optional `taskBehavior` enum (`auto` /
   `always` / `never`) that controls whether the workflow's
   `action_<name>` tool returns synchronously or as a
   `CreateTaskResult`. Per-call decision lives on the server side,
   per SEP-2663's server-directed model.
3. **Rename internally where it doesn't hurt, alias where it does.**
   `api.WorkflowExecution.ExecutionID` is the muster-internal name
   that, on the inbound MCP-Tasks surface, should be exposed as
   `taskId`. The struct does not need renaming — `api.WorkflowExecution`
   ([internal/api/types.go](../../../internal/api/types.go) lines
   315 onward) stays as the muster-internal type; a new (thin) adapter
   from `api.WorkflowExecution` to `Task` / `DetailedTask` lives in
   either `internal/workflow/` or a new `internal/tasks/` package and
   is responsible for the `executionStatus → taskStatus` mapping
   (`inprogress → working`, `completed → completed`, `failed →
   failed`, plus new cases for `input_required` and `cancelled`).
4. **Wire an `io.modelcontextprotocol/tasks` advertisement on the
   inbound side.** Tied to
   [02-extensions-first-class.md](02-extensions-first-class.md) §4
   item 4. When the aggregator's inbound `extensions` map gains a
   non-empty value, advertise the Tasks extension only if (a) at
   least one workflow has `taskBehavior != never`, or (b) at least
   one upstream advertises the extension and muster is willing to
   forward it.
5. **Handle the inbound `_meta.io.modelcontextprotocol/clientCapabilities`.**
   Per SEP-2575 and SEP-2663, an inbound `action_<workflow>`
   invocation that wants Tasks semantics declares the extension in
   per-request `_meta` (see SEP-2575 plumbing item in
   [01-stateless-protocol.md](01-stateless-protocol.md) §4). The
   workflow adapter's `ExecuteWorkflow` must read that map before
   deciding whether it is allowed to return a `CreateTaskResult`.
6. **Implement `tasks/get`, `tasks/update`, `tasks/cancel` as
   meta-tools on muster's inbound surface.** Three new
   aggregator-level RPCs. Implementation is straightforward for
   `tasks/get` against muster's own workflow store (delegating to
   `executionTracker.GetExecution` in
   [internal/workflow/execution_tracker.go](../../../internal/workflow/execution_tracker.go),
   surfaced today as
   `workflow_execution_get` —
   [internal/workflow/api_adapter.go](../../../internal/workflow/api_adapter.go)
   lines 1408–1496). `tasks/cancel` requires a new cancellation path
   on
   [internal/workflow/executor.go](../../../internal/workflow/executor.go);
   `tasks/update` requires a workflow-level elicitation channel that
   doesn't yet exist (item 8 below).
7. **Implement `Mcp-Name = taskId` routing on muster's inbound
   transport.** Slots in next to the `Mcp-Method` / `Mcp-Name`
   handling tracked in
   [01-stateless-protocol.md](01-stateless-protocol.md) §4. For
   muster-served tasks, the routing target is muster itself (so the
   header is largely a no-op); for forwarded tasks, the header lets
   any L7 in front of muster pin task traffic to the muster instance
   holding the upstream connection.
8. **Add a workflow-level elicitation channel.** Today
   [internal/workflow/executor.go](../../../internal/workflow/executor.go)
   does not expose elicitation — a workflow step that needs
   mid-flight user input has no place to express that. SEP-2663
   `inputRequests` plus
   [SEP-2322](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2322)
   are the obvious target: a step can pause the workflow with an
   `inputRequest`, the executor parks the execution as
   `WorkflowExecutionInputRequired` (new state — also item 9), and
   the wire-level `tasks/get` surfaces the request to the caller.
   This is the single biggest piece of new code.
9. **Extend `WorkflowExecutionStatus`.** Add `WorkflowExecutionInputRequired`
   and `WorkflowExecutionCancelled` to
   [internal/api/types.go](../../../internal/api/types.go) lines
   321–332. The mapping in item 3 then has a 1:1 correspondence
   between muster execution status and SEP-2663 task status. Storage
   migrations are needed for any persisted executions written under
   the previous schema (see
   [internal/workflow/execution_storage.go](../../../internal/workflow/execution_storage.go)).
10. **Forward Tasks calls to upstream MCP servers.** Add Tasks
    request shaping to the outbound clients
    ([client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go),
    [client_sse.go](../../../internal/mcpserver/client_sse.go),
    [client_stdio.go](../../../internal/mcpserver/client_stdio.go),
    [client_dynamic_auth.go](../../../internal/mcpserver/client_dynamic_auth.go),
    [client_interface.go](../../../internal/mcpserver/client_interface.go))
    and an upstream-taskId-to-server map in the aggregator. The
    "tag the `taskId` with an upstream prefix" route (§3.2) is the
    one that stays compatible with the stateless rework.
11. **Drop any reliance on `tasks/list` in design conversations.**
    No code change because muster has no `tasks/list` usage; pure
    documentation hygiene to ensure future authors do not assume
    cross-task enumeration is available on the Tasks surface.
    `core_workflow_execution_list` remains usable for muster's own
    admin UI because it lives outside the MCP wire.
12. **Conformance scenarios.** Add Tasks scenarios to
    [internal/testing/scenarios/](../../../internal/testing) — at
    minimum: synchronous-baseline (no extension declared), opt-in
    happy path (`working → completed`), `input_required` round-trip
    via `tasks/update`, `tasks/cancel` cooperative cancel, and
    `-32003` error when an inbound client demands a task that
    muster cannot fulfil. Targets the conformance posture in
    [08-protocol-evolution.md](08-protocol-evolution.md).
13. **Documentation updates.** The workflow execution chapter of
    `docs/` should explain the alias `execution_id == taskId` and
    the synchronous-vs-task choice. The CHANGELOG entry for the
    Tasks integration should explicitly call out that muster never
    implemented the `2025-11-25` experimental Tasks, so there is no
    breaking change for existing muster users.

## 5. Open questions

- **`taskId` namespace vs `execution_id` namespace.** Muster's
  `execution_id` is a v4 UUID minted by
  [uuid.New().String()](../../../internal/workflow/execution_tracker.go)
  (line 56). SEP-2663 requires `taskId`s to be "generated with
  sufficient entropy that a third party cannot enumerate or guess
  them"; v4 UUIDs comfortably satisfy that. Still: should muster
  *reuse* the same value for both, or mint a separate `taskId` and
  keep a translation table? Reuse is simpler; a separate value gives
  freedom to vary the format (for example, "v1-`<upstream>`-`<uuid>`"
  for forwarded tasks).
- **TTL semantics.** SEP-2663's `ttlMs` is the duration *from
  creation* after which the server may purge a task. Muster's
  workflow executions are persisted indefinitely on the filesystem
  via
  [internal/workflow/execution_storage.go](../../../internal/workflow/execution_storage.go)
  — there is no purge policy. Picking a `ttlMs` value means picking a
  purge policy that didn't exist before. `null` (unlimited) is the
  safe default; the question is whether muster admins want a TTL knob
  to avoid unbounded execution-record growth.
- **`pollIntervalMs` defaults.** Workflows can run anywhere from
  hundreds of milliseconds to hours. A single static
  `pollIntervalMs` is wrong for both ends of that spectrum. Options:
  derive it from historical execution duration of the workflow, let
  the workflow author declare it on the CRD, or use an
  exponential-backoff hint encoded as a sequence of values across
  `tasks/get` responses (SEP-2663 allows `pollIntervalMs` to change
  over a task's lifetime).
- **Workflow elicitation model.** Item 8 in §4 is conceptually large
  — workflows today are batch pipelines, not interactive sessions.
  Are there real muster workflows that *need* mid-flight elicitation
  (deployment confirmations, approval gates)? If not, `input_required`
  is theoretical and can be deferred; if yes, the design needs to
  decide where in the YAML/CRD step shape an elicitation belongs.
- **Cancellation contract for workflows.** SEP-2663 is explicit that
  `tasks/cancel` is cooperative. Muster's workflow executor today
  doesn't have a cooperative-cancel signal threaded through tool
  calls. The minimal implementation is a `context.Cancel` on the
  per-execution context; the question is what that does to
  in-flight tool calls that don't honour the cancellation signal —
  do we wait for them, abandon them, or kill the underlying upstream
  connection?
- **Tasks for resources / prompts.** SEP-2663 currently augments only
  `tools/call`, but the SEP explicitly says implementations
  "SHOULD be designed to accommodate additional request types in
  future revisions". Does muster's design treat its own `tasks/get`
  as method-agnostic, or hardcode the `tools/call → workflow`
  mapping? Method-agnostic is more work now but cheaper later.
- **Forwarded-task `taskId` namespace collisions.** If muster
  forwards a `taskId` minted by an upstream verbatim, two upstreams
  could in principle mint the same string. Probability is
  vanishingly small with UUIDs but non-zero with arbitrary
  upstreams. A wrap like `up-<server-id>-<upstream-taskId>` makes
  the collision impossible; the question is whether that breaks any
  upstream that assumes its own `Mcp-Name` will come back unchanged
  on `tasks/get`.
- **mcp-go SDK readiness.** As with the rest of the 2026-07-28
  rework, the Tasks extension types are not yet in
  [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go). Until
  they are, muster's options are (a) wait, (b) carry a thin
  parallel-types package in muster's own tree, or (c) propose the
  types upstream and gate this work on the upstream landing. Same
  decision as for SEP-2133 / SEP-2575; see
  [02-extensions-first-class.md](02-extensions-first-class.md) §3.4.

## 6. References

- [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/) — announcement, section "Tasks graduates to an extension"
- [SEP-2663 — Tasks Extension (#2663)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2663) — primary PR; metadata, body, and full diff via `gh pr view 2663` / `gh pr diff 2663`
- [experimental-ext-tasks repository](https://github.com/modelcontextprotocol/experimental-ext-tasks) — `ext-*` repo that hosts the living extension spec referenced from SEP-2663's `docs/extensions/tasks/overview.mdx`
- [Tasks utility, 2025-11-25 specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/tasks) — the experimental design SEP-2663 supersedes (capabilities tree, `tasks/list`, `tasks/result`, tool-level `execution.taskSupport`)
- [SEP-2133 — Extensions framework (#2133)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2133) — the framework SEP-2663 uses for negotiation and lifecycle; covered in [02-extensions-first-class.md](02-extensions-first-class.md)
- [SEP-2575 — Make MCP Stateless (#2575)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2575) — `_meta` carries `clientCapabilities.extensions`; `subscriptions/listen` carries `notifications/tasks`
- [SEP-2567 — Sessionless MCP via Explicit State Handles (#2567)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2567) — removed sessions; the reason `tasks/list` cannot be scoped
- [SEP-2260 — Server requests associated with client requests (#2260)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2260) — no unsolicited server-to-client requests; `tasks/update` replaces the old `tasks/result`-side-channel for elicitation
- [SEP-2322 — Multi Round-Trip Requests (#2322)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2322) — `resultType` discriminator + `inputRequests` / `inputResponses` shape
- [SEP-2243 — HTTP header standardisation (#2243)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2243) — `Mcp-Method` / `Mcp-Name`; `Mcp-Name = taskId` routing rule
- [SEP-2577 — Roots, Sampling, Logging deprecations (#2577)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2577) — context for why client-hosted tasks (sampling, elicitation) drop out of the Tasks design
- Cross-section context: [01-stateless-protocol.md](01-stateless-protocol.md), [02-extensions-first-class.md](02-extensions-first-class.md), [03-mcp-apps.md](03-mcp-apps.md), [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md), [07-json-schema-2020-12.md](07-json-schema-2020-12.md), [08-protocol-evolution.md](08-protocol-evolution.md)
- Muster code paths cited in this document:
  [internal/workflow/api_adapter.go](../../../internal/workflow/api_adapter.go),
  [internal/workflow/execution_tracker.go](../../../internal/workflow/execution_tracker.go),
  [internal/workflow/execution_storage.go](../../../internal/workflow/execution_storage.go),
  [internal/workflow/executor.go](../../../internal/workflow/executor.go),
  [internal/workflow/operations.go](../../../internal/workflow/operations.go),
  [internal/api/types.go](../../../internal/api/types.go),
  [internal/api/workflow.go](../../../internal/api/workflow.go),
  [internal/aggregator/tool_factory.go](../../../internal/aggregator/tool_factory.go),
  [internal/aggregator/server.go](../../../internal/aggregator/server.go),
  [internal/aggregator/registry.go](../../../internal/aggregator/registry.go),
  [internal/aggregator/capability_store.go](../../../internal/aggregator/capability_store.go),
  [internal/aggregator/notification_subscriber.go](../../../internal/aggregator/notification_subscriber.go),
  [internal/mcpserver/client_streamable_http.go](../../../internal/mcpserver/client_streamable_http.go),
  [internal/mcpserver/client_sse.go](../../../internal/mcpserver/client_sse.go),
  [internal/mcpserver/client_stdio.go](../../../internal/mcpserver/client_stdio.go),
  [internal/mcpserver/client_dynamic_auth.go](../../../internal/mcpserver/client_dynamic_auth.go),
  [internal/mcpserver/client_interface.go](../../../internal/mcpserver/client_interface.go),
  [internal/testing/scenarios/](../../../internal/testing).
