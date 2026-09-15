# muster auth login

Authenticate to a muster aggregator

## Synopsis

Authenticate to a muster aggregator using OAuth.

This command initiates an OAuth browser-based authentication flow to obtain
access tokens for connecting to OAuth-protected muster aggregators.

Examples:
  muster auth login                    # Login to configured aggregator
  muster auth login --endpoint <url>   # Login to specific endpoint
  muster auth login --server <name>    # Login to specific MCP server
  muster auth login --all              # Login to aggregator + all pending MCP servers
  muster auth login --silent           # Attempt silent re-auth (requires IdP support)

```
muster auth login [flags]
```

## Options

```
      --all             Login to aggregator and all pending MCP servers
  -h, --help            help for login
      --server string   MCP server name (managed by aggregator) to authenticate to
      --silent          Attempt silent re-auth using OIDC prompt=none (requires IdP support, not supported by Dex)
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
