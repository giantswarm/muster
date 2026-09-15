# muster get

Get detailed information about a resource

## Synopsis

Get detailed information about a specific resource.

Available resource types:
  service             - Get detailed status of a service (by name)
  mcpserver           - Get MCP server details and configuration (by name)
  workflow            - Get workflow definition and details (by name)
  workflow-execution  - Get workflow execution details and results (by execution ID)
  tool                - Get MCP tool details including input schema (by name)
  resource            - Get MCP resource metadata (by URI)
  prompt              - Get MCP prompt details including arguments (by name)

Examples:
  muster get service prometheus
  muster get workflow auth-flow
  muster get workflow-execution abc123-def456-789
  muster get mcpserver kubernetes --output yaml
  muster get tool core_service_list
  muster get resource muster://auth/status
  muster get prompt code_review

Note: The aggregator server must be running (use 'muster serve') before using these commands.

```
muster get <type> <name|uri|id>
```

## Options

```
      --auth string          Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --debug                Enable debug logging (show MCP protocol messages)
      --endpoint string      Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
  -h, --help                 help for get
      --no-headers           Suppress header row in table output
  -o, --output string        Output format (table, wide, json, yaml) (default "table")
  -q, --quiet                Suppress non-essential output
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
