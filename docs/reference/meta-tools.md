# Meta-tools

Generated from the registered meta-tools (`make tools-doc`): every description and input schema exactly as an MCP client lists them on connect. Through `muster agent --mcp-server`, `call_tool` also takes `timeout`, which the agent consumes. [MCP tools](mcp-tools.md) covers what each one answers.

| Tool | Kind |
|---|---|
| `call_tool` | write, destructive |
| `describe_prompt` | read-only |
| `describe_resource` | read-only |
| `describe_tool` | read-only |
| `filter_prompts` | read-only |
| `filter_resources` | read-only |
| `filter_tools` | read-only |
| `get_prompt` | read-only |
| `get_resource` | read-only |
| `list_core_tools` | read-only |
| `list_prompts` | read-only |
| `list_resources` | read-only |
| `list_tools` | read-only |

## `call_tool`

```text
Execute a catalogue tool by name: aggregated tools are callable only this way, never by their own name.

Write name first, then arguments: a name written after a long arguments object gets lost in it. A request that declares a toolset (X-Muster-Toolset) can only call tools inside it.
```

```json
{
  "properties": {
    "arguments": {
      "description": "Arguments to pass to the tool (as JSON object)",
      "type": "object"
    },
    "name": {
      "description": "Name of the tool to call. Always set, and written before arguments.",
      "type": "string"
    }
  },
  "required": [
    "name"
  ],
  "type": "object"
}
```

## `describe_prompt`

```text
Describe one prompt by name: its description and arguments.
```

```json
{
  "properties": {
    "name": {
      "description": "Name of the prompt to describe",
      "type": "string"
    }
  },
  "required": [
    "name"
  ],
  "type": "object"
}
```

## `describe_resource`

```text
Describe one resource by URI: its name, description, MIME type and server, not its contents.
```

```json
{
  "properties": {
    "server": {
      "description": "Server to describe the resource from. Only needed when several servers expose the same URI; list_resources reports the server for every entry",
      "type": "string"
    },
    "uri": {
      "description": "URI of the resource to describe",
      "type": "string"
    }
  },
  "required": [
    "uri"
  ],
  "type": "object"
}
```

## `describe_tool`

```text
Get one tool's full description, input schema, origin and annotations, and the call_tool call that runs it.
```

```json
{
  "properties": {
    "name": {
      "description": "Name of the tool to describe",
      "type": "string"
    }
  },
  "required": [
    "name"
  ],
  "type": "object"
}
```

## `filter_prompts`

```text
Filter the prompts by name pattern and/or source server, one page at a time; names are x_<server>_<name>.
```

```json
{
  "properties": {
    "case_sensitive": {
      "description": "Match case-sensitively (default false)",
      "type": "boolean"
    },
    "limit": {
      "description": "Maximum number of results to return per page",
      "type": "number"
    },
    "offset": {
      "description": "Number of matches to skip before the returned page",
      "type": "number"
    },
    "pattern": {
      "description": "Glob pattern to match against the prompt name (e.g. \"x_myserver_*\")",
      "type": "string"
    },
    "server": {
      "description": "Only return prompts exposed by this server",
      "type": "string"
    }
  },
  "required": [],
  "type": "object"
}
```

## `filter_resources`

```text
Filter the resources by URI pattern and/or source server, one page at a time.

A URI with a scheme carries no server prefix, so scope to one server with server, not the URI.
```

```json
{
  "properties": {
    "case_sensitive": {
      "description": "Match case-sensitively (default false)",
      "type": "boolean"
    },
    "limit": {
      "description": "Maximum number of results to return per page",
      "type": "number"
    },
    "offset": {
      "description": "Number of matches to skip before the returned page",
      "type": "number"
    },
    "pattern": {
      "description": "Glob pattern to match against the resource URI (e.g. \"board://*\")",
      "type": "string"
    },
    "server": {
      "description": "Only return resources exposed by this server",
      "type": "string"
    }
  },
  "required": [],
  "type": "object"
}
```

## `filter_tools`

