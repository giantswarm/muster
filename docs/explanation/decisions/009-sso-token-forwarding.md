# 009. Single Sign-On via Token Forwarding

## Status

Accepted

## Implementation Status

The core SSO token forwarding mechanism is implemented. Key implementation details:

### Proactive SSO Session Initialization

When muster's OAuth server is enabled (`aggregator.oauthServer.enabled: true`), proactive SSO connections are established automatically during session initialization.

**Flow:**
1. User authenticates to muster via `muster auth login`
2. User's ID token is stored in muster's token store
3. On first authenticated MCP request (any tool/resource call), the **Session Init Callback** triggers
4. Muster proactively connects to all SSO-enabled servers (`forwardToken: true`) using the user's ID token
5. Subsequent `auth://status` reads show SSO servers as "connected" without additional authentication

**Key Components:**
- `SessionInitCallback` (`internal/api/oauth.go`): Callback type for session initialization
- `handleSessionInit` (`internal/aggregator/auth_resource.go`): Establishes proactive SSO connections
- `triggerSessionInitIfNeeded` (`internal/server/oauth_http.go`): Triggers callback on first authenticated request

**Why Session Init, Not Auth Status Read:**
The proactive SSO logic runs during session initialization (first authenticated MCP request), not when reading `auth://status`. This ensures:
- `auth://status` is a pure read operation without side effects
- SSO connections are established exactly once per session
- The user experience is seamless: after `muster auth login`, SSO servers are immediately connected

### Session-Specific Connection Detection

Before attempting authentication, `handleAuthLogin` checks if the session already has a connection to the target server. This prevents redundant authentication attempts and ensures that proactively established SSO connections are recognized.

```go
// Check if this session already has a connection to this server
if p.aggregator.sessionRegistry != nil {
    if conn, exists := p.aggregator.sessionRegistry.GetConnection(sessionID, serverName); exists {
        return "Server 'X' is already authenticated for this session."
    }
}
```

## Context

### The Problem

When users connect through the Muster architecture, they face a frustrating authentication experience:

1. **Triple Authentication**: Users must authenticate separately to:
   - Muster Server itself
   - Each remote MCP server (e.g., mcp-kubernetes)
   - Each additional remote MCP server (e.g., inboxfewer)

2. **Short Token Lifetime**: Default OAuth access tokens expire after 1 hour, requiring frequent re-authentication. While refresh tokens help, many IdPs have session limits.

3. **No Token Sharing**: Even when all components use the same Identity Provider (e.g., Google, Dex), each is a separate OAuth client, requiring separate authentication flows.

### Current Architecture

```
┌─────────────┐     ┌──────────────────┐     ┌───────────────────┐
│   User      │     │  Muster Server   │     │  mcp-kubernetes   │
│   (Agent)   │────▶│  (OAuth Client)  │────▶│  (OAuth Client)   │
│             │     │                  │     │                   │
│  Auth #1    │     │  Token: T1       │     │  Token: T2        │
│  (to Muster)│     │  Issuer: Google  │     │  Issuer: Google   │
└─────────────┘     └──────────────────┘     └───────────────────┘
                            │
                            │                 ┌───────────────────┐
                            └────────────────▶│    inboxfewer     │
                                              │  (OAuth Client)   │
                                              │                   │
                                              │  Token: T3        │
                                              │  Issuer: Google   │
                                              └───────────────────┘

Problem: T1, T2, T3 are all separate tokens even though they're from the same IdP.
User must complete 3 separate OAuth flows.
```

### Why This Happens

1. **Separate OAuth Clients**: Each service registers as a separate OAuth client with its own `client_id`. IdPs issue tokens scoped to specific clients.

2. **Token Validation**: Each service validates that incoming tokens:
   - Are issued by a trusted IdP
   - Have the correct `aud` (audience) claim matching their `client_id`
   - Have required scopes

3. **No Trust Relationship**: Even though all services trust the same IdP, they don't trust tokens issued to *other* clients.

### Technical Analysis

After reviewing the codebase of the relevant projects:

#### mcp-oauth Library

The `mcp-oauth` library provides OAuth 2.1 server functionality used by both muster and mcp-kubernetes. Key observations:

