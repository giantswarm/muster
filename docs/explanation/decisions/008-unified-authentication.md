# 008. Unified Authentication Architecture

## Status

Accepted (Implemented)

## Context

### Problem Statement

The current authentication implementation has evolved organically across [ADR-004](004-oauth-proxy.md) (OAuth Proxy) and [ADR-005](005-muster-auth.md) (Muster Auth), resulting in architectural issues that impact maintainability, user experience, and SSO capabilities.

**Key problems identified:**

1. **Inference-Based Auth Detection**: The agent infers authentication requirements by scanning tool names for `_authenticate` suffixes. This loses critical information (issuer URL, SSO capability, error details) and is fragile.

2. **One-Shot Auto-SSO**: Auto-SSO triggers once at agent startup and never watches for new servers or token expirations. Servers added after initial connection require manual intervention.

3. **Dual OAuth Implementations**: Two parallel OAuth packages (`internal/agent/oauth/` and `internal/oauth/`) implement overlapping functionality with ~80% code duplication.

4. **Storage Semantics Mismatch**: Agent uses URL-based token keys (no SSO), while Server uses issuer-based keys (SSO-enabled). This creates inconsistent SSO behavior across the system.

5. **Fragile 401 Detection**: Authentication requirements are detected by string-matching error messages for "401", which is lossy and error-prone.

6. **Session vs Global Confusion**: A server can be `StatusAuthRequired` globally but `StatusSessionConnected` for specific sessions. This dual state model complicates tool visibility logic.

### Current State Distribution

Authentication state is fragmented across multiple locations:

| Location | State | Scope |
|----------|-------|-------|
| `internal/agent/oauth/auth_manager.go` | Auth flow state machine | Agent process |
| `internal/agent/oauth/token_store.go` | Tokens keyed by URL hash | Agent filesystem |
| `internal/aggregator/registry.go` | Server auth status | Server process (global) |
| `internal/aggregator/session_registry.go` | Session connections | Server process (per-session) |
| `internal/oauth/token_store.go` | Tokens keyed by session+issuer | Server memory |

### Requirements

- Explicit auth state communication (not inferred from tool names)
- Continuous SSO watching (not one-shot at startup)
- Unified OAuth core with specialized storage backends
- Issuer-based token storage for consistent SSO
- Structured 401 detection with full auth challenge information
- Clear session-scoped auth lifecycle

## Decision

We will refactor authentication into a unified architecture with three layers:

1. **Shared OAuth Core** (`pkg/oauth/`): Common types, client, and parsing
2. **Explicit Auth State Resource**: MCP resource for structured auth status
3. **Continuous Auth Watcher**: Event-driven SSO with token forwarding

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                                                                 │
│   AGENT                                          SERVER                         │
│                                                                                 │
│   ┌──────────────────────┐                      ┌──────────────────────────────┐│
│   │  internal/agent/     │                      │  internal/aggregator/        ││
│   │  ┌────────────────┐  │                      │  ┌────────────────────────┐  ││
│   │  │ AuthWatcher    │◄─┼── MCP Resource ──────┼──│ AuthStateResource      │  ││
│   │  │ (continuous)   │  │   auth://status      │  │                        │  ││
│   │  └────────────────┘  │                      │  └────────────────────────┘  ││
│   │         │            │                      │             ▲                ││
│   │         ▼            │                      │             │                ││
│   │  ┌────────────────┐  │                      │  ┌────────────────────────┐  ││
│   │  │ AuthOrchestrator│ │   submit_auth_token  │  │ ServerRegistry         │  ││
│   │  │ (SSO decisions) │─┼──────────────────────┼─►│ SessionRegistry        │  ││
│   │  └────────────────┘  │                      │  └────────────────────────┘  ││
│   │         │            │                      │                              ││
│   │         ▼            │                      └──────────────────────────────┘│
│   │  ┌────────────────┐  │                      ┌──────────────────────────────┐│
│   │  │ pkg/oauth/     │  │                      │  pkg/oauth/                  ││
│   │  │ (shared core)  │  │                      │  (shared core)               ││
│   │  └────────────────┘  │                      └──────────────────────────────┘│
│   │         │            │                                                      │
│   │         ▼            │                                                      │
│   │  ┌────────────────┐  │                                                      │
│   │  │ FileTokenStore │  │                                                      │
│   │  │ (issuer-keyed) │  │                                                      │
│   │  └────────────────┘  │                                                      │
│   └──────────────────────┘                                                      │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 1. Shared OAuth Core (`pkg/oauth/`)

