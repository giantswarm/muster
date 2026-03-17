# ADR-011: Session Connection Pool and Auth/Capability Separation

## Status

Accepted

## Context

Prior to this change, every tool call to an OAuth-protected (SSO) MCP server followed an ephemeral pattern:

1. Create a new MCP client
2. Initialize the connection (HTTP handshake)
3. Execute the tool call
4. Close the client

This resulted in **3 HTTP round-trips per tool call** (initialize + tool call + close) instead of 1, adding significant latency to SSO-authenticated tool execution. The overhead was particularly noticeable for token-exchange servers, which also required an RFC 8693 token exchange request before each connection.

Additionally, authentication state and cached capabilities were conflated in a single `CapabilityStore`. This coupling meant that clearing stale capabilities (e.g., on server health transitions or tool-change detection) would accidentally revoke a user's authentication, forcing them to re-authenticate.

### Problems

1. **Latency**: 3x HTTP overhead per SSO tool call due to ephemeral client creation
2. **Auth/capability coupling**: `CapabilityStore.Exists()` was used as both an auth gate ("may this session call tools?") and a capability cache lookup, making it impossible to refresh one without affecting the other
3. **Token expiry handling**: Token-exchange clients embed a fixed-lifetime token in their `headerFunc`. When the token expired while no pool existed, every call would fail until a new client was created
4. **Connection leaks**: Sessions that expired without explicit revocation had no mechanism to clean up their connections

## Decision

### 1. Session Connection Pool

Introduce a `SessionConnectionPool` that maintains live MCP clients keyed by `(sessionID, serverName)`. The pool is **orthogonal** to the `CapabilityStore`: the store caches *what tools exist*; the pool caches *the live connection used to call them*.

```
Tool Call Flow (before):
  → Create client → Initialize → CallTool → Close
  (3 HTTP round-trips)

Tool Call Flow (after):
  → Pool.Get(sessionID, server)
    → Hit:  CallTool directly (1 HTTP round-trip)
    → Miss: Create → Initialize → Pool.Put → CallTool (3 round-trips, amortized once)
```

**Key design choices:**

- **No-op cleanup**: `getOrCreateClientForToolCall` returns a no-op cleanup function. Client lifecycle is managed by the pool, not by individual callers.
- **Background reaper**: A goroutine periodically evicts entries idle longer than `DefaultConnectionPoolMaxAge` (1 hour). Each `Get` resets the idle timer, so actively used connections are never reaped.
- **Close outside lock**: Eviction collects entries under the write lock, then closes them *outside* the lock to avoid holding it during potentially slow network close operations.

### 2. SessionAuthStore -- Separating Auth from Capabilities

Introduce a `SessionAuthStore` interface that tracks per-session, per-server authentication state **independently** from `CapabilityStore`.

```
Before:
  CapabilityStore.Exists(sessionID, server) → "is authenticated AND has cached tools"

After:
  AuthStore.IsAuthenticated(sessionID, server) → "may this session call tools?"
  CapabilityStore.Exists(sessionID, server)    → "do we have cached tool metadata?"
```

This separation enables future capability freshness work (periodic re-fetching of tool lists, health-based tool hiding) without accidentally revoking authentication.

**Auth store characteristics:**

- Interface-driven: `SessionAuthStore` interface with `InMemorySessionAuthStore` implementation
- Dual-check TTL: soft `expireAt` deadline (checked under `RLock` for fast reads) + hard cleanup timer (garbage collects abandoned sessions)
- All auth gates migrated: every `capabilityStore.Exists()` call used as an auth gate is replaced with `authStore.IsAuthenticated()`
- Fail-closed: a nil `authStore` returns an error rather than silently skipping the auth check

### 3. Token Expiry Handling for Token-Exchange Clients

Token-exchange clients bake a fixed-lifetime exchanged token into their `headerFunc`. The pool tracks each entry's `TokenExpiry` to enable two layers of defense:

1. **Proactive refresh**: `getOrCreateClientForToolCall` checks `IsTokenExpiringSoon(margin=30s)`. If the token is about to expire, the stale entry is evicted and a fresh client is created with a re-exchanged token.
2. **Reactive retry**: `callToolWithTokenExchangeRetry` catches 401 errors from `CallTool`, evicts the stale pool entry, creates a fresh client (which re-exchanges), and retries once.

Token-forwarding clients are unaffected because their `headerFunc` dynamically resolves the latest token from the OAuth store on each request.

### 4. SSO Login/Logout Restrictions

SSO servers (token exchange or token forwarding) are connected automatically during `initSSOForSession`. Manual `core_auth_login` and `core_auth_logout` are blocked for these servers with clear error messages, since their connection lifecycle is managed by the platform.

### 5. Complete Eviction Coverage

Every path that invalidates session or server state now covers all three stores:

