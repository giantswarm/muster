# Test Scenario Authoring Guide

## Overview

This guide provides comprehensive documentation for authoring YAML-based test scenarios for the muster test framework. Test scenarios define the complete lifecycle of a test, including setup, execution, validation, and cleanup.

**Key Architecture Points:**
- Each test scenario runs against its own isolated muster serve instance
- Mock MCP servers are essential for testing muster's core MCP server management and tool aggregation features
- Mock servers enable testing muster concepts (workflows, capabilities, services) that depend on MCP server tools
- Mock servers are managed as separate processes by muster serve, so we can test the complete functionality of muster serve through the scenarios
- Tools follow specific naming conventions based on their source (core vs. mock)

## YAML Schema Reference

### Complete Scenario Structure

```yaml
# Required fields
name: "scenario-unique-name"           # Unique identifier for the scenario
category: "behavioral"                 # "behavioral" or "integration"
concept: "workflow"                    # Core muster concept being tested
description: "Human-readable description of what this scenario tests"

# Optional metadata
tags: ["basic", "crud", "smoke"]       # Tags for filtering and organization
timeout: "5m"                          # Global scenario timeout (default: 30m)

# Pre-configuration for the isolated muster instance
# This generates the necessary configs and definitions before starting muster serve
pre_configuration:
  mcp_servers:                         # Mock MCP servers (uses muster's standard MCP server management)
    - name: "mock-server-name"
      config:
        tools:
          - name: "tool-name"          # Simple name in mock config
            description: "Tool description"
            annotations:                 # Optional MCP tool annotations the mock declares
              read_only_hint: true       # (read_only_hint, destructive_hint, idempotent_hint, open_world_hint)
            input_schema:
              type: "object"
              properties:
                param1:
                  type: "string"
            responses:
              - response:
                  status: "success"

  workflows:                           # Workflows to pre-create
    - name: "test-workflow"
      config:
        # Workflow definition

  storage:                             # Store backend the instance runs on (default: memory)
    type: "valkey"                     # "memory" | "valkey" (in-process stand-in per instance)
    start_delay: "3s"                  # Optional: the store answers this long after muster serve started

  mode: "kubernetes"                   # Definition source (default: filesystem): "kubernetes" applies the
                                       # definitions as CRs to the run's envtest API server and runs muster
                                       # serve in Kubernetes mode; skipped without KUBEBUILDER_ASSETS
  apiserver:                           # Kubernetes mode only
    reachable_after: "5s"              # Optional: the API server refuses connections this long after muster serve started

# Test execution steps
steps:
  - id: "step-unique-name"             # Unique step identifier
    description: "What this step does" # Human-readable step description
    tool: "core_workflow_create"       # MCP tool name to invoke
    args:                              # Tool args (renamed from 'args')
      yaml: |                          # YAML content (for tools that accept YAML)
        name: test-resource
        description: "Test resource"
    expected:                          # Validation rules
      success: true                    # Expected success/failure
      contains: ["created", "success"] # Response must contain these strings
      json_path:                       # JSON path assertions
        status: "created"
        available: true
    timeout: "1m"                      # Step-specific timeout

# Required cleanup steps (always run, even on failure)
cleanup:
  - id: "cleanup-resources"            # Changed from 'name' to 'id'
    description: "Remove test resources"
    tool: "core_workflow_delete"
    args:                              # Changed from 'args' to 'args'
      name: "test-resource"
    expected:
      success: true
    timeout: "30s"

# Optional assertions on muster serve's own stdout/stderr, captured at debug
# level for the whole scenario and evaluated once after steps and cleanup ran
instance_logs:
  not_contains: ["eyJ"]                # e.g. no JWT ever reached the logs
  occurrences:                         # exact line counts for once-only actions
    "Suspending MCPServer service my-server (spec.suspended=true)": 1
```

### Key Schema Changes

