# 009. Single Sign-On via Token Forwarding

## Status

Proposed

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

Muster exchanges its access token for a new token scoped to the downstream service.

**Flow:**
1. User authenticates to Muster (receives token with `aud: muster-client`)
2. Muster calls the IdP's token exchange endpoint
3. IdP issues a new token with `aud: mcp-kubernetes-client`
4. Muster uses this new token to call mcp-kubernetes

**Advantages:**
- Proper audience separation
- Fine-grained scope control per downstream service
- No need to modify downstream server trust configuration

**Disadvantages:**
- Requires IdP support for token exchange (Dex does not fully support RFC 8693)
- More complex implementation
- Additional network round-trip per downstream server

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

When defining MCP servers that support SSO via token forwarding:

```yaml
# MCPServer CRD with SSO enabled
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
    # Fallback: if token forwarding fails, offer separate auth
    fallbackToOwnAuth: true
```

When `forwardToken: true`:
1. Muster injects the user's ID token into requests to this server
2. The server validates the token with its multi-audience configuration
3. No separate authentication flow is required

When `fallbackToOwnAuth: true`:
1. If token forwarding fails (wrong audience, expired, etc.)
2. Muster triggers a separate OAuth flow for this specific server
3. Provides graceful degradation for misconfigured deployments

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
   → User authenticates with Google
   → Muster receives tokens (access + ID + refresh)
   → User is now authenticated to Muster

3. User requests tool from mcp-kubernetes
   → Muster checks: forwardToken=true for mcp-kubernetes
   → Muster injects ID token into request headers
   → mcp-kubernetes validates token:
       - Issuer: Google (trusted)
       - Audience: muster-client (in trustedAudiences)
       - Signature: Valid
   → Request succeeds with user identity from token

4. User requests tool from inboxfewer
   → Same flow as step 3
   → No additional authentication required

5. Token nearing expiry (within 5 minutes)
   → Agent proactively refreshes token
   → New access/ID tokens replace old ones
   → All downstream requests continue working

Result: User authenticated ONCE, uses tools from ALL servers.
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
       fallbackToOwnAuth: true  # If forwarding fails, trigger separate auth
   ```

2. Modify OAuth proxy (`internal/oauth/manager.go`) to:
   - Check if `forwardToken` is enabled for the target server
   - Extract the user's ID token from the session store
   - Inject it as `Authorization: Bearer <id_token>` when calling the downstream server

3. Handle fallback gracefully:
   - If downstream returns 401 despite forwarded token, clear cached auth and trigger normal flow

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
3. Add CLI commands for forced refresh (`muster auth refresh`)

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

**Rejected because**: Limited IdP support (Dex doesn't fully implement it), more complex, additional latency.

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
