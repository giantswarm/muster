# muster auth logout

Clear stored authentication tokens

## Synopsis

Clear stored OAuth tokens.

This command removes cached authentication tokens, requiring you to
re-authenticate on the next connection to protected endpoints.

Examples:
  muster auth logout                   # Logout from configured aggregator
  muster auth logout --endpoint <url>  # Logout from specific endpoint
  muster auth logout -s <name>         # Logout from specific MCP server
  muster auth logout --all             # Clear all stored tokens
  muster auth logout --all --yes       # Clear all without confirmation

```
muster auth logout [flags]
```

## Options

```
      --all             Clear all stored tokens
  -h, --help            help for logout
  -s, --server string   MCP server name to disconnect
  -y, --yes             Skip confirmation prompt for --all
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