#### Updated Field Names
- Step identifiers use `id` instead of `name` (aligns with workflow step format)
- Tool args use `args` instead of `args` (aligns with workflow step format)
- Cleanup steps also use `id` and `args` for consistency

#### Tool Naming Conventions

**Core muster Tools**: Use standard names
```yaml
steps:
  - id: "create-workflow"
    tool: "core_workflow_create"       # Standard core tool
```

**Mock MCP Server Tools**: Use `x_<server-name>_<tool-name>` pattern
```yaml
# Pre-configuration defines mock server:
pre_configuration:
  mcp_servers:
    - name: "kubernetes-mock"
      config:
        tools:
          - name: "get_pods"           # Simple name in config

# Steps reference with prefix:
steps:
  - id: "test-k8s-pods"
    tool: "x_kubernetes-mock_get_pods" # Prefixed name in usage
```

**Workflow Tools**: Use `workflow_<workflow-name>` pattern
```yaml
# Pre-configuration defines workflow:
pre_configuration:
  workflows:
    - name: "backup-data"
      config:
        # workflow definition

# Steps reference workflows:
steps:
  - id: "run-backup"
    tool: "workflow_backup-data"     # workflow_ prefix (NOT action_)
```

### Schema Validation Rules

#### Required Fields

- **name**: Must be unique across all scenarios, use kebab-case
- **category**: Must be either "behavioral" or "integration"
- **concept**: Must be one of the supported concepts (workflow, mcpserver, service)
- **description**: Human-readable description of the test purpose
- **steps**: At least one test step must be defined

#### Optional Fields

- **tags**: Array of strings for categorization and filtering
- **timeout**: Global timeout in Go duration format (e.g., "5m", "30s", "1h")
- **pre_configuration**: Setup for the isolated muster instance
- **cleanup**: Teardown steps run after test completion
- **instance_logs**: Assertions on the muster serve instance's captured output; see [Instance log assertions](#instance-log-assertions)

#### Step Schema

Each step must define:
- **id**: Unique identifier within the scenario
- **tool**: Valid MCP tool name (core, mock, or workflow)
- **expected**: At least one validation rule (success, contains, json_path, etc.)

## Authoring Best Practices

### 1. Naming Conventions

#### Scenario Names
Use descriptive, kebab-case names:

```yaml
# ✅ Good examples
name: "workflow-basic-crud-operations"
name: "workflow-arg-templating-validation"
name: "mcpserver-connection-recovery-handling"

# ❌ Bad examples
name: "test1"
name: "Workflow_Test"
```

#### Step Names
Use action-oriented names with `id` field:

```yaml
# ✅ Good examples
  - id: "create-test-workflow"
  - id: "verify-workflow-availability"
  - id: "execute-workflow"

# ❌ Bad examples
  - id: "step1"
  - id: "test-stuff"
```

### 2. Tool Reference Patterns

#### Core Tools
```yaml
steps:
  - id: "list-workflows"
    tool: "core_workflow_list"        # Direct core tool usage
```

#### Mock Server Tools
```yaml
# Define in pre_configuration:
pre_configuration:
  mcp_servers:
    - name: "storage-mock"
      config:
        tools:
          - name: "create_volume"      # Simple name in mock config

# Reference in steps:
steps:
  - id: "test-storage"
    tool: "x_storage-mock_create_volume"  # x_<server>_<tool> pattern
```

#### Workflow Tools
```yaml
# Define in pre_configuration:
pre_configuration:
  workflows:
    - name: "backup-data"
      config:
        # workflow definition

# Reference in steps:
steps:
  - id: "run-backup"
    tool: "workflow_backup-data"     # workflow_<name> pattern (NOT action_)
```

### 3. Arg Patterns

#### YAML Args
For tools that accept YAML configurations:

```yaml
args:
  yaml: |
    name: test-workflow
    description: "Test Workflow for scenario"
    args:
      replicas:
        type: integer
        default: 1
      image:
        type: string
        required: true
    steps:
      - id: "list-services"
        tool: "core_service_list"
```


