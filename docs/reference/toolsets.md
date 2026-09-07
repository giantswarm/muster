# Toolsets Reference

A **toolset** is the selector list an agent declares to say which of the gateway's tools it is
composed with. muster evaluates it **per request**, statelessly, as the third filter on the
caller's catalogue — after server-authentication visibility and workflow availability — so
`list_tools`, `filter_tools`, `describe_tool`, `list_core_tools`, the resource and prompt
accessors and `call_tool` all answer within it.

A toolset never widens access. The catalogue it filters is already the caller's own: a tool
from a server the session has not signed in to is not in the catalogue, so a selector naming
it matches nothing. The invoking human's identity and the backends' own authorization
remain the boundary; a toolset bounds what the *model* can discover and call.

## Declaring a toolset

### The `X-Muster-Toolset` request header

```http
X-Muster-Toolset: preset:read-only, workflow:incident-triage
```

A comma-separated list of inline selectors; whitespace around each selector is trimmed. The
header is read on **every** request. Nothing is bound to the MCP session: two requests on the
same session with different headers get different catalogues, and a request without the header
is unscoped — exactly today's behaviour. muster's session for a forwarded token is derived from
the token, so all the agents one person drives share one session; per-request evaluation is
what keeps agent A's toolset from applying to agent B.

The platform's Generic `agent` chart renders this header from its `toolset` value onto the
agent's muster tool entry (`headersFrom`), so an agent declares its toolset once, on its own
release.

### Inline selector grammar

| Selector | Selects |
|---|---|
| `preset:<name>` | A preset from `toolsetPresets` or a built-in (below). |
| `server:<name>` | Every tool of the MCPServer `<name>`. For a family, the family name selects the family surface; a member's name selects it too. |
| `workflow:<name>` | The workflow's execution tool (`workflow_<name>`). |
| `tool:<name>` | One tool by its exposed name (`x_<server>_<tool>`, `workflow_<name>`, `core_*`). |

Names are exact and case-sensitive (regex `^(preset|server|workflow|tool):[^\s,]+$`). At most
**32** selectors inline — larger selections are presets. Core tools (`core_*`) are selectable
only explicitly: `tool:core_workflow_list` inline, `pattern: core_*` in a preset. There is no
`core` pseudo-server.

Rejected inline, with an error naming the toolset and the selector:

| Input | Error |
|---|---|
| empty header / empty list | `toolset [] is empty; use "preset:none" for an agent without tools` |
| `toolset:<name>` | `… selector "toolset:x" is reserved for shared toolsets` |
| `label:<k>=<v>` | `… selector "label:…" is allowed inside presets only` |
| anything else | `… selector "…" is malformed; expected preset:<name>, server:<name>, workflow:<name> or tool:<name>` |
| more than 32 | `toolset [...] has 33 selectors, more than the 32 allowed inline; define a preset` |
| unknown preset | `toolset [preset:foo] names unknown preset "foo"; known presets: read-only, none, full, …` |

These are **error results on every meta-tool call** — including `get_resource` and
`get_prompt` — never a silent fall-back to the unscoped catalogue and never silent degradation
to the selectors that did parse. They are not sticky: the next request without the header is
unscoped again.

## Presets

Presets are muster configuration (`toolsetPresets:` in `config.yaml`; chart value
`muster.toolsetPresets`, rendered into the ConfigMap). They are GitOps-managed by
construction and evaluated against the live catalogue on each request, so a preset selecting
by server or by annotation stays correct when a server adds tools.

```yaml
toolsetPresets:
  infrastructure:
    description: Every infrastructure server, without deletes
    include:
      - server: mcp-kubernetes
      - server: mcp-prometheus
    exclude:
      - pattern: "*_delete"
  safe-ops:
    description: infrastructure narrowed to read-only, plus one workflow
    include:
      - preset: infrastructure
      - readOnly: true
      - workflow: incident-triage
    exclude:
      - workflow: rollout
```

Each preset has an optional `description` (shown by `filter_tools` with `include_presets`),
an `include` list and an optional `exclude` list. A preset resolves to the union of its
includes minus its excludes. Every rule sets **exactly one** key:

| Rule | Selects |
|---|---|
| `tool: <name>` | One tool by exposed name. |
| `pattern: <glob>` | Tools whose exposed name matches the glob (`core_*`, `x_mcp-kubernetes_*`; Go `path.Match` syntax). |
| `server: <name>` | Every tool of that server (family name or member name for families). |
| `workflow: <name>` | The workflow's execution tool. |
| `readOnly: true` | Every tool annotated `readOnlyHint: true`, including workflows carrying the derived hint (below). |
| `preset: <name>` | Composition: everything another preset selects. `include` only. |
| `label: <key>=<value>` / `label: <key>` | Tools of every MCPServer carrying that label (value match or presence). Presets only; see #1168. |

