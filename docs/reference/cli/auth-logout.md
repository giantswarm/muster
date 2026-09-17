# muster auth logout

Clear stored authentication tokens

## Synopsis

Clear stored OAuth tokens.

This command removes cached authentication tokens, requiring you to
re-authenticate on the next connection to protected endpoints.

With --server the command signs the session out of one MCP server at the
aggregator and nothing else: the server's tools are hidden for this session
until 'muster auth login --server <name>', and the session stays signed in to
the aggregator. Without --server the stored token for the aggregator is
removed, which ends the session with every server.

Examples:
  muster auth logout                   # Logout from configured aggregator
  muster auth logout --endpoint <url>  # Logout from specific endpoint
  muster auth logout -s <name>         # Disconnect one MCP server, stay signed in
  muster auth logout --all             # Clear all stored tokens
  muster auth logout --all --yes       # Clear all without confirmation

```
muster auth logout [flags]
```

## Options

```
      --all             Clear all stored tokens
  -h, --help            help for logout
  -s, --server string   MCP server to sign out of for this session (the aggregator session stays)
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
