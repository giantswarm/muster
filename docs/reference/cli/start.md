# muster start

Start a resource

## Synopsis

Start a resource in the muster environment.

Available resource types:
  service   - Start a service by its name
  workflow  - Execute a workflow with optional parameters

Workflow arguments are passed as --name=value or --name value flags. The flags
muster itself declares (--endpoint, --auth, --output and the others listed
below) are never passed on. To pass a workflow argument that shares a name with
one of them, put it after "--": everything after the separator is an argument.

Examples:
  muster start service prometheus
  muster start service vault
  muster start workflow deploy-app --environment=production --replicas=3
  muster start workflow auth-setup --cluster=test
  muster start workflow sync --endpoint http://muster:8090/mcp -- --endpoint=https://target

Note: The aggregator server must be running (use 'muster serve') before using these commands.

```
muster start <type> <name> [--arg=value ...]
```

## Options

```
      --auth string          Authentication mode: none (default: fail with auth_required), prompt, or auto (env: MUSTER_AUTH_MODE)
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --debug                Enable debug logging (show MCP protocol messages)
      --endpoint string      Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
  -h, --help                 help for start
      --login                Open the browser to sign in when authentication is required (same as --auth auto)
      --no-headers           Suppress header row in table output
  -o, --output string        Output format (table, wide, json, yaml) (default "table")
  -q, --quiet                Suppress non-essential output
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
