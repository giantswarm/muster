# muster auth

Manage authentication for connecting to OAuth-protected Muster aggregators.

## Synopsis

```bash
muster auth <subcommand> [OPTIONS]
```

## Description

The `auth` command group provides subcommands to manage OAuth authentication for connecting to remote Muster aggregators that require authentication. This is typically needed when connecting to aggregators deployed in production environments with OAuth/OIDC protection.

Muster supports RFC 9728 Protected Resource Metadata discovery, dynamic client registration, and PKCE-based OAuth 2.1 flows with browser-based authorization.

## Subcommands

### muster auth login

Authenticate to a Muster aggregator using OAuth.

```bash
muster auth login [OPTIONS]
```

**Options:**

- `--endpoint` (string): Specific endpoint URL to authenticate to
  - If not provided, uses the configured aggregator endpoint
- `--server` (string): Specific MCP server name to authenticate to
  - Authenticates to a remote MCP server managed by the aggregator
- `--all`: Authenticate to aggregator and all pending MCP servers
  - Provides SSO-style authentication chain
- `--no-silent`: Skip silent re-authentication, always use interactive login
  - By default, muster attempts silent re-auth using OIDC `prompt=none`

**Examples:**

```bash
# Login to configured aggregator
muster auth login

# Login to specific remote endpoint
muster auth login --endpoint https://muster.example.com/mcp

# Login to a specific MCP server through the aggregator
muster auth login --server mcp-kubernetes

# Login to aggregator and all MCP servers requiring auth
muster auth login --all

# Skip silent re-auth and always show the login page
muster auth login --no-silent
```

**What happens during login:**

1. Muster probes the endpoint to check if OAuth is required
2. If required, discovers OAuth metadata (issuer, authorization endpoint)
3. Opens your browser to the authorization page
4. Waits for you to complete authentication
5. Stores the token securely for future use

**Silent Re-Authentication (Optional):**

Silent re-authentication is **disabled by default** because Dex (the default IdP) does not support OIDC `prompt=none`. When silent auth fails, it causes two browser tabs to open.

If your IdP supports `prompt=none`, you can enable silent re-authentication with the `--silent` flag:

```bash
muster auth login --silent
```

When enabled, silent re-authentication provides a seamless experience:

- Opens browser with OIDC `prompt=none` parameter
- If IdP session is still valid, completes without user interaction
- If IdP session expired, falls back to interactive login

### muster auth logout

Clear stored authentication tokens.

```bash
muster auth logout [OPTIONS]
```

**Options:**

- `--endpoint` (string): Logout from specific endpoint
- `--all`: Clear all stored tokens (requires confirmation)
- `--yes, -y`: Skip confirmation prompt when using `--all`

**Examples:**

```bash
# Logout from configured aggregator
muster auth logout

# Logout from specific endpoint
muster auth logout --endpoint https://muster.example.com/mcp

# Clear all stored tokens (with confirmation)
muster auth logout --all

# Clear all stored tokens (skip confirmation)
muster auth logout --all --yes
```

### muster auth status

Show the current authentication status for all known endpoints.

```bash
muster auth status [OPTIONS]
```

**Options:**

- `--endpoint` (string): Show status for specific endpoint
- `--server` (string): Show status for specific MCP server

**Output:**

```
Muster Aggregator
  Endpoint:  https://muster.example.com/mcp
  Status:    Authenticated
  Expires:   in 23 minutes
  Session:   ~29 days remaining (auto-refresh)
  Issuer:    https://dex.example.com

MCP Servers
  (1 pending authentication)
  mcp-kubernetes      Connected
  mcp-github          Not authenticated   Run: muster auth login --server mcp-github
```

The **Expires** line shows when the current access token expires (typically 30 minutes).
Access tokens are automatically refreshed using the refresh token, so you don't need to
re-authenticate when they expire.

The **Session** line shows an approximate estimate (`~`) of how long your session will
remain active before re-authentication is required. This is based on the configured
session duration (default: 30 days). The actual session may end earlier if the upstream
identity provider (e.g., Dex) has a shorter absolute lifetime configured.

