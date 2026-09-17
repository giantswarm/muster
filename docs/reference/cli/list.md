# muster list

List resources

## Synopsis

List resources in the muster environment.

Available resource types:
  service(s)              - List all services with their status
  mcpserver(s)            - List all MCP server definitions
  workflow(s)             - List all workflow definitions
  workflow-execution(s)   - List all workflow execution history
  tool(s)                 - List all MCP tools from aggregated servers
  resource(s)             - List all MCP resources from aggregated servers
  prompt(s)               - List all MCP prompts from aggregated servers

Filtering (for MCP primitives only: tool, resource, prompt):
  --filter <pattern>       - Filter by name pattern (wildcards * and ? supported)
  --description <text>     - Filter by description content (case-insensitive substring)
  --server <name>          - Filter by server. Tools: the server a tool belongs to as the
                             aggregator reports it (e.g. "files" for x_files_*, "core",
                             "workflow"), or the prefix of the exposed name (e.g. "x_files").
                             Resources and prompts: the prefix of the exposed name.

Output options:
  --output/-o <format>     - Output format: table (default), wide, json, yaml
  --no-headers             - Suppress header row in table output (useful for scripting)

The 'wide' format (-o wide) shows additional columns for each resource type:
  services       - endpoint, tools count
  mcpservers     - url/command, timeout
  workflows      - input arguments
  tools          - server, argument count
  resources      - name
  prompts        - argument count

Examples:
  muster list service
  muster list services -o wide
  muster list workflow
  muster list workflow-execution
  muster list mcpservers -o wide
  muster list tool
  muster list tools -o wide
  muster list tools --filter "core_*"
  muster list tools --server github
  muster list tools --server files -o wide
  muster list tools --filter "*service*" --description "status"
  muster list resources --output yaml
  muster list mcpservers --no-headers | awk '{print $1}'

Note: The aggregator server must be running (use 'muster serve') before using these commands.

```
muster list
```

## Options

```
      --all                  Show all servers including unreachable ones (for mcpserver only)
      --auth string          Authentication mode: none (default: fail with auth_required), prompt, or auto (env: MUSTER_AUTH_MODE)
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --debug                Enable debug logging (show MCP protocol messages)
      --description string   Filter by description content (case-insensitive substring, for MCP primitives only)
      --endpoint string      Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
      --filter string        Filter by name pattern (wildcards * and ? supported, for MCP primitives only)
  -h, --help                 help for list
      --login                Open the browser to sign in when authentication is required (same as --auth auto)
      --no-headers           Suppress header row in table output
  -o, --output string        Output format (table, wide, json, yaml) (default "table")
  -q, --quiet                Suppress non-essential output
      --server string        Filter by server: the server a tool belongs to (e.g. "files", "core") or the exposed name prefix (e.g. "x_files"); for MCP primitives only
      --verbose              Show detailed error information for failed/unreachable servers (for mcpserver only)
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