Extract common OAuth functionality into a shared package that both agent and server import.

#### Types

```go
// pkg/oauth/types.go

// Token represents an OAuth token with its metadata
type Token struct {
    AccessToken  string    `json:"access_token"`
    RefreshToken string    `json:"refresh_token,omitempty"`
    Expiry       time.Time `json:"expiry"`
    Issuer       string    `json:"issuer"`
    Scopes       []string  `json:"scopes,omitempty"`
}

// IsExpired returns true if the token has expired (with 30s margin)
func (t *Token) IsExpired() bool {
    return time.Now().Add(30 * time.Second).After(t.Expiry)
}

// AuthChallenge represents parsed WWW-Authenticate header information
type AuthChallenge struct {
    Issuer      string `json:"issuer"`
    Scope       string `json:"scope"`
    Realm       string `json:"realm,omitempty"`
    ResourceURI string `json:"resource_uri,omitempty"`
}

// PKCEFlow represents an in-progress PKCE authorization flow
type PKCEFlow struct {
    CodeVerifier  string
    CodeChallenge string
    State         string
    RedirectURI   string
    AuthURL       string
}

// Metadata represents OAuth/OIDC server metadata
type Metadata struct {
    Issuer                string   `json:"issuer"`
    AuthorizationEndpoint string   `json:"authorization_endpoint"`
    TokenEndpoint         string   `json:"token_endpoint"`
    ScopesSupported       []string `json:"scopes_supported,omitempty"`
}
```

#### Client

```go
// pkg/oauth/client.go

// Client handles OAuth protocol operations
type Client struct {
    httpClient *http.Client
    logger     *slog.Logger
}

// DiscoverMetadata fetches OAuth/OIDC metadata for an issuer
// Tries RFC 8414 first, then falls back to OIDC discovery
func (c *Client) DiscoverMetadata(ctx context.Context, issuer string) (*Metadata, error)

// StartPKCEFlow initiates a PKCE authorization code flow
func (c *Client) StartPKCEFlow(metadata *Metadata, redirectURI string, scopes []string) (*PKCEFlow, error)

// ExchangeCode exchanges an authorization code for tokens
func (c *Client) ExchangeCode(ctx context.Context, metadata *Metadata, flow *PKCEFlow, code string) (*Token, error)

// RefreshToken obtains a new access token using a refresh token
func (c *Client) RefreshToken(ctx context.Context, metadata *Metadata, token *Token) (*Token, error)
```

#### WWW-Authenticate Parsing

```go
// pkg/oauth/www_authenticate.go

// ParseWWWAuthenticate parses the WWW-Authenticate header value
func ParseWWWAuthenticate(header string) (*AuthChallenge, error)

// ParseFromError attempts to extract auth challenge from an error
// This is a best-effort fallback when the header is not directly available
func ParseFromError(err error) (*AuthChallenge, error)
```

#### PKCE

```go
// pkg/oauth/pkce.go

// GeneratePKCE generates a PKCE code verifier and S256 challenge
func GeneratePKCE() (verifier, challenge string, err error)
```

### 2. Explicit Auth State Resource

Replace tool-name inference with a dedicated MCP resource that provides structured auth state.

#### Resource Definition

```go
// internal/aggregator/auth_resource.go

// Resource URI: auth://status
// Provides structured authentication state for the current session

type AuthStatusResponse struct {
    // MusterAuth describes authentication to Muster Server itself
    MusterAuth *MusterAuthStatus `json:"muster_auth"`
    
    // ServerAuths describes authentication to each remote MCP server
    ServerAuths []ServerAuthStatus `json:"server_auths"`
}

type MusterAuthStatus struct {
    Authenticated bool   `json:"authenticated"`
    User          string `json:"user,omitempty"`
    Issuer        string `json:"issuer,omitempty"`
}

type ServerAuthStatus struct {
    // ServerName is the name of the MCP server
    ServerName string `json:"server_name"`
    
    // Status is one of: "connected", "auth_required", "error", "initializing"
    Status string `json:"status"`
    
    // AuthChallenge is present when Status == "auth_required"
    AuthChallenge *AuthChallengeInfo `json:"auth_challenge,omitempty"`
    
    // Error is present when Status == "error"
    Error string `json:"error,omitempty"`
}

type AuthChallengeInfo struct {
    // Issuer is the IdP URL that will issue tokens
    Issuer string `json:"issuer"`
    
    // Scope is the OAuth scope required
    Scope string `json:"scope,omitempty"`
    
    // AuthToolName is the tool to call for browser-based auth (legacy support)
    AuthToolName string `json:"auth_tool_name"`
}
```

