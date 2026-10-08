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
  muster auth login --force            # Sign in again although the session is valid
  muster auth login --callback-port 3001  # Take the browser's callback on another port
  muster auth login --no-browser       # Print the sign-in URL instead of opening a browser

A valid session is reused. The session's automatic refresh renews the access
token and the OIDC ID token together, once the access token has expired; when
the session carries no ID token or an expired one, login signs in again through
the browser so the token file carries a current one now (see 'muster auth
token --id'). --force signs in again regardless.

The browser returns to http://localhost:<port>/callback (default 3000). muster
listens on that port on 127.0.0.1 and ::1 and stops at once, naming the
holder, when another process has it on either. --callback-port (env:
MUSTER_OAUTH_CALLBACK_PORT) picks another port, for a server that accepts a
localhost redirect to it.

--no-browser (env: MUSTER_NO_BROWSER=1) starts no browser: the sign-in URL is
printed to stdout and login waits for the callback until the URL is opened in
any browser on this machine. Otherwise the commands in $BROWSER are tried
before the platform default (xdg-open, open, start).

```
muster auth login [flags]
```

## Options

```
      --all                 Login to aggregator and all pending MCP servers
      --callback-port int   Local port the browser is redirected back to (env: MUSTER_OAUTH_CALLBACK_PORT) (default 3000)
      --force               Sign in again through the browser although the session is valid, renewing the stored ID token
  -h, --help                help for login
      --no-browser          Print the sign-in URL and wait for the callback instead of opening a browser (env: MUSTER_NO_BROWSER)
      --server string       MCP server name (managed by aggregator) to authenticate to
      --silent              Attempt silent re-auth using OIDC prompt=none (requires IdP support, not supported by Dex)
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