- **Token Validation**: Uses providers (Dex, Google, GitHub) to validate tokens via userinfo endpoints
- **RFC 8707 Support**: Has `ResourceIdentifier` for token audience binding
- **No Multi-Audience**: Currently validates tokens against a single `client_id` - no support for accepting tokens issued to different clients

#### mcp-kubernetes

- Uses `mcp-oauth` for OAuth protection
- Extracts and forwards ID tokens to Kubernetes API for OIDC authentication
- Has `OAuthConfig` struct with extensive security configuration
- No current support for accepting forwarded tokens from other OAuth clients

#### muster

- Uses `mcp-oauth` for both server protection (ADR-005) and OAuth proxy (ADR-004)
- OAuth proxy stores tokens indexed by `(SessionID, Issuer, Scope)` for SSO within Muster
- ID tokens are available in the token store for potential forwarding

## Decision

We will implement **Token Forwarding with Trusted Relay** - a pattern where Muster acts as a trusted intermediary, forwarding its user tokens to downstream MCP servers that are configured to accept them.

### Core Principle

**Muster becomes the single point of authentication for the entire MCP ecosystem.**

Users authenticate once to Muster. Downstream MCP servers trust tokens presented by Muster because they:
1. Trust the same IdP (issuer)
2. Are configured to accept tokens from the Muster client

### Architecture

```
┌─────────────┐     ┌──────────────────────────────────────────────┐
│   User      │     │                Muster Server                  │
│   (Agent)   │     │                                               │
│             │────▶│  OAuth Middleware validates user token        │
│  Auth #1    │     │                                               │
│  (once!)    │     │  User token: { aud: muster-client, ... }      │
│             │     │                                               │
└─────────────┘     │  ┌─────────────────────────────────────────┐  │
                    │  │           Token Forwarder               │  │
                    │  │                                         │  │
                    │  │  Forwards user token (or ID token) to   │  │
                    │  │  downstream MCP servers                  │  │
                    │  └─────────────────────────────────────────┘  │
                    │                    │                          │
                    └────────────────────┼──────────────────────────┘
                                         │
                    ┌────────────────────┴────────────────────┐
                    ▼                                         ▼
            ┌───────────────────┐                  ┌───────────────────┐
            │  mcp-kubernetes   │                  │    inboxfewer     │
            │                   │                  │                   │
            │  Accepts tokens   │                  │  Accepts tokens   │
            │  with audience:   │                  │  with audience:   │
            │  - self           │                  │  - self           │
            │  - muster-client  │                  │  - muster-client  │
            └───────────────────┘                  └───────────────────┘

User authenticates ONCE. Muster forwards the token to all downstream servers.
```

### Token Forwarding Strategies

We propose two strategies, applicable depending on deployment configuration:

#### Strategy 1: ID Token Forwarding (Recommended)

When the user authenticates to Muster, the IdP issues:
- **Access Token**: Used to call Muster's API
- **ID Token**: A signed JWT containing user identity claims

Muster extracts the **ID Token** and forwards it to downstream MCP servers.

**Advantages:**
- ID tokens contain user identity (`sub`, `email`, `groups`)
- Downstream servers can make authorization decisions based on user identity
- ID tokens are typically long-lived (or can be reissued via refresh)

**Configuration:**
```yaml
# Downstream MCP server configuration (mcp-kubernetes, inboxfewer)
oauth:
  enabled: true
  provider: dex  # or google
  # Accept ID tokens from the muster client in addition to direct clients
  trustedIssuers:
    - issuer: "https://dex.example.com"
      audiences:
        - "mcp-kubernetes-client"    # Direct authentication
        - "muster-client"            # Forwarded from Muster
```

#### Strategy 2: Token Exchange (OAuth 2.0 Token Exchange - RFC 8693)

Muster exchanges its ID token for a new token from the remote cluster's Identity Provider.