#### Key-Value Args
For simple arg passing:

```yaml
args:
  name: "test-workflow"
  timeout: "5m"
  parallel: true
```

### 4. Validation Patterns

#### Success Validation
Basic validation - ensure the operation succeeded:

```yaml
expected:
  success: true
```

#### Content Validation
Verify response contains expected content:

```yaml
expected:
  success: true
  contains: ["created successfully", "test-workflow"]
```

#### JSON Path Validation
For structured responses:

```yaml
expected:
  success: true
  json_path:
    status: "running"
    available: true
    metadata.name: "test-workflow"
```

#### Error Validation
For testing error conditions:

```yaml
expected:
  success: false
  error_contains: ["not found", "resource does not exist"]
```

#### Absence Validation
Assert that something is *not* in the response:

```yaml
expected:
  success: true
  not_contains: ["x_server-beta_beta_tool"]
```

#### Supported expectation kinds

Every kind below means the same thing on every step, whether the step calls an
MCP tool or a `test_*` tool -- both go through one evaluator, and
`TestEveryExpectationKindIsEnforcedOnBothStepKinds` fails the build if a kind is
ever honoured on one path and dropped on the other.

| Kind | Checks |
|---|---|
| `success` | the step succeeded (or failed, when `false`), including a `success: false` payload |
| `contains` | the response text contains every listed string |
| `not_contains` | the response text contains none of the listed strings |
| `error_contains` | the error text contains every listed string |
| `json_path` | the named paths resolve to the given values |
| `wait_for_state` | retry policy: re-invoke the tool until the above hold, or the timeout elapses |

`status_code` is **not** supported and is rejected at load time. Test tools that
perform HTTP put the status in their result payload, so assert it with
`json_path` instead:

```yaml
expected:
  success: true
  json_path:
    status_code: 200
```

#### There is no per-step `retry`

A step-level `retry:` block (`count`, `delay`, `backoff_multiplier`) used to be
accepted and validated, but the runner never read it -- a step declaring it got
exactly one attempt. It is now rejected at load time. `wait_for_state` covers the
same need and is the mechanism that works, so poll with it instead:

```yaml
# Wrong: rejected at load time.
expected:
  success: true
  contains: ["obo-backend"]
retry:
  count: 5
  delay: "1s"

# Right: re-invokes the tool every second for up to 30s.
expected:
  success: true
  contains: ["obo-backend"]
  wait_for_state: "30s"
```

Set `wait_for_state` *or* a step `timeout:`, not both -- two deadlines on the
same step race, and the poll should own the bound.

#### Bounding a step's duration

A step-level `max_duration:` fails the step when its single invocation took
longer. It is the assertion for "this request is answered while background
work runs" -- a new session's first request returning while muster connects
the session's SSO servers, a listing that must not wait for a slow backend.
It is judged on the invocation the runner made, so it cannot be combined with
`expected.wait_for_state` (rejected at load time: polling would count towards
it). Leave a wide margin between the bound and the background work it proves
the step does not wait for -- a 2 s bound against a backend that takes 4 s to
connect -- so a loaded machine cannot turn the proof into a flake:

```yaml
- id: "connect-as-agent"
  tool: "test_reconnect_with_token"
  args: { token_ref: "obo-token" }
  max_duration: "2s"
  expected:
    success: true
```

#### Instance log assertions

Step expectations only see what a tool call returns. `instance_logs` is the one
expectation that sees the other side: the stdout and stderr of the `muster serve`
instance the scenario ran against, captured at debug level for the whole run and
checked once after the last step and cleanup step. It is scenario-level, not
per-step, and takes `contains` and/or `not_contains` lists and/or an
`occurrences` map (at least one is required; an empty block is rejected at
load time):

```yaml
instance_logs:
  contains: ["SSO: initSSOForSession called"]
  not_contains: ["eyJ"]   # the base64url prefix every JWT header shares
  occurrences:
    "Suspending MCPServer service my-server (spec.suspended=true)": 1
```