```text
Find tools by name pattern, description, labels or a ranked natural-language query, as a small summarised page.

A tool you cannot find may sit behind a sign-in: servers_requiring_auth names each server whose tools the pattern could match, and its auth_tool with that server's name starts the sign-in. describe_tool gives a chosen tool's full description and schema. The response's toolset names the toolset bounding you: the toolset argument, else X-Muster-Toolset.
```

```json
{
  "properties": {
    "case_sensitive": {
      "default": false,
      "description": "Whether pattern matching should be case-sensitive",
      "type": "boolean"
    },
    "description_filter": {
      "description": "Filter by description content (case-insensitive substring match)",
      "type": "string"
    },
    "include_presets": {
      "default": false,
      "description": "Include the known toolset presets (name, description, built_in) in the response",
      "type": "boolean"
    },
    "include_schema": {
      "default": false,
      "description": "Whether to include full input schemas and full descriptions instead of one-line summaries",
      "type": "boolean"
    },
    "labels": {
      "description": "Label facets to scope discovery, as key=value pairs. A tool must carry every given label to match (e.g. {\"category\": \"observability\"}).",
      "type": "object"
    },
    "limit": {
      "default": 5,
      "description": "Maximum number of tools in this page",
      "type": "number"
    },
    "offset": {
      "default": 0,
      "description": "Number of matching tools to skip before this page",
      "type": "number"
    },
    "pattern": {
      "description": "Glob on tool names (e.g. x_slack_*); it also picks the servers_requiring_auth entries whose tool prefix it could match",
      "type": "string"
    },
    "query": {
      "description": "Natural-language query: matches come best-first with a score, a name match weighing most; tools that match no word are dropped",
      "type": "string"
    },
    "toolset": {
      "description": "Toolset selectors to resolve for you: preset:<name>, server:<name>, workflow:<name>, tool:<name>, at most 32; never wider than X-Muster-Toolset",
      "items": {
        "type": "string"
      },
      "type": "array"
    }
  },
  "required": [],
  "type": "object"
}
```

## `get_prompt`

```text
Render one prompt by name with the given arguments.
```

```json
{
  "properties": {
    "arguments": {
      "description": "Arguments to pass to the prompt (as JSON object with string values)",
      "type": "object"
    },
    "name": {
      "description": "Name of the prompt to get",
      "type": "string"
    }
  },
  "required": [
    "name"
  ],
  "type": "object"
}
```

## `get_resource`

```text
Read the contents of one resource by URI.
```

```json
{
  "properties": {
    "server": {
      "description": "Server to read from. Only needed when several servers expose the same URI; list_resources reports the server for every entry",
      "type": "string"
    },
    "uri": {
      "description": "URI of the resource to retrieve",
      "type": "string"
    }
  },
  "required": [
    "uri"
  ],
  "type": "object"
}
```

## `list_core_tools`

```text
List muster's own core tools (core_*), apart from the tools of the MCP servers it aggregates.
```

```json
{
  "properties": {
    "include_schema": {
      "default": true,
      "description": "Whether to include full tool specifications with input schemas",
      "type": "boolean"
    }
  },
  "required": [],
  "type": "object"
}
```

## `list_prompts`

```text
List the prompts of the connected MCP servers.
```

```json
{
  "properties": {},
  "required": [],
  "type": "object"
}
```

## `list_resources`

```text
List the resources of the connected MCP servers, each with the server exposing it.
```

```json
{
  "properties": {},
  "required": [],
  "type": "object"
}
```

## `list_tools`

```text
List one page of your tools as summaries, plus servers a sign-in would unlock; to find a tool use filter_tools.

Costly: the catalogue can hold hundreds of tools and every page stays in your context, so do not page through it to find one. describe_tool gives a tool's schema. truncated says more pages exist at offset + limit.
```

```json
{
  "properties": {
    "limit": {
      "default": 50,
      "description": "Maximum number of tools in this page",
      "type": "number"
    },
    "offset": {
      "default": 0,
      "description": "Number of tools to skip before this page",
      "type": "number"
    }
  },
  "required": [],
  "type": "object"
}
```
