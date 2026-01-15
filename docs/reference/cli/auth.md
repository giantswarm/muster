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
  - Note: Not yet implemented
- `--all`: Authenticate to aggregator and all pending MCP servers
  - Provides SSO-style authentication chain

**Examples:**

```bash
# Login to configured aggregator
muster auth login

# Login to specific remote endpoint
muster auth login --endpoint https://muster.example.com/mcp

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
- `--server` (string): Logout from specific MCP server (not yet implemented)
- `--all`: Clear all stored tokens

**Examples:**

```bash
# Logout from configured aggregator
muster auth logout

# Logout from specific endpoint
muster auth logout --endpoint https://muster.example.com/mcp

# Clear all stored tokens
muster auth logout --all
```

### muster auth status

Show the current authentication status for all known endpoints.

```bash
muster auth status [OPTIONS]
```

**Options:**

- `--endpoint` (string): Show status for specific endpoint
- `--server` (string): Show status for specific MCP server (not yet implemented)

**Output:**

```
┌────────────────────────────────────┬───────────────┬──────────┬────────────────────┐
│ Endpoint                           │ Status        │ Expires  │ Issuer             │
├────────────────────────────────────┼───────────────┼──────────┼────────────────────┤
│ https://muster.example.com/mcp     │ Authenticated │ 23 hours │ https://dex.exam.. │
│ https://other.example.com/mcp      │ Not authenticated │      │                    │
└────────────────────────────────────┴───────────────┴──────────┴────────────────────┘
```

**Examples:**

```bash
# Show all auth status
muster auth status

# Show status for specific endpoint
muster auth status --endpoint https://muster.example.com/mcp
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

## Common Options

These options are available on all auth subcommands:

- `--config-path` (string): Configuration directory
  - Default: `~/.config/muster`

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

# Logout from all
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

## Troubleshooting

### Browser Doesn't Open

If the browser doesn't open automatically:

```
Opening browser for authentication...
If the browser doesn't open, visit:
  https://dex.example.com/auth?...
```

Copy and paste the URL manually into your browser.

### Callback Port in Use

If port 3000 is already in use, the callback server cannot start. Free the port or configure a different callback port.

### Token Expired

Tokens typically expire after 24 hours. If you see authentication errors:

```bash
# Refresh the token
muster auth refresh --endpoint https://muster.example.com/mcp

# Or re-authenticate
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