The typical use is proving a credential never reached the logs, so a
`not_contains` hit is reported by line number and the text of that line *up to*
the match -- never the match itself or what follows it. `occurrences` maps a
substring to the exact number of lines that must contain it: the assertion for
an action that has to happen once and only once, such as a lifecycle step the
reconciler must not repeat on its resync ticks (the harness runs instances with
a 2 s resync, so a step that waits 5 s sees at least two of them). Use
`contains` and `occurrences` sparingly: log lines are not an API, and a
scenario pinned to log wording breaks on harmless rewording.

#### Storage backend and process restart

Every store muster keeps outside the process -- the session auth store, the
capability store, the OAuth token, state and client-credential stores and the
OAuth server's own store -- follows `oauth.server.storage`. On an installation
that is Valkey, and it outlives the pod; by default a scenario's instance runs
on memory, where every store dies with the process. `pre_configuration.storage`
switches the instance to a Valkey stand-in:

```yaml
pre_configuration:
  storage:
    type: "valkey"          # one in-process miniredis per instance, on a harness port
    start_delay: "3s"       # optional: refuse connections until 3 s after muster serve started
```

The stand-in answers every command muster issues (hashes, sets, scans, expiry,
Lua scripts, pipelines) and the `CLIENT TRACKING` handshake of a valkey-go
client with client-side caching on. It is content the OAuth server needs to
mint sessions on, so a scenario with `use_as_muster_oauth_server` keeps its
bearers valid across a restart. `start_delay` models a Valkey pod that is not
scheduled yet: connects are refused until the delay passes, so a scenario can
assert which backend muster came up on (`muster_session_store_backend` via
`test_scrape_metrics`, and the serve log).

Three test tools drive the process and the store while a scenario runs:

| Tool | Args | Effect |
|------|------|--------|
| `test_restart_instance` | none | SIGTERMs `muster serve`, starts it again on the same configuration, ports and environment while the store and every mock server keep running, waits for readiness, and reconnects every client the scenario holds (the default one and every `test_create_user` one) with the bearer it held. Same bearer, same session: the steps after it act as the sessions that lived through a rollout, without a new sign-in. Result: `pid_before`, `pid_after`, `reconnected_users`. The instance's captured output spans both lives, so `instance_logs` sees the whole scenario. |
| `test_stop_valkey` | none | Takes the store off its port, data kept -- a Valkey pod being rescheduled while muster keeps running. Requires `storage.type: valkey`. |
| `test_start_valkey` | none | Brings the store back on its port with its data; also starts a `start_delay` store early. |

A restart scenario has three parts: build the state (sign in, list, call),
`test_restart_instance`, then assert the behaviour the new process owes the
old sessions -- a tool call that must still route, a grant that must still
be revocable, a sign-in that must still be known. What the new process must
*not* do (fall back to memory, re-run a one-shot action) is an `instance_logs`
assertion over both lives. Keep the memory default for everything else: the
stand-in is one more listener per instance, and only scenarios about
persistence or restarts learn anything from it.

#### Kubernetes mode: CRs, informers, reconciler and boot pass

An installation runs muster in Kubernetes mode: the MCPServer and Workflow
definitions are CRs read through informers, the reconciler and the
orchestrator's boot pass both drive service lifecycle, status goes to the CR's
status subresource, and the API server can be late or gone. By default a
scenario's instance runs in filesystem mode, where none of that exists.
`pre_configuration.mode: kubernetes` runs the scenario the way an installation
runs muster:

```yaml
pre_configuration:
  mode: "kubernetes"
  apiserver:
    reachable_after: "5s"   # optional: the API server refuses connections until 5 s after muster serve started
  mcp_servers:
    - name: "srv"
      config:
        type: "streamable-http"   # required: Kubernetes mode refuses stdio servers
        tools: [...]
```

