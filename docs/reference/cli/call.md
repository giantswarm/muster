# muster call

Call an MCP tool by name

## Synopsis

Call any MCP tool directly by name with arbitrary arguments.

Arguments are passed as --key=value or --key value flags. The flags muster
itself declares (--endpoint, --auth, --output and the others listed below) are
never passed on. To pass an argument that shares a name with one of them, put
it after "--": everything after the separator is an argument. A value that is
a JSON array or object is sent as one when the tool's parameter is an array or
an object (--clusters '["a","b"]'). Use --json to pass a JSON object as
arguments instead. A call waits 5 minutes for its
answer; --call-timeout waits longer, for a workflow that runs longer.

Examples:
  muster call core_service_list
  muster call core_service_status --name=prometheus
  muster call workflow_deploy --environment=production --replicas=3
  muster call core_mcpserver_list --output json
  muster call workflow_fleet-image-scan --call-timeout 45m --management_clusters '["alba"]'
  muster call x_http_get --endpoint http://muster:8090/mcp -- --endpoint=https://target

Note: The aggregator server must be running (use 'muster serve') before using this command.

```
muster call <tool-name> [--arg=value ...]
```

## Options

```
      --auth string             Authentication mode: none (default: fail with auth_required), prompt, or auto (env: MUSTER_AUTH_MODE)
      --call-timeout duration   How long to wait for the tool's answer, e.g. 45m for a workflow that scans a fleet (default 5m0s)
      --config-path string      Configuration directory (default "~/.config/muster")
      --context string          Use a specific context (env: MUSTER_CONTEXT)
      --debug                   Enable debug logging (show MCP protocol messages)
      --endpoint string         Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
  -h, --help                    help for call
      --json string             Pass tool arguments as a JSON object
      --login                   Open the browser to sign in when authentication is required (same as --auth auto)
      --no-headers              Suppress header row in table output
  -o, --output string           Output format (table, wide, json, yaml) (default "table")
  -q, --quiet                   Suppress non-essential output
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
