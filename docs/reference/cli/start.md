# muster start

Start a resource

## Synopsis

Start a resource in the muster environment.

Available resource types:
  service   - Start a service by its name
  workflow  - Execute a workflow with optional parameters

Examples:
  muster start service prometheus
  muster start service vault
  muster start workflow deploy-app --environment=production --replicas=3
  muster start workflow auth-setup --cluster=test

Note: The aggregator server must be running (use 'muster serve') before using these commands.

```
muster start
```

## Options

```
      --auth string          Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --debug                Enable debug logging (show MCP protocol messages)
      --endpoint string      Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
  -h, --help                 help for start
      --no-headers           Suppress header row in table output
  -o, --output string        Output format (table, wide, json, yaml) (default "table")
  -q, --quiet                Suppress non-essential output
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