#### Resource Handler

```go
// internal/aggregator/auth_resource.go

func (r *AuthResource) GetResource(ctx context.Context, uri string) (*mcp.Resource, error) {
    sessionID := GetSessionIDFromContext(ctx)
    
    status := &AuthStatusResponse{
        MusterAuth:  r.getMusterAuthStatus(ctx),
        ServerAuths: r.getServerAuthStatuses(ctx, sessionID),
    }
    
    data, _ := json.Marshal(status)
    return &mcp.Resource{
        URI:      "auth://status",
        MimeType: "application/json",
        Contents: string(data),
    }, nil
}
```

### 3. Issuer-Keyed Token Store (Agent)

Migrate agent token storage from URL-based to issuer-based keys.

#### Token Store

```go
// internal/agent/oauth/token_store.go

type TokenStore struct {
    storageDir string
    tokens     map[string]*pkg_oauth.Token // Key: hash(issuer)
    mu         sync.RWMutex
}

// Store saves a token indexed by its issuer
func (s *TokenStore) Store(token *pkg_oauth.Token) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    key := s.hashIssuer(token.Issuer)
    s.tokens[key] = token
    
    return s.persistToken(key, token)
}

// GetByIssuer retrieves a token for a specific issuer
func (s *TokenStore) GetByIssuer(issuer string) *pkg_oauth.Token {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    key := s.hashIssuer(issuer)
    token, ok := s.tokens[key]
    if !ok || token.IsExpired() {
        return nil
    }
    return token
}

// GetByIssuerWithRefresh retrieves a token, refreshing if expired
func (s *TokenStore) GetByIssuerWithRefresh(ctx context.Context, client *pkg_oauth.Client, issuer string) (*pkg_oauth.Token, error)
```

#### File Structure

```
~/.config/muster/tokens/
├── tokens.json          # All tokens in single encrypted file
└── tokens.json.backup   # Backup before writes
```

Token file format:
```json
{
  "version": 2,
  "tokens": {
    "https://dex.example.com": {
      "access_token": "...",
      "refresh_token": "...",
      "expiry": "2024-01-15T10:30:00Z",
      "issuer": "https://dex.example.com",
      "scopes": ["openid", "profile", "email"]
    }
  }
}
```

### 4. Continuous Auth Watcher (Agent)

Replace one-shot SSO with continuous auth state watching.

#### AuthWatcher

```go
// internal/agent/auth_watcher.go

type AuthWatcher struct {
    client       *Client
    tokenStore   *oauth.TokenStore
    oauthClient  *pkg_oauth.Client
    pollInterval time.Duration
    logger       *slog.Logger
    
    // Callbacks
    onBrowserAuthRequired func(server string, authURL string)
    onAuthComplete        func(server string)
    onAuthError           func(server string, err error)
}

func NewAuthWatcher(client *Client, tokenStore *oauth.TokenStore, opts ...AuthWatcherOption) *AuthWatcher

func (w *AuthWatcher) Start(ctx context.Context) {
    ticker := time.NewTicker(w.pollInterval)
    defer ticker.Stop()
    
    var lastStatus *AuthStatusResponse
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            status, err := w.fetchAuthStatus(ctx)
            if err != nil {
                w.logger.Warn("Failed to fetch auth status", "error", err)
                continue
            }
            
            newChallenges := w.detectNewChallenges(lastStatus, status)
            resolvedChallenges := w.detectResolvedChallenges(lastStatus, status)
            
            for _, challenge := range newChallenges {
                w.handleNewChallenge(ctx, challenge)
            }
            
            for _, server := range resolvedChallenges {
                w.onAuthComplete(server)
            }
            
            lastStatus = status
        }
    }
}

func (w *AuthWatcher) handleNewChallenge(ctx context.Context, challenge ServerAuthStatus) {
    // Check if we have a token for this issuer
    token := w.tokenStore.GetByIssuer(challenge.AuthChallenge.Issuer)
    
    if token != nil && !token.IsExpired() {
        // SSO: Forward existing token to server
        w.logger.Info("SSO: Forwarding existing token",
            "server", challenge.ServerName,
            "issuer", challenge.AuthChallenge.Issuer)
        
        err := w.submitToken(ctx, challenge.ServerName, token)
        if err != nil {
            w.logger.Warn("Failed to submit token", "error", err)
            // Fall through to browser auth
        } else {
            return // Token submitted successfully
        }
    }
    
    // Need browser authentication
    w.onBrowserAuthRequired(challenge.ServerName, challenge.AuthChallenge.AuthToolName)
}

func (w *AuthWatcher) fetchAuthStatus(ctx context.Context) (*AuthStatusResponse, error) {
    resource, err := w.client.GetResource(ctx, "auth://status")
    if err != nil {
        return nil, err
    }
    
    var status AuthStatusResponse
    if err := json.Unmarshal([]byte(resource.Contents), &status); err != nil {
        return nil, err
    }
    return &status, nil
}
```