### Built-in presets

Three presets are built in and **cannot be redefined** — configuration that tries fails muster's
startup naming the preset:

| Preset | Resolves to |
|---|---|
| `read-only` | Every tool its server annotates `readOnlyHint: true`, plus every workflow whose step tools are all read-only. Core tools carry no annotations today and are therefore not included. |
| `none` | Nothing. A `preset:none` header hides and refuses every tool; the Generic chart omits the muster tool entry altogether for an agent whose toolset is exactly `["preset:none"]`. |
| `full` | The whole catalogue, core tools included (`pattern: "*"`). |

The platform charts ship `infrastructure` and `agent-platform` as values (by the
`agent-platform.giantswarm.io/tool-group` label); installations add their own the same way.

### Validation at startup

`muster serve` refuses to start when `toolsetPresets` redefines a built-in preset, when a rule
sets no key or more than one, when a `pattern` does not compile, when `readOnly` is `false`,
when `exclude` composes a preset, when a composition names an unknown preset, or when
compositions cycle. Every error names the preset and the rule position.

### Workflow read-only derivation

A workflow is read-only when every tool its steps reference — conditions, `forEach` and
`parallel` sub-steps and `onFailure` handlers included, nested workflows followed — resolves in
the caller's catalogue to a tool annotated read-only. A step calling a core tool (no
annotation), an unknown tool, or a cycle makes the workflow not read-only. The derived hint
fills the workflow tool's `readOnlyHint` annotation slot, so `describe_tool` shows it and
`preset:read-only` includes the pure-query workflows.

## What the meta-tools do with a toolset

- `list_tools`, `filter_tools`, `describe_tool`, `list_core_tools` read the filtered catalogue.
  `describe_tool` of a tool the session can see but the toolset excludes answers
  `tool "<name>" is outside the toolset [<selectors>]`; an unknown name is still `Tool not found`.
- `call_tool` — including workflow execution (`workflow_<name>`) — of a name outside the
  toolset is refused with `tool "<name>" is outside the toolset [<selectors>]` and logged once
  at info level with the tool, the toolset and the session. The tools a workflow's steps call
  internally are the workflow author's composition and are not re-checked.
- Resources and prompts follow the servers: a server is inside the toolset when at least one
  of its tools is selected. `list_resources`, `filter_resources`, `describe_resource`,
  `list_prompts`, `filter_prompts`, `describe_prompt` hide the others; `get_resource` and
  `get_prompt` refuse them (`resource "<uri>" is outside the toolset […]`).
- `list_tools`' `servers_requiring_auth` is not narrowed: it tells the caller which sign-in
  would make more of the toolset resolve.
- `tools/list` — the meta-tools themselves — is unchanged.

### `filter_tools` and toolsets

`filter_tools` accepts `toolset` (an array of inline selectors) and `include_presets` (bool):

```json
{"name": "filter_tools", "arguments": {"toolset": ["preset:read-only", "server:pro"], "include_presets": true}}
```

The response carries the tools the toolset resolves to **for the caller**, `toolset` (the
selectors as given), `toolset_unmatched` (the selectors that selected nothing for the caller —
for example a server the caller has not signed in to; `preset:none` is never reported) and
`presets` (`[{name, description, built_in}]`, built-ins first). When the request also carries
`X-Muster-Toolset`, the argument resolves **within** the header's toolset and never widens it.
Argument errors use the header's texts.

### Tool information

Every entry of `list_tools` / `filter_tools`, and `describe_tool`, carries:

- `server` — the owning MCPServer (the family name for a family tool); omitted for workflows and core tools,
- `kind` — `tool` (served by an MCPServer), `workflow` or `core`,
- `annotations` — the hints the server declared (`readOnlyHint`, `destructiveHint`,
  `idempotentHint`, `openWorldHint`), or the derived `readOnlyHint` of a workflow; omitted when
  the tool carries none.

## Composition, not authorization

The toolset is declared by the request. A client that omits the header reaches everything the
invoking human may reach — which is what it could reach before toolsets existed. Enforcing a
toolset against an agent runtime's will needs an agent identity muster can trust; when that
exists, muster resolves the toolset from the identity through the same seam
(`toolset.SourceFromRequest`) and presets, resolution, the refusal and the scenarios carry over
unchanged.
