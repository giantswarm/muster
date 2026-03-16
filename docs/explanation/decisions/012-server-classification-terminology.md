# ADR-012: Server Classification Terminology

## Status

Accepted

## Context

The muster codebase, documentation, plans, and issues use inconsistent terminology for downstream MCP servers. The same server category is variously referred to as:

- "SSO server"
- "OAuth server"
- "OAuth-protected server"
- "non-OAuth server"
- "non-SSO server"

This causes real problems:

1. **Concept conflation**: "SSO server" is used as shorthand for any server with `StatusAuthRequired`, when SSO (token forwarding and token exchange) is just one of three authentication mechanisms. Manual OAuth login produces the exact same per-session architecture but is not SSO.

2. **AI agent confusion**: AI agents (and humans) reading the code, plans, or issues see "SSO" and mentally narrow the scope to automatic authentication flows, missing that manual OAuth servers follow the identical code paths.

3. **Misleading function names**: Functions like `handleNonOAuthCapabilityChanged` and `refreshNonOAuthCapabilities` suggest an OAuth-specific concern when the actual distinction is about the connection model (global client vs. per-session client).

### The actual distinction

There are exactly **two server categories** in the aggregator, defined by their **connection model**:

| Property | Non-authenticated servers | Authenticated servers |
|---|---|---|
| Status | `StatusConnected` | `StatusAuthRequired` |
| Client model | Single persistent `ServerInfo.Client` shared by all sessions | Per-session clients pooled in `SessionConnectionPool` |
| Tool storage | `ServerInfo.Tools` (global) | `CapabilityStore` (keyed by `sessionID + serverName`) |
| Auth state | None | `SessionAuthStore` (keyed by `sessionID + serverName`) |
| Notification refresh | Via persistent client's `OnNotification` | Via pooled client's `OnNotification` (session-scoped) |

**Within authenticated servers**, there are three auth mechanisms that do NOT affect the connection model:

| Mechanism | Trigger | Configuration |
|---|---|---|
| Manual OAuth login | User runs `core_auth_login` | Default for servers returning 401 during registration |
| Token forwarding | Automatic at session creation | `auth.forwardToken: true` |
| Token exchange | Automatic at session creation | `auth.tokenExchange.enabled: true` |

The difference between these mechanisms is *how the initial connection is established*. Once connected, the per-session client, capability store, auth store, and connection pool interactions are identical.

## Decision

### Standard terminology

Use these terms consistently across all code, documentation, issues, and plans:

| Term | Meaning | Use when... |
|---|---|---|
| **non-authenticated server** | A server with `StatusConnected` and a persistent global client | Describing the connection model |
| **authenticated server** | A server with `StatusAuthRequired` and per-session pooled clients | Describing the connection model |
| **token forwarding** | An auth mechanism where muster forwards its own ID token | Describing a specific auth mechanism |
| **token exchange** | An auth mechanism using RFC 8693 token exchange | Describing a specific auth mechanism |
| **manual OAuth login** | An auth mechanism where the user authenticates via browser | Describing a specific auth mechanism |
| **SSO** | Collective term for token forwarding and token exchange (automatic auth) | Describing automatic authentication, never as a server category |

### Terms to avoid

| Avoid | Use instead | Why |
|---|---|---|
| "SSO server" | "authenticated server" | SSO is an auth mechanism, not a server category |
| "OAuth server" | "authenticated server" | OAuth is an auth mechanism, not a server category |
| "non-OAuth server" | "non-authenticated server" | Defines the category by what it lacks, using the wrong axis |
| "OAuth-protected server" | "authenticated server" | Same issue as "OAuth server" |

### Code naming conventions

For new code, use names that reflect the connection model:

- Functions operating on non-authenticated servers: `*Global*` or `*Persistent*` (e.g., `refreshGlobalCapabilities`)
- Functions operating on authenticated servers: `*Session*` or `*PerSession*` (e.g., `refreshSessionCapabilities` -- already correct)
- Functions operating on SSO specifically: `*SSO*` is fine when the code is actually specific to automatic auth (e.g., `initSSOForSession`, `ssoTracker`)

### Existing code

Existing function names like `refreshNonOAuthCapabilities`, `handleNonOAuthCapabilityChanged`, and `wirePoolNotificationCallback` are not renamed in this ADR. Renaming should happen opportunistically when those files are modified for other reasons, to avoid unnecessary churn. The terminology standard applies to:

- All new code
- All new documentation and ADRs
- All new issues and plans
- Comments and doc strings when they are touched for other reasons

## Consequences

### Positive

1. **Correct mental model**: The two-tier architecture is described by its actual axis (connection model), not by a subset of its authentication mechanisms.
2. **Reduced AI confusion**: AI agents reading "authenticated server" correctly understand this includes all three auth mechanisms, not just SSO.
3. **Clearer design discussions**: Layer 3 (polling) and Layer 4 (health tracking) can be discussed in terms of "iterate authenticated servers" instead of the misleading "iterate SSO servers".

### Negative

1. **Terminology gap**: Existing code, earlier ADRs (#011), and closed issues still use the old terms. This is acceptable as a living codebase; the standard applies going forward.

### Neutral

- The code constants `StatusConnected` and `StatusAuthRequired` remain unchanged -- they already align with this terminology.
- `ShouldUseTokenForwarding` and `ShouldUseTokenExchange` remain unchanged -- they correctly describe specific auth mechanisms, not server categories.

## Related Decisions

- [ADR-008: Unified Authentication Architecture](008-unified-authentication.md) -- defines `core_auth_login`/`core_auth_logout` as the auth interface
- [ADR-009: SSO Token Forwarding](009-sso-token-forwarding.md) -- defines token forwarding and exchange as SSO mechanisms
- [ADR-011: Session Connection Pool](011-session-connection-pool.md) -- establishes the three-store model for authenticated servers