### 5. Token Forwarding

Enable agent-side SSO by allowing the agent to submit tokens to the server.

#### Submit Token Tool

```go
// internal/aggregator/tools.go

// Tool: submit_auth_token
// Called by the agent when it has a valid token for a server's issuer

type SubmitAuthTokenInput struct {
    ServerName  string `json:"server_name"`
    AccessToken string `json:"access_token"`
}

func (a *Aggregator) handleSubmitAuthToken(ctx context.Context, input SubmitAuthTokenInput) (*mcp.ToolResult, error) {
    sessionID := GetSessionIDFromContext(ctx)
    
    // Store the token for this session
    err := a.sessionRegistry.StoreToken(sessionID, input.ServerName, input.AccessToken)
    if err != nil {
        return nil, fmt.Errorf("failed to store token: %w", err)
    }
    
    // Attempt to connect to the server with the token
    err = a.connectWithToken(ctx, sessionID, input.ServerName, input.AccessToken)
    if err != nil {
        return &mcp.ToolResult{
            Content: []mcp.Content{{
                Type: "text",
                Text: fmt.Sprintf("Token accepted but connection failed: %v", err),
            }},
            IsError: true,
        }, nil
    }
    
    // Notify tools changed
    a.notifyToolsChanged(sessionID)
    
    return &mcp.ToolResult{
        Content: []mcp.Content{{
            Type: "text",
            Text: fmt.Sprintf("Successfully authenticated to %s", input.ServerName),
        }},
    }, nil
}
```

### 6. Structured 401 Detection

Replace string-matching with proper HTTP response handling.

#### MCP Client Enhancement

```go
// internal/mcpserver/client.go

type AuthRequiredError struct {
    URL           string
    AuthChallenge *pkg_oauth.AuthChallenge
    OriginalError error
}

func (e *AuthRequiredError) Error() string {
    return fmt.Sprintf("authentication required for %s (issuer: %s)", e.URL, e.AuthChallenge.Issuer)
}

// During MCP client connection, capture the HTTP response directly
func (c *Client) Connect(ctx context.Context, url string, opts ...Option) error {
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return err
    }
    
    if resp.StatusCode == http.StatusUnauthorized {
        wwwAuth := resp.Header.Get("WWW-Authenticate")
        challenge, parseErr := pkg_oauth.ParseWWWAuthenticate(wwwAuth)
        if parseErr != nil {
            challenge = &pkg_oauth.AuthChallenge{Issuer: "unknown"}
        }
        
        return &AuthRequiredError{
            URL:           url,
            AuthChallenge: challenge,
            OriginalError: fmt.Errorf("401 Unauthorized"),
        }
    }
    
    // ... continue with connection
}
```

### 7. API Integration

Following the service locator pattern (ADR-001), expose auth functionality through the API layer.

#### Handler Interface

```go
// internal/api/handlers.go

// AuthHandler provides authentication operations
type AuthHandler interface {
    // GetAuthStatus returns the current auth status for a session
    GetAuthStatus(ctx context.Context, sessionID string) (*AuthStatusResponse, error)
    
    // SubmitToken stores a token for a session/server combination
    SubmitToken(ctx context.Context, sessionID string, serverName string, token string) error
    
    // GetTokenForIssuer retrieves a stored token by issuer (server-side)
    GetTokenForIssuer(ctx context.Context, sessionID string, issuer string) (*Token, error)
}
```

## Implementation Steps

### Phase 1: Shared OAuth Core

1. Create `pkg/oauth/` package with:
   - `types.go`: Token, AuthChallenge, PKCEFlow, Metadata
   - `client.go`: OAuth client with metadata discovery, PKCE flow, token exchange
   - `www_authenticate.go`: WWW-Authenticate parsing
   - `pkce.go`: PKCE generation
   - `doc.go`: Package documentation

