# muster call

Call an MCP tool by name

## Synopsis

Call any MCP tool directly by name with arbitrary arguments.

Arguments can be passed as --key=value or --key value flags.
Use --json to pass a JSON object as arguments instead.

Examples:
  muster call core_service_list
  muster call core_service_status --name=prometheus
  muster call workflow_deploy --environment=production --replicas=3
  muster call core_mcpserver_list --output json

Note: The aggregator server must be running (use 'muster serve') before using this command.

```
muster call <tool-name> [--arg=value ...]
```

## Options

```
      --auth string          Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --debug                Enable debug logging (show MCP protocol messages)
      --endpoint string      Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
  -h, --help                 help for call
      --json string          Pass tool arguments as a JSON object
      --no-headers           Suppress header row in table output
  -o, --output string        Output format (table, wide, json, yaml) (default "table")
  -q, --quiet                Suppress non-essential output
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
