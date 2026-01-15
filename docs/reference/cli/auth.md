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
```

**What happens during login:**

1. Muster probes the endpoint to check if OAuth is required
2. If required, discovers OAuth metadata (issuer, authorization endpoint)
3. Opens your browser to the authorization page
4. Waits for you to complete authentication
5. Stores the token securely for future use

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
  Expires:   in 23 hours
  Issuer:    https://dex.example.com

MCP Servers
  (1 pending authentication)
  mcp-kubernetes      Connected
  mcp-github          Not authenticated   Run: muster auth login --server mcp-github
```

**Examples:**

```bash
# Show all auth status
muster auth status

# Show status for specific endpoint
muster auth status --endpoint https://muster.example.com/mcp

# Show status for specific MCP server
muster auth status --server mcp-kubernetes
```

### muster auth refresh

Force a token refresh for an endpoint.

```bash
muster auth refresh [OPTIONS]
```

**Options:**

- `--endpoint` (string): Refresh token for specific endpoint

**Examples:**

```bash
# Refresh token for configured aggregator
muster auth refresh

# Refresh token for specific endpoint
muster auth refresh --endpoint https://muster.example.com/mcp
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
# Shows: Authenticated, Expires in 24 hours

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
# If experiencing auth issues, try refreshing
muster auth refresh --endpoint https://muster.example.com/mcp

# Or re-authenticate completely
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

Free the port by stopping any service using it:

```bash
# Find what's using port 3000
lsof -i :3000

# Kill the process if needed
kill <PID>
```

### Token Expired

Tokens typically expire after 24 hours. The status command shows expiry:

```
Expires:   expired 2 hours ago
```

Re-authenticate:

```bash
muster auth login --endpoint https://muster.example.com/mcp
```

### Network Issues

Ensure you can reach both:
- The Muster aggregator endpoint
- The OAuth identity provider (issuer URL)

```bash
# Test connectivity
curl -I https://muster.example.com/mcp
curl -I https://dex.example.com/.well-known/openid-configuration
```

## Security Considerations

- Tokens are stored with restrictive file permissions
- Access tokens are short-lived (typically 24 hours)
- Refresh tokens enable automatic renewal
- Token values are never logged (only metadata)
- All OAuth communication uses HTTPS in production

## Related Commands

- **[agent](agent.md)** - Connect to aggregator as MCP client
- **[list](list.md)** - List resources (may require auth)
- **[get](get.md)** - Get resource details (may require auth)
- **[serve](serve.md)** - Start aggregator server (can enable OAuth)