2. Refactor `internal/oauth/` to import and use `pkg/oauth/` types and client

3. Refactor `internal/agent/oauth/` to import and use `pkg/oauth/` types and client

4. Delete duplicated code from both packages

### Phase 2: Auth Status Resource

1. Create `internal/aggregator/auth_resource.go`:
   - Implement `auth://status` resource
   - Register with MCP resource handler

2. Update `internal/aggregator/registry.go`:
   - Add method to collect auth status for all servers
   - Include issuer information in status

3. Update `internal/aggregator/session_registry.go`:
   - Add method to get session-specific auth status

### Phase 3: Issuer-Keyed Agent Tokens

1. Redesign `internal/agent/oauth/token_store.go`:
   - Change key from URL hash to issuer
   - Update file format (version 2)
   - Implement migration from v1 format

2. Update `internal/agent/oauth/auth_manager.go`:
   - Use issuer-based token lookup
   - Add `GetByIssuer` method

### Phase 4: Continuous Auth Watcher

1. Create `internal/agent/auth_watcher.go`:
   - Implement polling of `auth://status` resource
   - Detect new auth challenges
   - Implement SSO token lookup and forwarding

2. Update `cmd/agent.go`:
   - Replace `triggerPendingRemoteAuth` with AuthWatcher
   - Start AuthWatcher after initial connection

3. Create `internal/aggregator/submit_token.go`:
   - Implement `submit_auth_token` tool
   - Connect session to server with provided token

### Phase 5: Structured 401 Detection

1. Update `internal/mcpserver/client.go`:
   - Capture HTTP response on connection
   - Parse WWW-Authenticate header directly
   - Return structured `AuthRequiredError`

2. Update `internal/aggregator/registry.go`:
   - Use structured error for auth challenge extraction

### Phase 6: Cleanup

1. Remove deprecated code:
   - Tool-name inference in agent
   - String-matching 401 detection
   - One-shot SSO trigger

2. Update documentation

3. Add migration notes

## Consequences

### Positive

#### User Experience
- **Transparent SSO**: Tokens automatically forwarded when issuer matches
- **Continuous Auth**: New servers get authenticated without agent restart
- **Clear Feedback**: Explicit auth status instead of inferred state

#### Architecture
- **Single Source of Truth**: Auth state explicitly provided, not inferred
- **Code Reuse**: ~80% less OAuth code through shared core
- **Clear Boundaries**: Auth responsibilities clearly separated

#### Maintainability
- **Testability**: Shared core tested once, specialized layers tested in isolation
- **Debugging**: Explicit state easier to debug than inference
- **Evolution**: Clear interfaces allow independent evolution

### Negative

#### Complexity
- **New Resource**: Additional MCP resource to maintain
- **Token Forwarding**: Security implications of sending tokens via tools
- **Polling Overhead**: Continuous polling adds network traffic

#### Migration
- **Token Format Change**: Users may need to re-authenticate
- **API Changes**: Components must update to use new interfaces

### Risk Mitigation

#### For Token Forwarding Security
- Tokens already traverse the same transport for MCP calls
- `submit_auth_token` uses existing authentication
- Tokens are session-scoped on server side

#### For Migration
- Token store supports version detection
- Automatic migration from v1 to v2 format
- Clear error messages for migration failures

#### For Polling Overhead
- Reasonable poll interval (5-10 seconds)
- Lightweight resource response
- Could add MCP notifications in future

## Related Decisions

- [ADR-001: API Service Locator Pattern](001-api-service-locator.md) - Auth handlers follow this pattern
- [ADR-004: OAuth Proxy](004-oauth-proxy.md) - Superseded for server OAuth implementation
- [ADR-005: Muster Auth](005-muster-auth.md) - Superseded for agent OAuth implementation
- [ADR-006: Session-Scoped Tool Visibility](006-session-scoped-tool-visibility.md) - Auth affects tool visibility

## References

- [OAuth 2.1 Specification](https://oauth.net/2.1/)
- [RFC 7636: PKCE](https://datatracker.ietf.org/doc/html/rfc7636)
- [RFC 8414: OAuth 2.0 Authorization Server Metadata](https://datatracker.ietf.org/doc/html/rfc8414)
- [MCP Specification](https://modelcontextprotocol.io/)

