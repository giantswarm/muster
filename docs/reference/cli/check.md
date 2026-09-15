# muster check

Check if a resource is available

## Synopsis

Check if a resource is available and properly configured.

Available resource types:
  mcpserver    - Check MCP server status
  workflow     - Check if a workflow is available (all required tools present)

Examples:
  muster check mcpserver prometheus
  muster check workflow my-deployment

Note: The aggregator server must be running (use 'muster serve') before using these commands.

```
muster check
```

## Options

```
      --auth string          Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --debug                Enable debug logging (show MCP protocol messages)
      --endpoint string      Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
  -h, --help                 help for check
      --no-headers           Suppress header row in table output
  -o, --output string        Output format (table, wide, json, yaml) (default "table")
  -q, --quiet                Suppress non-essential output
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