**Flow:**
1. User authenticates to Muster (receives token from Cluster A's Dex)
2. Muster calls the remote cluster's Dex token exchange endpoint
3. Remote Dex validates the token via its OIDC connector for Cluster A
4. Remote Dex issues a new token with `iss: remote-dex`
5. Muster uses this new token to call the MCP server on the remote cluster

**Advantages:**
- Proper audience separation
- Token is issued by the remote cluster's IdP (not just forwarded)
- Works with separate Dex instances per cluster
- Scopes and groups can be mapped during exchange

**Disadvantages:**
- Requires remote Dex to have an OIDC connector configured for the local cluster's Dex
- Additional network round-trip for token exchange (cached to reduce impact)
- More complex configuration

**Implemented Status:** Fully implemented in muster v0.X.X

### Recommended Approach: ID Token Forwarding

Given that:
1. Many IdPs (including Dex) have limited token exchange support
2. ID tokens are already issued during OIDC authentication
3. ID tokens contain all necessary user identity information

We recommend **ID Token Forwarding** with multi-audience trust.

### Implementation Details

#### 1. Muster Server: Extract and Store ID Token

The Muster Server already stores the ID token from authentication (via mcp-oauth library). We extend the OAuth proxy to inject this token for downstream calls.

```go
// internal/oauth/forwarder.go

type TokenForwarder struct {
    tokenStore *TokenStore
}

// ForwardTokenForSession retrieves the user's ID token and prepares it for downstream use.
func (f *TokenForwarder) ForwardTokenForSession(sessionID string) (string, error) {
    // Get the user's token for this session
    token := f.tokenStore.GetBySession(sessionID)
    if token == nil {
        return "", fmt.Errorf("no token for session")
    }
    
    // Prefer ID token for forwarding (contains user identity)
    if token.IDToken != "" {
        return token.IDToken, nil
    }
    
    // Fallback to access token if no ID token (less ideal)
    return token.AccessToken, nil
}
```

#### 2. Downstream MCP Server: Accept Forwarded Tokens

Each downstream MCP server (mcp-kubernetes, inboxfewer, etc.) must be configured to accept tokens with multiple audiences:

```go
// Token validation configuration (mcp-oauth integration)
type OAuthConfig struct {
    // Primary audience (direct authentication)
    ClientID string
    
    // Additional trusted audiences (forwarded tokens)
    TrustedAudiences []string
    
    // Trusted issuers
    TrustedIssuers []string
}

func (c *OAuthConfig) ValidateToken(token string) (*Claims, error) {
    claims, err := parseJWT(token)
    if err != nil {
        return nil, err
    }
    
    // Validate issuer
    if !contains(c.TrustedIssuers, claims.Issuer) {
        return nil, fmt.Errorf("untrusted issuer: %s", claims.Issuer)
    }
    
    // Validate audience (allow primary OR trusted forwarded audiences)
    allAudiences := append([]string{c.ClientID}, c.TrustedAudiences...)
    if !containsAny(allAudiences, claims.Audience) {
        return nil, fmt.Errorf("invalid audience")
    }
    
    return claims, nil
}
```

#### 3. Configuration for SSO-Enabled Deployment

**Muster Server (Helm values):**
```yaml
aggregator:
  oauth:
    enabled: true
    publicUrl: "https://muster.example.com"
    provider: "dex"
    dex:
      issuerUrl: "https://dex.example.com"
      clientId: "muster-client"
    # Enable token forwarding to downstream MCP servers
    forwardIdToken: true
```

**Downstream MCP Server (mcp-kubernetes):**
```yaml
oauth:
  enabled: true
  provider: "dex"
  dex:
    issuerUrl: "https://dex.example.com"
    clientId: "mcp-kubernetes-client"
  # Trust tokens forwarded from Muster
  trustedAudiences:
    - "muster-client"
```

### Longer Token Lifetime

To address the 1-hour expiry issue, we recommend:

#### 1. Configure IdP for Longer Token Lifetimes

**Dex example:**
```yaml
expiry:
  signingKeys: "6h"
  idTokens: "24h"       # Extend ID token lifetime
  refreshTokens:
    validIfNotUsedFor: "168h"  # 7 days
    absoluteLifetime: "720h"    # 30 days
```

**Google OAuth:**
- Access tokens: Fixed 1-hour lifetime (cannot be changed)
- Refresh tokens: Long-lived (until revoked)
- ID tokens: Same as access tokens

For Google, the solution is aggressive refresh token usage (already implemented).

#### 2. Agent-Side Token Persistence

The Muster Agent already supports persistent token storage (`~/.config/muster/tokens/`). We enhance this to:

1. **Prefer refresh tokens**: Store and reuse refresh tokens across sessions
2. **Proactive refresh**: Refresh tokens before they expire (already implemented with 5-minute threshold)
3. **Cross-session persistence**: Tokens survive agent restarts

```go
// Already implemented in internal/agent/oauth/token_store.go
type StoredToken struct {
    AccessToken  string    `json:"access_token"`
    RefreshToken string    `json:"refresh_token,omitempty"`  // Persisted!
    Expiry       time.Time `json:"expiry,omitempty"`
    // ...
}
```

#### 3. Aggregator-Side Session Persistence (Future)

For the aggregator (server-side), tokens are currently in-memory and lost on restart. Future enhancement:

- Use Valkey/Redis for persistent token storage
- Tokens survive server restarts
- Users don't need to re-authenticate after deployments

### MCPServer Configuration for SSO

Muster supports two SSO mechanisms, configured per MCPServer:

#### 1. Token Forwarding (Recommended for Same-Cluster SSO)

When the downstream MCP server trusts muster's OAuth client ID:

```yaml
# MCPServer CRD with SSO via token forwarding
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: mcp-kubernetes
spec:
  type: streamable-http
  url: https://mcp-kubernetes.example.com/mcp
  auth:
    type: oauth
    # Forward Muster's ID token instead of requiring separate auth
    forwardToken: true
    # Required audiences for the forwarded token
    # For Kubernetes OIDC auth, typically needs "dex-k8s-authenticator"
    requiredAudiences:
      - "dex-k8s-authenticator"
```

When `forwardToken: true`:
1. Muster requests tokens with the specified `requiredAudiences` from Dex via cross-client scopes
2. Muster injects the user's multi-audience ID token into requests to this server
3. The server validates the token with its audience configuration
4. No separate authentication flow is required

#### 2. Token Exchange (Recommended for Cross-Cluster SSO)

When the downstream MCP server is on a different cluster with its own Dex instance:

```yaml
# MCPServer CRD with SSO via RFC 8693 token exchange
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-mcp-kubernetes
spec:
  type: streamable-http
  url: https://mcp-kubernetes.cluster-b.example.com/mcp
  auth:
    type: oauth
    # RFC 8693 Token Exchange for cross-cluster SSO
    tokenExchange:
      enabled: true
      # Remote cluster's Dex token endpoint
      dexTokenEndpoint: https://dex.cluster-b.example.com/token
      # The connector ID on remote Dex that trusts this cluster's Dex
      connectorId: cluster-a-dex
      # Optional: scopes to request (defaults to "openid profile email groups")
      scopes: "openid profile email groups"
```

When `tokenExchange.enabled: true`:
1. Muster extracts the user's ID token from the session
2. Muster calls the remote Dex's token exchange endpoint
3. Remote Dex validates the token via its OIDC connector
4. Remote Dex issues a new token valid for that cluster
5. Muster uses the exchanged token to connect to the MCP server

**Remote Dex Configuration Required:**
```yaml
# On remote cluster's Dex (cluster-b)
connectors:
- type: oidc
  id: cluster-a-dex   # Must match connectorId in MCPServer
  name: "Cluster A"
  config:
    issuer: https://dex.cluster-a.example.com
    getUserInfo: true
    insecureEnableGroups: true
```

Token exchange takes precedence over token forwarding if both are configured.

### SSO Detection

Two functions determine which SSO mechanism to use for a server:

```go
// Token exchange takes precedence over token forwarding
func ShouldUseTokenExchange(serverInfo *ServerInfo) bool {
    if serverInfo == nil || serverInfo.AuthConfig == nil || serverInfo.AuthConfig.TokenExchange == nil {
        return false
    }
    config := serverInfo.AuthConfig.TokenExchange
    return config.Enabled && config.DexTokenEndpoint != "" && config.ConnectorID != ""
}

func ShouldUseTokenForwarding(serverInfo *ServerInfo) bool {
    if serverInfo == nil || serverInfo.AuthConfig == nil {
        return false
    }
    // ForwardToken implies OAuth-based authentication
    return serverInfo.AuthConfig.ForwardToken
}
```

**SSO Precedence Order:**
1. Token Exchange (if `tokenExchange.enabled: true` with required fields)
2. Token Forwarding (if `forwardToken: true`)
3. Server-specific OAuth flow (for servers without SSO configuration)

Setting `forwardToken: true` or `tokenExchange.enabled: true` implicitly enables OAuth-based authentication since these SSO mechanisms only make sense in an OAuth context.

### Security Considerations

#### 1. Trusted Relay Pattern

This pattern is safe because:
- Downstream servers explicitly configure which client IDs they trust
- The IdP signature on the ID token proves the user's identity
- No token modification or impersonation is possible

#### 2. Scope Preservation

ID tokens don't carry scopes. For scope-based authorization:
- Downstream servers must validate scopes separately
- Or use access token forwarding with scope validation
- Or implement token exchange for scope delegation

#### 3. Token Revocation

When a user's session is revoked:
- Agent-side: Delete stored tokens
- Server-side: Session cleanup removes tokens from memory
- IdP-side: Revoked refresh tokens will fail to renew

### Example: Complete SSO Flow

```
1. User starts Cursor with Muster Agent

2. Agent connects to Muster Server
   → Muster returns 401 with auth challenge
   → Agent opens browser for OAuth flow
   → User authenticates with Google/Dex
   → Muster receives tokens (access + ID + refresh)
   → User is now authenticated to Muster

3. First MCP request (e.g., list_tools, read auth://status, call any tool)
   → Session Init Callback triggers (first authenticated request for this session)
   → Muster finds all SSO-enabled servers (forwardToken: true)
   → For each SSO server:
       → Muster extracts user's ID token from session store
       → Muster establishes connection using ID token forwarding
       → mcp-kubernetes validates token:
           - Issuer: Google/Dex (trusted)
           - Audience: muster-client (in trustedAudiences)
           - Signature: Valid
       → Session connection established with session-specific tools
   → SSO servers now show as "connected" in auth://status

4. User requests tool from mcp-kubernetes
   → Session already has connection (established in step 3)
   → Request uses existing session connection
   → No additional authentication required

5. User explicitly calls core_auth_login for SSO server
   → Muster detects existing session connection
   → Returns "Server 'X' is already authenticated for this session"
   → No duplicate authentication

6. Token nearing expiry (within 5 minutes)
   → Agent proactively refreshes token
   → New access/ID tokens replace old ones
   → All downstream requests continue working

Result: User authenticated ONCE, SSO servers connected AUTOMATICALLY on first request.
```

## Implementation Steps

### Phase 1: mcp-oauth Library Changes (giantswarm/mcp-oauth)

**Issue: Add TrustedAudiences Support for SSO Token Forwarding**

The mcp-oauth library needs to support accepting tokens with multiple valid audiences. This enables downstream MCP servers to accept tokens originally issued to muster.

Required changes to `server/config.go`:

```go
type Config struct {
    // ... existing fields ...

    // TrustedAudiences lists additional OAuth client IDs whose tokens are accepted.
    // This enables SSO where tokens issued to a trusted upstream (e.g., muster)
    // are accepted by this resource server.
    // The server's own ClientID is always implicitly trusted.
    // Example: ["muster-client"] accepts tokens issued to muster
    TrustedAudiences []string
}
```

Required changes to token validation:
1. After provider validates the token (via userinfo), extract the `aud` claim from ID token
2. If `aud` matches the server's own client ID, accept (current behavior)
3. If `aud` matches any entry in `TrustedAudiences`, accept (new behavior)
4. Otherwise, reject with `invalid_token` error

### Phase 2: muster Changes (giantswarm/muster)

**Issue: Implement ID Token Forwarding for SSO**

1. Add `forwardToken` option to MCPServer CRD spec:
   ```yaml
   spec:
     auth:
       type: oauth
       forwardToken: true    # Forward Muster's ID token
   ```

2. Modify OAuth proxy (`internal/oauth/manager.go`) to:
   - Check if `forwardToken` is enabled for the target server
   - Extract the user's ID token from the session store
   - Inject it as `Authorization: Bearer <id_token>` when calling the downstream server

### Phase 3: mcp-kubernetes Changes (giantswarm/mcp-kubernetes)

**Issue: Accept Forwarded Tokens from Trusted Aggregators**

1. Add configuration for trusted audiences:
   ```go
   type OAuthConfig struct {
       // ... existing fields ...
       
       // TrustedAudiences lists client IDs whose tokens are accepted for SSO
       TrustedAudiences []string
   }
   ```

2. Pass `TrustedAudiences` to mcp-oauth's `server.Config`

3. Document SSO configuration in Helm chart

### Phase 4: Documentation and IdP Configuration

1. Document IdP configuration for longer token lifetimes (Dex, Google)
2. Create SSO deployment guide with example configurations
3. Add troubleshooting guide for SSO issues

### Phase 5: Enhanced Agent Experience

1. Improve token persistence reliability
2. Add CLI commands for token status (`muster auth status` - already exists)

## Security Considerations

### 1. ID Token vs Access Token

- ID tokens are designed for authentication and contain user identity claims
- They are self-contained JWTs that can be validated without calling the IdP
- Access tokens require userinfo endpoint calls for validation
- Forwarding ID tokens is preferred because downstream servers can validate them locally

### 2. Cross-Client Trust Model

- Explicit configuration required via `TrustedAudiences` - no implicit trust
- Each downstream server explicitly lists which client IDs it will accept tokens from
- The IdP signature on the token proves authenticity - no modification is possible
- Compromising one OAuth client doesn't automatically compromise others

### 3. Audit Logging

- mcp-oauth should log when a token is accepted via `TrustedAudiences` (cross-client)
- New audit event type: `EventCrossClientTokenAccepted`
- Logs should include: original audience, accepting server, user identity (hashed)

### 4. Token Exposure Surface

- ID tokens flow through Muster, which must be trusted
- TLS is mandatory for all communication paths
- Tokens are never logged in plaintext (only hashed identifiers)
- Token storage uses encryption at rest when configured

### 5. Scope Handling

- ID tokens don't carry OAuth scopes - they contain identity claims
- Downstream servers must implement their own authorization based on user identity
- For scope-based authorization, consider access token forwarding instead
- Document scope requirements for each downstream service

### 6. Token Lifetime and Refresh

- Forwarded tokens inherit the original token's expiry
- Muster proactively refreshes tokens before expiry (5-minute threshold)
- Downstream servers honor the token's expiry, don't issue their own
- Refresh token rotation is handled by mcp-oauth

### 7. Issuer Validation

- Both muster and downstream MCPs MUST validate the same issuer
- Cross-issuer token forwarding is NOT supported
- Each deployment should use a single IdP for the entire ecosystem

### 8. Revocation

When a user's session is revoked:
- Agent-side: Delete stored tokens from `~/.config/muster/tokens/`
- Server-side: Session cleanup removes tokens from memory/Valkey
- IdP-side: Revoked refresh tokens fail to renew, cascading revocation

## Consequences

### Positive

- **Single Sign-On**: Users authenticate once, access all MCP servers
- **Better UX**: No more "please authenticate to X" interruptions
- **Simpler Mental Model**: "Log in to Muster, use everything"
- **Security Preserved**: IdP signatures ensure token authenticity

### Negative

- **Trust Configuration Required**: Downstream servers must be configured to trust Muster's client ID
- **ID Token Limitations**: ID tokens don't carry scopes; scope-based authz needs extra work
- **Deployment Complexity**: SSO requires coordinated configuration across services

### Trade-offs

| Aspect | Without SSO | With SSO |
|--------|-------------|----------|
| User Experience | 3 auth flows | 1 auth flow |
| Configuration | Each server independent | Coordinated trust config |
| Security Model | Per-service tokens | Shared identity token |
| Scope Granularity | Per-service scopes | User identity only |

## Alternatives Considered

### 1. OAuth 2.0 Token Exchange (RFC 8693)

**Now Implemented**: Initially rejected due to limited IdP support. However, Dex has since implemented token exchange (enabled by default), and the mcp-oauth library now includes a `TokenExchangeClient`. Token exchange is now available as a second SSO mechanism alongside token forwarding, recommended for cross-cluster SSO scenarios where clusters have separate Dex instances.

### 2. Shared OAuth Client

All services use the same OAuth client ID.

**Rejected because**: Violates OAuth best practices, no per-service authorization, single point of compromise.

### 3. Proxy-Based Authentication

Muster authenticates to each downstream service with service credentials, not user identity.

**Rejected because**: Loses user identity, prevents per-user authorization, audit trail breaks.

## Related Decisions

- [ADR-004: OAuth Proxy](004-oauth-proxy.md) - Foundation for downstream auth
- [ADR-005: Muster Auth](005-muster-auth.md) - Agent-side OAuth implementation
- [ADR-006: Session-Scoped Tool Visibility](006-session-scoped-tool-visibility.md) - Per-user tool access
- [ADR-008: Explicit Authentication State](008-unified-authentication.md) - Auth status communication