The harness starts one envtest control plane per `muster test` run from the
binaries `KUBEBUILDER_ASSETS` points at (kube-apiserver, etcd), installs the
CRDs from `helm/muster-crds/files/crds`, and shares it between every
Kubernetes-mode scenario of the run. Each instance gets its own namespace,
its `mcp_servers` and `workflows` applied there as CRs (the same documents a
filesystem instance reads from its config directory; that directory holds no
definitions in Kubernetes mode, so a process that fell back to the filesystem
finds it empty), a kubeconfig, and `muster serve` with `kubernetes: true` and
`namespace: <its namespace>`. Between muster and the API server sits a TCP
proxy the harness owns; the kubeconfig names the proxy, so the API server can
be absent for one instance while the others keep theirs -- `reachable_after`
keeps the proxy closed for that long after the process started, the shape of
a kube-apiserver that restarted together with the muster pod.

Mock servers must be remote (`type: streamable-http` or `sse`): muster in
Kubernetes mode refuses stdio definitions, and the loader rejects such a
scenario. `core_mcpserver_update` writes with the caller's identity in
Kubernetes mode and fails for the harness's anonymous session; drive the
reconciler with `test_patch_cr` instead. `core_events` returns the Kubernetes
Events of the instance's namespace.

Where `KUBEBUILDER_ASSETS` is not set, every Kubernetes-mode scenario is
reported as **skipped** with that reason -- never as passed. `make test`
(the go-build CI job) runs the suite without the binaries; `make test-envtest`
(the test-envtest CI job) provisions them with setup-envtest and runs
`muster test --mode kubernetes`. Locally:

```bash
export KUBEBUILDER_ASSETS="$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.24 use -p path)"
PATH="$PWD:$PATH" ./muster test --mode kubernetes --parallel 8 --base-port 31000
```

Three test tools act on the CRs and the API server while a scenario runs:

| Tool | Args | Effect |
|------|------|--------|
| `test_patch_cr` | `name` (required), `patch` (object, required), `kind` (`MCPServer`, the default, or `Workflow`) | Applies a JSON merge patch to a CR of the instance's namespace, the way `kubectl patch --type merge` does: nested objects merge, `null` removes a field. `{spec: {suspended: true}}` suspends a server through a real CR update; `{metadata: {labels: {...}}}` relabels it. Returns the stored object (`metadata`, `labels`, `spec`, `status`). |
| `test_get_cr` | `name` (required), `kind` | Reads a CR as the API server stores it -- `spec`, `labels` and the `status` muster wrote (`status.state: Connected`, `lastAttempt`, ...) -- for `json_path` assertions on what reached the cluster. |
| `test_set_apiserver_reachable` | `reachable` (bool, required) | Closes (`false`) or opens (`true`) the instance's API server proxy: closed, muster's watches end and its next request is refused; open again, they resume. The control plane and the other instances are untouched. |

`test_set_mcpserver_labels` and `test_pin_mcpserver_authorization_server` work
in both modes: they update the CR in Kubernetes mode and the definition file in
filesystem mode.

A Kubernetes-mode scenario states what the reconciler and the boot pass owe
the CRs: which services exist after a boot (`core_service_status`), what the
CR's status reads (`test_get_cr`), how often a lifecycle action ran
(`instance_logs.occurrences`), and that the client that came up is the
Kubernetes one (`instance_logs.contains: "Initialized reconciliation manager
in kubernetes mode"`, `not_contains: "falling back to filesystem mode"`).
Keep the filesystem default for everything that does not concern definitions,
their source or the API server: a Kubernetes-mode instance costs a namespace
and a proxy on the shared control plane, and only scenarios about the CR
lifecycle learn anything from it.

#### Faults and time

The faults an installation meets are named steps, each a mechanism rather
than a bug, and every one of them acts on the harness's own infrastructure
while `muster serve` runs untouched:

