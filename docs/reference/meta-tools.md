# Meta-tools

Generated from the provider (`make tools-doc`): every meta-tool's description, annotations and arguments as an MCP client lists them on connect. See [MCP tools](mcp-tools.md) for what they answer.

## `list_tools`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: false.

```text
List one page of your tools as one-line summaries, plus the servers a sign-in would unlock (servers_requiring_auth).

Costly: the catalogue holds hundreds of tools and every page stays in your context, so do not page through it to find a tool: filter_tools finds one, describe_tool gives its schema. truncated says more pages exist at offset + limit.
```

| Argument | Type | Required | Description |
|---|---|---|---|
| `limit` | number | false | Maximum number of tools in this page (default: 50) |
| `offset` | number | false | Number of tools to skip before this page (default: 0) |

## `describe_tool`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: false.

```text
Get one tool's full description, input schema, origin and annotations, and the call_tool call that runs it.
```

| Argument | Type | Required | Description |
|---|---|---|---|
| `name` | string | true | Name of the tool to describe |

## `list_core_tools`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: false.

```text
List muster's own core tools (core_*), apart from the tools of the MCP servers it aggregates.
```

| Argument | Type | Required | Description |
|---|---|---|---|
| `include_schema` | boolean | false | Whether to include full tool specifications with input schemas (default: true) |

## `filter_tools`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: false.

```text
Find tools by name pattern, description, labels or a ranked natural-language query, as a small summarised page.

A tool you cannot find may sit behind a sign-in: servers_requiring_auth names each server whose tools the pattern could match, and its auth_tool with that server's name starts the sign-in. describe_tool gives a chosen tool's full description and schema. The response's toolset names the toolset bounding you: the toolset argument, else X-Muster-Toolset.
```

| Argument | Type | Required | Description |
|---|---|---|---|
| `toolset` | array | false | Toolset selectors to resolve for you: preset:&lt;name&gt;, server:&lt;name&gt;, workflow:&lt;name&gt;, tool:&lt;name&gt;, at most 32; never wider than X-Muster-Toolset |
| `include_presets` | boolean | false | Include the known toolset presets (name, description, built_in) in the response (default: false) |
| `pattern` | string | false | Glob on tool names (e.g. x_slack_*); it also picks the servers_requiring_auth entries whose tool prefix it could match |
| `description_filter` | string | false | Filter by description content (case-insensitive substring match) |
| `query` | string | false | Natural-language query: matches come best-first with a score, a name match weighing most; tools that match no word are dropped |
| `labels` | object | false | Label facets to scope discovery, as key=value pairs. A tool must carry every given label to match (e.g. {"category": "observability"}). |
| `case_sensitive` | boolean | false | Whether pattern matching should be case-sensitive (default: false) |
| `include_schema` | boolean | false | Whether to include full input schemas and full descriptions instead of one-line summaries (default: false) |
| `limit` | number | false | Maximum number of tools in this page (default: 5) |
| `offset` | number | false | Number of matching tools to skip before this page (default: 0) |

## `call_tool`

Annotations: readOnly: false, destructive: true, idempotent: false, openWorld: true.

```text
Execute a catalogue tool by name: aggregated tools are callable only this way, never by their own name.

Write name first, then arguments: a name written after a long arguments object gets lost in it. A request that declares a toolset (X-Muster-Toolset) can only call tools inside it.
```

| Argument | Type | Required | Description |
|---|---|---|---|
| `name` | string | true | Name of the tool to call. Always set, and written before arguments. |
| `arguments` | object | false | Arguments to pass to the tool (as JSON object) |

## `list_resources`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: false.

```text
List the resources of the connected MCP servers, each with the server exposing it.
```

## `filter_resources`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: false.

```text
Filter the resources by URI pattern and/or source server, one page at a time.

A URI with a scheme carries no server prefix, so scope to one server with server, not the URI.
```

| Argument | Type | Required | Description |
|---|---|---|---|
| `pattern` | string | false | Glob pattern to match against the resource URI (e.g. "board://*") |
| `server` | string | false | Only return resources exposed by this server |
| `case_sensitive` | boolean | false | Match case-sensitively (default false) |
| `limit` | number | false | Maximum number of results to return per page |
| `offset` | number | false | Number of matches to skip before the returned page |

## `describe_resource`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: false.

```text
Describe one resource by URI: its name, description, MIME type and server, not its contents.
```

| Argument | Type | Required | Description |
|---|---|---|---|
| `uri` | string | true | URI of the resource to describe |
| `server` | string | false | Server to describe the resource from. Only needed when several servers expose the same URI; list_resources reports the server for every entry |

## `get_resource`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: true.

```text
Read the contents of one resource by URI.
```

| Argument | Type | Required | Description |
|---|---|---|---|
| `uri` | string | true | URI of the resource to retrieve |
| `server` | string | false | Server to read from. Only needed when several servers expose the same URI; list_resources reports the server for every entry |

## `list_prompts`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: false.

```text
List the prompts of the connected MCP servers.
```

## `filter_prompts`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: false.

```text
Filter the prompts by name pattern and/or source server, one page at a time; names are x_<server>_<name>.
```

| Argument | Type | Required | Description |
|---|---|---|---|
| `pattern` | string | false | Glob pattern to match against the prompt name (e.g. "x_myserver_*") |
| `server` | string | false | Only return prompts exposed by this server |
| `case_sensitive` | boolean | false | Match case-sensitively (default false) |
| `limit` | number | false | Maximum number of results to return per page |
| `offset` | number | false | Number of matches to skip before the returned page |

## `describe_prompt`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: false.

```text
Describe one prompt by name: its description and arguments.
```

| Argument | Type | Required | Description |
|---|---|---|---|
| `name` | string | true | Name of the prompt to describe |

## `get_prompt`

Annotations: readOnly: true, destructive: false, idempotent: true, openWorld: true.

```text
Render one prompt by name with the given arguments.
```

| Argument | Type | Required | Description |
|---|---|---|---|
| `name` | string | true | Name of the prompt to get |
| `arguments` | object | false | Arguments to pass to the prompt (as JSON object with string values) |