**Examples:**

```bash
# Show all auth status
muster auth status

# Show status for specific endpoint
muster auth status --endpoint https://muster.example.com/mcp

# Show status for specific MCP server
muster auth status --server mcp-kubernetes
```

### muster auth whoami

Show the currently authenticated identity and token information.

```bash
muster auth whoami [OPTIONS]
```

**Options:**

- `--endpoint` (string): Show identity for specific endpoint

**Output:**

```
Identity:  user@example.com
Endpoint:  https://muster.example.com/mcp
Issuer:    https://dex.example.com
Expires:   in 23 hours
```

**Examples:**

```bash
# Show identity for configured aggregator
muster auth whoami

# Show identity for specific endpoint
muster auth whoami --endpoint https://muster.example.com/mcp
```

## Common Options

These options are available on all auth subcommands:

- `--config-path` (string): Configuration directory
  - Default: `~/.config/muster`
- `--quiet, -q`: Suppress non-essential output

## Environment Variables

Muster supports several environment variables for authentication configuration:

| Variable | Description | Default |
|----------|-------------|---------|
| `MUSTER_ENDPOINT` | Default aggregator endpoint URL | (none) |
| `MUSTER_AUTH_MODE` | Authentication mode: `auto`, `prompt`, or `none` | `auto` |
| `MUSTER_OAUTH_CALLBACK_PORT` | Port for OAuth callback server | `3000` |

**Auth Modes:**

- **auto** (default): Automatically opens browser when authentication is required
- **prompt**: Asks for confirmation before opening browser
- **none**: Fails immediately if authentication is required

Example usage:

```bash
# Set default endpoint
export MUSTER_ENDPOINT=https://muster.example.com/mcp

# Now commands use this endpoint automatically
muster list mcpserver
muster auth status

# Use prompt mode for interactive scripts
export MUSTER_AUTH_MODE=prompt
muster list service

# Use a different callback port (if 3000 is in use)
export MUSTER_OAUTH_CALLBACK_PORT=8080
muster auth login
```

## Authentication on CLI Commands

All CLI commands that connect to the aggregator support the `--auth` flag:

```bash
muster list mcpserver --auth=auto    # Default: auto-open browser
muster list mcpserver --auth=prompt  # Ask before opening browser
muster list mcpserver --auth=none    # Fail if auth required
```

By default, authentication is automatic (`auto`): if authentication is required, muster will open your browser to complete the OAuth flow.

## Token Storage

Tokens are stored securely in `~/.config/muster/tokens/` with:

- File permissions of `0600` (owner read/write only)
- Directory permissions of `0700` (owner only)
- Hashed filenames to avoid exposing server URLs

Tokens include:
- Access token (for API authentication)
- Refresh token (for obtaining new access tokens)
- Expiry time
- Issuer information

## OAuth Flow

Muster uses a secure OAuth 2.1 flow with PKCE:

1. **Discovery**: Probes `/.well-known/oauth-protected-resource` for OAuth metadata
2. **Client Registration**: Uses Client ID Metadata Documents (CIMD) for dynamic registration
3. **Authorization**: Opens browser to the identity provider's login page
4. **Callback**: Local server on port 3000 receives the authorization code
5. **Token Exchange**: Exchanges code for access and refresh tokens
6. **Storage**: Stores tokens securely for future use

## Integration with Other Commands

Once authenticated, all CLI commands automatically use stored tokens:

```bash
# After successful login
muster auth login --endpoint https://muster.example.com/mcp

# These commands now work against the protected aggregator
muster list mcpserver --endpoint https://muster.example.com/mcp
muster get service myservice --endpoint https://muster.example.com/mcp
muster agent --endpoint https://muster.example.com/mcp
```

## Error Messages

When commands fail due to missing authentication, actionable guidance is provided:

```
Authentication required for https://muster.example.com/mcp

To authenticate, run:
  muster auth login --endpoint https://muster.example.com/mcp

To check current authentication status:
  muster auth status
```

