# muster create

Create a resource

## Synopsis

Create a resource in the muster environment.

Available resource types:
  workflow      - Create a Workflow definition
  mcpserver     - Create an MCP server definition (stdio, streamable-http, or sse)

Examples:
  muster create workflow example-workflow
  muster create mcpserver my-stdio-server --type=stdio --command=npx --args="@modelcontextprotocol/server-git" --autoStart=true
  muster create mcpserver my-http-server --type=streamable-http --url=https://api.example.com/mcp --timeout=30
  muster create mcpserver my-sse-server --type=sse --url=https://sse.example.com/mcp --timeout=60

Authentication (remote server types only):
  --auth-type oauth                    Enable OAuth 2.0/OIDC authentication
  --auth-issuer <url>                  Pin the authorization server issuer for servers
                                       without RFC 9728 metadata (requires --auth-type=oauth)
  --auth-scopes "<scopes>"             OAuth scopes for the pinned issuer (space-separated)
  --forward-token                      Forward the session's ID token for SSO (implies oauth)
  --required-audiences <a1,a2>         Extra audiences to request for the forwarded token

  muster create mcpserver my-oauth-server --type=streamable-http --url=https://api.example.com/mcp --auth-type=oauth
  muster create mcpserver my-pinned-server --type=streamable-http --url=https://api.example.com/mcp --auth-type=oauth --auth-issuer=https://auth.example.com --auth-scopes="openid profile"
  muster create mcpserver my-sso-server --type=streamable-http --url=https://mcp.example.com/mcp --forward-token --required-audiences=dex-k8s-authenticator

Note: The aggregator server must be running (use 'muster serve') before using these commands.

```
muster create
```

## Options

```
      --auth string          Authentication mode: none (default: fail with auth_required), prompt, or auto (env: MUSTER_AUTH_MODE)
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --debug                Enable debug logging (show MCP protocol messages)
      --endpoint string      Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
  -h, --help                 help for create
      --login                Open the browser to sign in when authentication is required (same as --auth auto)
      --no-headers           Suppress header row in table output
  -o, --output string        Output format (table, wide, json, yaml) (default "table")
  -q, --quiet                Suppress non-essential output
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