| Fault | Step or setting | What muster sees |
|-------|-----------------|------------------|
| Backend gone | `test_stop_mock_server` / `test_start_mock_server` | Connections refused for a while, then a fresh process on the same port |
| Backend redeployed | `test_redeploy_mock_server: {server}` | The port never refuses; a fresh process that knows no session -- the next call with the old session id is answered 404 (a rolled pod behind the same Service) |
| Gateway in front of the backend failing | `test_set_mock_server_outage: {server, requests, status, pings}` | An HTTP status (504 by default) for the next `requests` connection attempts, then normal service |
| Backend rolled over between anonymous and OAuth | `test_set_mock_server_auth: {server, required: true\|false}` | The same process answering anonymously, or 401 with the RFC 9728 challenge and resource metadata. The mock needs a token validator (`oauth.mock_oauth_server_ref` or `oauth.trust_issuer_ref`); `oauth.required` is its state at start |
| Backend suspended in its definition | `mcp_servers[].config.suspended: true`, `test_patch_cr` / `core_mcpserver_update` | A server muster boots with but must keep down |
| Authorization server that forgot its clients | `test_forget_oauth_registrations` | Every RFC 7591 registration gone (an AS restart with an in-memory client store) |
| Valkey late at start / gone / back | `storage.start_delay`, `test_stop_valkey` / `test_start_valkey` | Connections refused until the store answers; the data kept (see "Storage backend and process restart") |
| API server late at start / gone / back | `apiserver.reachable_after`, `test_set_apiserver_reachable: false\|true` | Connections refused for this instance alone (see "Kubernetes mode") |
| muster restarted | `test_restart_instance` | A new process on the stores of the old one |

Elapsed time is the other dimension. Every instance has a **controllable
clock**: `muster serve` runs with `MUSTER_TEST_CLOCK` naming a Unix socket
the harness owns, and `test_advance_clock: {duration}` moves the process's
clock forward by that much -- together with the clock of every mock
authorization server of the instance, so token lifetimes on the
authorization server and muster's own timers agree. The timers on that
clock are the reconnect backoff of a remote MCPServer and the orchestrator's
retry and health-probe ticks (`internal/orchestrator`,
`internal/services/mcpserver`) and the age of the aggregator's core
catalogue (`internal/aggregator`): a tick that has become due fires at once
when the clock passes it, so nothing waits. Production binaries never see
the variable and keep the system time.

By default an instance runs on `intervals: short`: the harness shortens
those timers to seconds through environment knobs (a 1 s initial backoff
capped at 3 s, 1 s retry and health ticks, a 3 s catalogue age, a 2 s
reconciler resync), so a scenario sees them act within its `wait_for_state`
budgets without touching the clock. A scenario that asserts the production
schedule itself -- the 2 min backoff cap, the 5 min catalogue age -- selects
`intervals: production` and advances the clock instead:

```yaml
pre_configuration:
  intervals: "production"   # 30 s initial backoff, 2 min cap, 30 s ticks, 5 min catalogue age

steps:
  - id: "two-minutes-pass"
    tool: "test_advance_clock"
    args: { duration: "2m" }      # the retry tick fires now if a retry is due
    expected:
      success: true
```

The reconciler's resync is controller-runtime's and out of the clock's
reach, so it stays at 2 s on both schedules. Token expiry on the
authorization-server side is the mock's clock: `test_advance_oauth_clock`
moves it alone (with `use_mock_clock`, a clock that stands still between
advances), `test_advance_clock` moves it with muster's. The stores'
time-to-live checks -- the OAuth token stores, the capability store, Valkey's
own key expiry -- run on the system time and are not reached by the clock;
scenarios about them use short lifetimes.

A `test_restart_instance` after an advance starts the new process at the
system time and advances it by the same offset, so time never runs backwards
across a restart. Under `intervals: production` a first connect that fails
is retried 30 s later; keep the default for scenarios that do not assert the
production schedule.

### 5. Mock Server Configuration

