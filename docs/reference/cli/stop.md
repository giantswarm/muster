# muster stop

Stop a resource

## Synopsis

Stop a resource in the muster environment.

Available resource types:
  service - Stop a service by its name

Examples:
  muster stop service prometheus
  muster stop service vault

Note: The aggregator server must be running (use 'muster serve') before using these commands.

```
muster stop
```

## Options

```
      --auth string          Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --debug                Enable debug logging (show MCP protocol messages)
      --endpoint string      Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
  -h, --help                 help for stop
      --no-headers           Suppress header row in table output
  -o, --output string        Output format (table, wide, json, yaml) (default "table")
  -q, --quiet                Suppress non-essential output
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