| Event | AuthStore | CapabilityStore | ConnPool |
|-------|-----------|-----------------|----------|
| Per-server logout | `Revoke` | `DeleteEntry` | `Evict` |
| Session revocation | `RevokeSession` | `Delete` | `EvictSession` |
| Server deregistration | `RevokeServer` | `DeleteServer` | `EvictServer` |
| User token deletion | `RevokeSession` (per sid) | `Delete` (per sid) | `EvictSession` (per sid) |
| Graceful shutdown | `Stop` | `Stop` | `Stop` + `DrainAll` |
| Idle timeout | TTL timer | TTL timer | Reaper goroutine |

### 6. Dead Code Removal

Removed unreachable code identified by analysis:

- `RedactedToken` type (no constructor, zero callers)
- `GetUpstreamAccessTokenFromContext` / `ContextWithUpstreamAccessToken` (context value written but never read)
- `ParseWWWAuthenticateFromResponse` (only called by its own test)
- `LogLevel.String()` (never called through `fmt.Stringer`)

### 7. Valkey-Backed Session Stores

`ValkeyCapabilityStore` and `ValkeySessionAuthStore` are wired into `NewAggregatorServer` via the existing `OAuthServerConfig.Storage` configuration. When `storage.type: valkey` is set, both stores share a single `valkey.Client` created from the same `ValkeyConfig` used by the mcp-oauth token store. In-memory stores remain the default for dev/test. The shared Valkey client is closed during `Stop()`.

### 8. Valkey-Backed OAuth Token and State Stores

The OAuth proxy's `TokenStore` (downstream OAuth access/refresh tokens) and `StateStore` (transient CSRF state parameters) are also Valkey-backed when `storage.type: valkey` is configured. This closes a gap where auth/capability state was shared across pods via Valkey, but the actual tokens needed to call downstream servers were stuck in a single pod's memory.

**Interfaces:** `TokenStorer` and `StateStorer` interfaces are defined in `internal/oauth/` alongside the existing in-memory implementations. This follows the same pattern as `SessionAuthStore` and `CapabilityStore`.

**Valkey data model:**

- `ValkeyTokenStore`: Per-session hash at `muster:oauth:token:{sessionID}` with fields `{issuer}|{scope}` → JSON token entry. A reverse index at `muster:oauth:token:user:{userID}` (Set of session IDs) enables `DeleteByUser` for "sign out everywhere". Session keys get a 30-day TTL; token expiry is checked client-side.
- `ValkeyStateStore`: Per-nonce key at `muster:oauth:state:{nonce}` → JSON state entry with a 10-minute TTL. `GETDEL` provides atomic consume-on-validate semantics.

**Wiring:** `NewAggregatorManager` retrieves the shared `valkey.Client` from the `AggregatorServer` and injects Valkey-backed stores into the OAuth `Manager` via functional options (`WithValkeyTokenStore`, `WithValkeyStateStore`). This reuses the same Valkey connection pool as the auth/capability stores.

## Consequences

### Positive

1. **Reduced latency**: SSO tool calls drop from 3 HTTP round-trips to 1 for pooled connections
2. **Clean separation of concerns**: Auth state, capability cache, and live connections are independent -- each can be refreshed, expired, or cleared without affecting the others
3. **Proactive token refresh**: Token-exchange connections are refreshed before expiry, avoiding 401 round-trips
4. **No connection leaks**: Background reaper cleans up idle connections; all eviction paths are covered
5. **Fail-closed security**: Auth gate denies access when the auth store is unavailable, rather than silently allowing unauthenticated calls
6. **Multi-pod ready**: Valkey-backed session auth, capability, OAuth token, and OAuth state stores enable horizontal scaling without sticky sessions

### Negative

1. **Memory overhead**: Pooled connections hold open sockets. Mitigated by the 1-hour idle reaper.
2. **Increased state surface**: Three stores (auth, capability, pool) must be kept in sync across all eviction paths. Mitigated by the eviction coverage table above ensuring completeness.
3. **Connection pool is in-memory only**: `SessionConnectionPool` is single-pod (pooled TCP connections cannot be shared). Multi-pod deployments share auth/capability state via Valkey but each pod maintains its own connection pool.

### Neutral

- Token-forwarding clients are unaffected by the token expiry handling (their `headerFunc` already resolves fresh tokens dynamically)
- BDD test scenarios continue working without changes
- The `CapabilityStore` continues to serve its original purpose (caching tool/resource/prompt metadata) unchanged

## Related Decisions

- [ADR-006: Session-Scoped Tool Visibility](006-session-scoped-tool-visibility.md) -- established the per-session model; this ADR adds connection pooling and auth separation within that model
- [ADR-009: SSO Token Forwarding](009-sso-token-forwarding.md) -- defines the token forwarding and exchange mechanisms; this ADR addresses connection reuse and token lifecycle for those mechanisms
- [ADR-010: Server-Side Meta-Tools](010-server-side-meta-tools.md) -- tool calls routed through `call_tool` meta-tool use the same pool

## Related Issues

- #485: Per-session connection pool for SSO servers