## Examples

### First-Time Setup

```bash
# 1. Check if authentication is required
muster list mcpserver --endpoint https://muster.example.com/mcp
# Error: Authentication required...

# 2. Authenticate
muster auth login --endpoint https://muster.example.com/mcp
# Opens browser, complete login

# 3. Verify authentication
muster auth status
# Shows: Authenticated, Expires in 23 minutes, Session ~30 days remaining

# 4. Now commands work
muster list mcpserver --endpoint https://muster.example.com/mcp
```

### Multiple Endpoints

```bash
# Authenticate to multiple aggregators
muster auth login --endpoint https://prod.example.com/mcp
muster auth login --endpoint https://staging.example.com/mcp

# Check status of all
muster auth status

# Logout from all (with confirmation)
muster auth logout --all
```

### Token Refresh

```bash
# If experiencing auth issues, re-authenticate
muster auth logout --endpoint https://muster.example.com/mcp
muster auth login --endpoint https://muster.example.com/mcp
```

### Scripting with Quiet Mode

```bash
# Use quiet mode for scripts
muster auth login --endpoint https://muster.example.com/mcp --quiet
if [ $? -eq 0 ]; then
  muster list service --endpoint https://muster.example.com/mcp --quiet
fi
```

## Troubleshooting

### Browser Doesn't Open

If the browser doesn't open automatically, you'll see:

```
Opening browser for authentication... failed
Please open this URL in your browser:
  https://dex.example.com/auth?...
```

Copy and paste the URL manually into your browser.

### Callback Port in Use

If port 3000 is already in use:

```
Authentication failed: callback port 3000 is already in use. Please free port 3000 and try again
```

**Option 1:** Use a different port via environment variable:

```bash
export MUSTER_OAUTH_CALLBACK_PORT=8080
muster auth login
```

**Option 2:** Free port 3000:

```bash
# Find what's using port 3000
lsof -i :3000

# Kill the process if needed
kill <PID>
```

### Silent Re-Authentication Issues

If silent re-authentication consistently fails or causes issues:

**Symptom:** Browser opens but immediately falls back to login page

This is expected when your IdP session has expired. The IdP returns `login_required` and muster falls back to interactive login.

**Symptom:** Browser opens twice during login

This happens when silent auth is enabled via `--silent` but fails (e.g., IdP session expired), causing muster to retry with interactive auth. Silent auth is disabled by default to prevent this.

If you're using `--silent` and experience this issue, your IdP may not support `prompt=none`. See the [Known Limitations](#silent-re-auth-known-limitations) section below.

**Solution:** Simply don't use the `--silent` flag:

```bash
muster auth login  # Uses interactive auth (default)
```

### Silent Re-Auth Known Limitations

Silent re-authentication using OIDC `prompt=none` requires IdP support. Some IdPs do not fully support this feature:

#### Dex

**Silent re-authentication does not work with Dex.** This is a known architectural limitation:

