# muster events

List events for muster resources

## Synopsis

List and filter events for muster resources in both Kubernetes and filesystem modes.

This command provides access to event history for muster components including
MCPServers and Workflows. Events are automatically generated during resource
lifecycle operations and can be queried with various filters.

Filtering Options:
  --resource-type     Filter by resource type (mcpserver, workflow)
  --resource-name     Filter by specific resource name
  --namespace         Filter by namespace (default: all namespaces)
  --type              Filter by event type (Normal, Warning)
  --since             Show events after this time (1h, 30m, 2024-01-15T10:00:00Z)
  --until             Show events before this time (2024-01-15T18:00:00Z)
  --limit             Limit number of events returned (default: 50)
  --follow, -f        Stream new events as they occur (follow mode)

Examples:
  # List all recent events
  muster events

  # Filter by resource type
  muster events --resource-type mcpserver
  muster events --resource-type workflow

  # Filter by specific resource
  muster events --resource-type mcpserver --resource-name prometheus

  # Filter by namespace
  muster events --namespace default
  muster events --namespace muster-system

  # Filter by time range
  muster events --since 1h
  muster events --since 2024-01-15T10:00:00Z --until 2024-01-15T18:00:00Z

  # Filter by event type
  muster events --type Warning
  muster events --type Normal

  # Combine filters and change output format
  muster events --resource-type mcpserver --namespace default --limit 20 --output json

  # Stream new events (follow mode)
  muster events --follow
  muster events --resource-type mcpserver --follow

  # Stream events with filters
  muster events --namespace default --type Warning --follow

Note: The aggregator server must be running (use 'muster serve') before using this command.

```
muster events
```

## Options

```
      --auth string            Authentication mode: none (default: fail with auth_required), prompt, or auto (env: MUSTER_AUTH_MODE)
      --config-path string     Configuration directory (default "~/.config/muster")
      --context string         Use a specific context (env: MUSTER_CONTEXT)
      --debug                  Enable debug logging (show MCP protocol messages)
      --endpoint string        Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
  -f, --follow                 Stream new events as they occur
  -h, --help                   help for events
      --limit int              Limit number of events returned (default 50)
      --login                  Open the browser to sign in when authentication is required (same as --auth auto)
      --namespace string       Filter by namespace
      --no-headers             Suppress header row in table output
  -o, --output string          Output format (table, wide, json, yaml) (default "table")
  -q, --quiet                  Suppress non-essential output
      --resource-name string   Filter by resource name
      --resource-type string   Filter by resource type (mcpserver, workflow)
      --since string           Show events after this time (e.g., 1h, 30m, 2024-01-15T10:00:00Z)
      --type string            Filter by event type (Normal, Warning)
      --until string           Show events before this time (e.g., 2024-01-15T18:00:00Z)
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
