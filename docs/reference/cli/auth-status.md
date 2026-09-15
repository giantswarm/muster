# muster auth status

Show authentication status

## Synopsis

Show the current authentication status for all known endpoints.

This command displays which endpoints you are authenticated to, when
tokens expire, and which endpoints require authentication.

Examples:
  muster auth status                   # Show all auth status
  muster auth status --endpoint <url>  # Show status for specific endpoint
  muster auth status --server <name>   # Show status for specific MCP server

```
muster auth status [flags]
```

## Options

```
  -h, --help            help for status
      --server string   MCP server name (managed by aggregator) to show status for
```

## Options inherited from parent commands

```
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --endpoint string      Specific endpoint URL to authenticate to
  -q, --quiet                Suppress non-essential output
```

## SEE ALSO

* [muster auth](auth.md)	 - Manage authentication for muster
