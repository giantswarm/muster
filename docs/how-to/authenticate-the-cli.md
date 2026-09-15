# Authenticate the CLI

A muster that runs with `oauth.server.enabled` accepts requests only with a valid token. The
`muster` CLI obtains that token through a browser login and keeps it for you. This guide covers
the login, the contexts that hold endpoints, the authentication modes for scripts, where tokens
live, and the exit codes.

## Log in

```bash
muster auth login --endpoint https://muster.example.com/mcp
```

The CLI opens the browser at muster's authorization endpoint, muster redirects to Dex, and after
the sign-in the tokens are stored locally. `muster auth status` shows the identity and how long
the session lasts; `muster auth whoami` prints the identity alone.

```bash
muster auth status
muster auth whoami
muster auth logout            # this endpoint
muster auth logout --all      # every stored token
```

Every command that talks to the aggregator (`list`, `get`, `call`, `create`, `start`, `stop`,
`check`, `events`, `agent`) uses the stored token and refreshes it when it expires. When no
valid token exists, the default behaviour is to start the login in the browser before running
the command.

## Contexts

Contexts store endpoints under a name so that `--endpoint` is not repeated on every command.
They live in `~/.config/muster/contexts.yaml`.

```bash
muster context add prod --endpoint https://muster.example.com/mcp --use
muster context add staging --endpoint https://muster.staging.example.com/mcp
muster context list
muster context use staging
muster context current
```

The endpoint a command uses is resolved in this order:

1. `--endpoint`
2. `--context <name>`
3. `MUSTER_CONTEXT`
4. `current-context` in `contexts.yaml`
5. `http://localhost:8090/mcp`

Tokens are stored per endpoint, so switching contexts switches identities as well.

## Authentication modes

`--auth` (or `MUSTER_AUTH_MODE`) decides what happens when a command needs a token it does not
have:

| Mode | Behaviour |
|---|---|
| `auto` (default) | Open the browser and complete the login, then run the command |
| `prompt` | Ask before opening the browser |
| `none` | Fail with exit code 2; nothing interactive happens |

`none` is the mode for scripts and CI: a missing login becomes a clear failure instead of a
hanging browser call.

```bash
muster list mcpserver --auth none || echo "login required"
```

## Signing in to MCP servers behind muster

Some registered servers require their own login (a remote server with `auth.type: oauth` that
does not accept muster's forwarded identity). `muster auth login --server <name>` completes that
login for one server; `muster auth login --all` signs you in to muster and every server that
is waiting for a login. Within an agent session the same is done with the `core_auth_login`
tool, which returns the login URL for the server. `muster auth logout` and `core_auth_logout`
revoke those grants.

## Where tokens live

Tokens are written to `~/.config/muster/tokens/`, one file per endpoint, with the file mode
`0600` and the directory mode `0700`; file names are hashes of the endpoint. A token file holds
the access token, the refresh token, the expiry and the issuer. Tokens never appear in muster's
logs; only hashed identifiers do.

`MUSTER_OAUTH_CALLBACK_PORT` changes the local port the browser is redirected back to (default
`3000`) when that port is taken.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | Error: the command failed or its arguments were invalid |
| `2` | Authentication required and not available (`--auth none`, or the token could not be refreshed) |
| `3` | Authentication failed: the OAuth flow itself did not complete |
| `125` | `self-update --check` only: a newer release exists |

```bash
muster list mcpserver --auth none
case $? in
  0) ;;
  2) muster auth login ;;
  *) exit 1 ;;
esac
```

## Related

- [Connect MCP clients](connect-mcp-clients.md): the same login, performed by the stdio bridge for an IDE.
- [Security](../operations/security.md): how the tokens between client, muster and Dex relate, and how long a session lasts.
- [muster auth](../reference/cli/auth.md) and [muster context](../reference/cli/context.md) in the CLI reference.