- **Dex doesn't maintain browser sessions**: Unlike direct OIDC providers (Azure AD, Google, etc.), Dex acts as a federation layer and doesn't maintain its own session state between requests.
- **Dex ignores `prompt=none`**: Current Dex versions do not honor the `prompt=none` parameter. Instead of returning `login_required` error or tokens silently, Dex shows its login UI.
- **Open feature requests**: See [dexidp/dex#990](https://github.com/dexidp/dex/issues/990), [dexidp/dex#4325](https://github.com/dexidp/dex/pull/4325), and [dexidp/dex#4086](https://github.com/dexidp/dex/pull/4086) for ongoing work.

**Impact:** When using Dex (common in Kubernetes environments), you will always need to click in the browser to select your account or confirm login, even if you recently authenticated.

**Workaround:** If your Dex instance uses an OIDC connector (e.g., Azure AD, Google), the upstream IdP may have an active session. While Dex still requires a click, the upstream IdP won't require re-entering credentials if its session is valid.

#### Supported IdPs

Silent re-authentication works with IdPs that properly support OIDC `prompt=none`:

- **Azure AD / Entra ID**: Full support
- **Google Identity Platform**: Full support
- **Okta**: Full support
- **Auth0**: Full support
- **Keycloak**: Supported (with session management enabled)

### Token Expired

Access tokens expire after 30 minutes, but are automatically refreshed in the background
using the refresh token. Sessions last approximately 30 days (configurable via
`oauth.server.sessionDuration`). After the session expires, you'll need to re-authenticate.

The status command shows both access token and session expiry:

```
Expires:   expired 2 minutes ago
Session:   ~29 days remaining (auto-refresh)
```

- **Expires** shows the current access token's remaining lifetime. Access tokens are
  short-lived (30 minutes) and refreshed automatically -- an expired access token does
  not require re-authentication.
- **Session** shows an approximate estimate of how long your session remains active
  before re-authentication is required. This is based on the configured session duration
  (default: 30 days).

If the session has also expired, re-authenticate:

```bash
muster auth login --endpoint https://muster.example.com/mcp
```

> **Note:** Muster uses a **rolling** refresh token TTL (reset on each token rotation),
> while Dex's `absoluteLifetime` is an **absolute** limit measured from original issuance
> that does not reset. The default session duration (30 days) is aligned with Dex's
> default `absoluteLifetime` (720h). If your Dex instance has a different
> `absoluteLifetime`, the actual session will end when Dex's absolute lifetime expires,
> even if muster's estimate shows more time remaining.
>
> For the full token lifecycle and how muster and Dex tokens interact, see the
> [Security Configuration](../../operations/security.md#token-lifecycle) guide.

### Network Issues

Ensure you can reach both:
- The Muster aggregator endpoint
- The OAuth identity provider (issuer URL)

```bash
# Test connectivity
curl -I https://muster.example.com/mcp
curl -I https://dex.example.com/.well-known/openid-configuration
```

## Exit Codes

Muster uses standard exit codes for scripting:

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | General error (command failed, invalid arguments) |
| `2` | Authentication required (use `muster auth login`) |
| `3` | Authentication failed (OAuth flow failed) |

Example scripting usage:

```bash
muster list service --endpoint https://muster.example.com/mcp --auth=none
case $? in
  0) echo "Success" ;;
  2) echo "Auth required - running login..."; muster auth login ;;
  *) echo "Error" ;;
esac
```

## Security Considerations

- Tokens are stored with restrictive file permissions (`0600`)
- Access tokens are short-lived (30 minutes), capped by `capTokenExpiry` to never exceed
  the provider's token lifetime, and refreshed automatically
- Refresh tokens enable automatic session renewal (session duration: ~30 days, aligned
  with Dex's `absoluteLifetime`)
- Token values are never logged (only metadata)
- All OAuth communication uses HTTPS in production
- PKCE (Proof Key for Code Exchange) protects against authorization code interception
- State parameter validation prevents CSRF attacks

For the full token lifecycle architecture, see the
[Security Configuration](../../operations/security.md#token-lifecycle) guide.

### Silent Re-Authentication Security

Silent re-authentication (`prompt=none`) is secure because:

- **PKCE is still enforced**: Every silent auth flow uses a unique code verifier/challenge pair
- **State validation**: CSRF protection is maintained for silent flows
- **IdP validation**: The Identity Provider validates the ID token hint and session
- **Shorter timeout**: Silent auth uses a 15-second timeout (vs standard callback timeout)
- **Graceful degradation**: Any failure falls back to full interactive authentication

The stored ID token is only used to provide `login_hint` and `id_token_hint` parameters to the IdP. These are hints that help the IdP identify the user - the IdP performs all actual authentication and validation.

## Related Commands

- **[agent](agent.md)** - Connect to aggregator as MCP client
- **[list](list.md)** - List resources (may require auth)
- **[get](get.md)** - Get resource details (may require auth)
- **[serve](serve.md)** - Start aggregator server (can enable OAuth)