#### Complete Mock Server Example
```yaml
pre_configuration:
  mcp_servers:
    - name: "database-mock"
      config:
        tools:
          - name: "create_table"
            description: "Create database table"
            input_schema:
              type: "object"
              properties:
                table_name:
                  type: "string"
                  required: true
                columns:
                  type: "array"
                  items:
                    type: "object"
            responses:
              - condition:
                  table_name: "users"
                response:
                  status: "created"
                  table_id: "tbl_users_123"
                  rows: 0
                delay: "2s"
              - error: "table '{{ .table_name }}' already exists"

# Usage in steps:
steps:
  - id: "create-users-table"
    tool: "x_database-mock_create_table"  # Note the x_ prefix
    args:
      table_name: "users"
      columns:
        - name: "id"
          type: "integer"
        - name: "email"
          type: "string"
    expected:
      success: true
      contains: ["created", "tbl_users_123"]
```

#### A backend that is slow to connect

`connect_delay:` on an OAuth-protected mock (`config.connect_delay`, a
duration) holds every `initialize` request for that long before the mock
answers it. It stands in for a session-authenticated backend whose connect is
still in flight while a scenario's steps run -- the way to prove what muster
does with a session's requests during its SSO fan-out:

```yaml
mcp_servers:
  - name: "slow-sso"
    config:
      type: "streamable-http"
      connect_delay: "4s"
      oauth:
        required: true
        trust_issuer_ref: "workload-idp"
        forward_token: true
      tools: [...]
```

### 6. Resource Management

#### Unique Resource Names
Always use unique names to avoid conflicts:

```yaml
args:
  yaml: |
    name: "test-workflow-{{ scenario.name }}"  # Use scenario name for uniqueness
```

#### Comprehensive Cleanup
Always clean up resources:

```yaml
cleanup:
  - id: "delete-test-workflow"
    tool: "core_workflow_delete"
    args:
      name: "test-workflow"
    expected:
      success: true
    continue_on_failure: true
```

## Common Anti-Patterns

### ❌ What to Avoid

#### 1. Incorrect Tool Naming
```yaml
# ❌ Bad: Old workflow naming
steps:
  - id: "run-workflow"
    tool: "action_my-workflow"  # Old naming, doesn't work

# ✅ Good: Current workflow naming
steps:
  - id: "run-workflow"
    tool: "workflow_my-workflow"  # Correct workflow_ prefix
```

#### 2. Missing Mock Tool Prefix
```yaml
# ❌ Bad: Direct mock tool name
steps:
  - id: "test-mock"
    tool: "create_resource"  # Missing x_ prefix

# ✅ Good: Proper mock tool reference
steps:
  - id: "test-mock"
    tool: "x_resource-mock_create_resource"  # Correct x_<server>_<tool> pattern
```

#### 3. Inconsistent Field Names
```yaml
# ❌ Bad: Mixing old and new field names
steps:
  - name: "test-step"         # Should be 'id'
    tool: "core_test"
    args:               # Should be 'args'
      test: true

# ✅ Good: Consistent field naming
steps:
  - id: "test-step"
    tool: "core_test"
    args:
      test: true
```

#### 4. Missing Cleanup
```yaml
# ❌ Bad: No cleanup section
steps:
  - id: "create-resource"
    # ... create something but never clean it up
```

```yaml
# ✅ Good: Always include cleanup
cleanup:
  - id: "delete-resource"
    # ... proper cleanup
```

## Per-Request Headers

A step can send HTTP headers on its own requests to the muster instance with `headers:`. They
apply to that step only — same client, same MCP session as the surrounding steps, including the
`wait_for_state` polls — which is how a scenario proves per-request evaluation of a header such
as `X-Muster-Toolset`: two steps on one session with different headers, then one without.

