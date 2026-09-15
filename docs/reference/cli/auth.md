# muster auth

Manage authentication for muster

## Synopsis

Manage authentication for muster CLI commands.

The auth command group provides subcommands to login, logout, check status,
and refresh authentication tokens for remote muster aggregators that require
OAuth authentication.

Examples:
  muster auth login                    # Login to configured aggregator
  muster auth login --endpoint <url>   # Login to specific remote endpoint
  muster auth status                   # Show authentication status
  muster auth logout                   # Logout from configured aggregator
  muster auth logout --all             # Clear all stored tokens
  muster auth whoami                   # Show current identity

## Options

```
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --endpoint string      Specific endpoint URL to authenticate to
  -h, --help                 help for auth
  -q, --quiet                Suppress non-essential output
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
* [muster auth login](auth-login.md)	 - Authenticate to a muster aggregator
* [muster auth logout](auth-logout.md)	 - Clear stored authentication tokens
* [muster auth status](auth-status.md)	 - Show authentication status
* [muster auth whoami](auth-whoami.md)	 - Show current authenticated identity
