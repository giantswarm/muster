# muster call

Call an MCP tool by name

## Synopsis

Call any MCP tool directly by name with arbitrary arguments.

Arguments are passed as --key=value or --key value flags. The flags muster
itself declares (--endpoint, --auth, --output and the others listed below) are
never passed on. To pass an argument that shares a name with one of them, put
it after "--": everything after the separator is an argument. Use --json to
pass a JSON object as arguments instead.

Examples:
  muster call core_service_list
  muster call core_service_status --name=prometheus
  muster call workflow_deploy --environment=production --replicas=3
  muster call core_mcpserver_list --output json
  muster call x_http_get --endpoint http://muster:8090/mcp -- --endpoint=https://target

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