```yaml
steps:
  - id: scoped
    tool: test_call_meta_tool
    args: { tool: "list_tools" }
    headers:
      X-Muster-Toolset: "preset:read-only,workflow:incident-triage"
    expected:
      success: true
      not_contains: ["x_kubernetes_delete"]

  - id: unscoped-again
    tool: test_call_meta_tool
    args: { tool: "list_tools" }
    expected:
      success: true
      contains: ["x_kubernetes_delete"]
```

Regular tool steps (`tool: x_server_tool`) carry the headers too, since they go through
`call_tool` on the same client.

### MCPServer labels

`pre_configuration.mcp_servers[].labels` writes `metadata.labels` on the server's MCPServer
definition — the way a chart labels the resources it ships — and
`test_set_mcpserver_labels {server, labels}` replaces them while muster runs (an empty `labels`
removes them). Together they exercise label-based toolset presets, including "a server gaining
or losing the label changes the resolution without a restart":

```yaml
pre_configuration:
  mcp_servers:
    - name: "k8s"
      labels:
        agent-platform.giantswarm.io/tool-group: infrastructure
      config:
        tools: [ ... ]
steps:
  - id: relabel
    tool: test_set_mcpserver_labels
    args: { server: "k8s", labels: { tier: "gold" } }
    expected: { success: true }
```

## Multi-User Testing

The test framework supports multi-user scenarios to verify session isolation and per-user tool visibility. This is critical for testing OAuth-protected MCP servers where different users may have access to different tools.

### Multi-User Test Tools

| Tool | Description |
|------|-------------|
| `test_create_user` | Creates a new user session with a separate MCP connection. Required arg: `name` |
| `test_switch_user` | Switches to a different user session. Required arg: `name` |
| `test_list_tools_for_user` | Lists tools visible to a specific user. Optional arg: `name` (defaults to current) |
| `test_get_current_user` | Returns the current user name and list of available users |

### Using `as_user` Field

Steps can specify which user session to execute as:

```yaml
steps:
  # Create multiple users
  - id: create-user-a
    tool: test_create_user
    args:
      name: "user-a"
    expected:
      success: true

  - id: create-user-b
    tool: test_create_user
    args:
      name: "user-b"
    expected:
      success: true

  # Execute a step as a specific user
  - id: user-a-authenticates
    as_user: "user-a"
    tool: test_simulate_oauth_callback
    args:
      server: "protected-server"
    expected:
      success: true

  # Verify different users see different tools
  - id: verify-user-a-tools
    tool: test_list_tools_for_user
    args:
      name: "user-a"
    expected:
      success: true
      contains:
        - "x_protected-server_some_tool"

  - id: verify-user-b-no-tools
    tool: test_list_tools_for_user
    args:
      name: "user-b"
    expected:
      success: true
      not_contains:
        - "x_protected-server"
```

### Multi-User Scenario Examples

See these scenarios for complete examples:
- `session-multi-user-tool-isolation.yaml` - Verifies session isolation between users
- `session-multi-user-progressive-auth.yaml` - Tests progressive tool access accumulation

## Validation and Testing

### Schema Validation

Use the built-in validation to check scenario syntax:

```bash
# Validate a single scenario
./muster test --validate-scenario=path/to/scenario.yaml

# Validate all scenarios in a directory
./muster test --validate-scenarios=path/to/scenarios/
```

### Testing Your Scenarios

Test scenarios automatically run against isolated muster instances:

```bash
# Test a specific scenario (creates fresh muster instance automatically)
./muster test --scenario=my-scenario --verbose

# Test with debugging to see instance logs
./muster test --scenario=my-scenario --debug

# Test all scenarios in a concept category
./muster test --concept=workflow --verbose
```

**Benefits of Managed Instances:**
- Each scenario runs against a fresh muster instance
- Mock MCP servers are automatically configured and integrated
- No interference between test scenarios
- Automatic cleanup of instances and configurations
- Complete isolation ensures reliable test results

---

For complete examples implementing these patterns, see the [examples/](examples/) directory.
For framework documentation, see [README.md](README.md).
For package details, see `internal/testing/doc.go`.
