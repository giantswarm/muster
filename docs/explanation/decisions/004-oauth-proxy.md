# 004. OAuth Proxy for Remote MCP Servers

## Context

We have added support for connecting to remote MCP servers (e.g., `mcp-kubernetes`). These servers often require authentication, specifically OAuth 2.1 (using `mcp-oauth`). 

The `muster` architecture consists of:
- **Muster Agent**: Runs locally (e.g., on a user's laptop), integrated with clients like Cursor via stdio.
- **Muster Server**: Runs centrally (e.g., on a management cluster), aggregating tools from remote MCP servers.

We need a flow where:
1. The User interacts with Cursor (Agent).
2. The Agent calls the Server.
3. The Server calls the Remote MCP.
4. If the Remote MCP requires authentication, the User can authenticate via their browser.
5. Cursor/Agent should **not** receive the sensitive token; the Server should manage it and proxy the authenticated requests.

## Decision

We will implement an **OAuth Proxy** pattern where the **Muster Server** acts as the OAuth Client on behalf of the user.

### 1. Roles

*   **Muster Server (OAuth Client & Proxy)**:
    *   Maintains OAuth configurations for downstream Remote MCP servers.
    *   Acts as the registered OAuth Client (using CIMD).
    *   Initiates the Authorization Code flow.
    *   Hosts the Redirect URI endpoint (`/oauth/callback`) to receive authorization codes.
    *   Exchanges codes for Access/Refresh tokens.
    *   Stores tokens securely, associated with the user's session.
    *   Injects the Access Token into outgoing requests to the Remote MCP.

*   **Muster Agent (UI Bridge)**:
    *   Detects "Authentication Required" responses from the Muster Server.
    *   Presents the Authorization URL to the user via the Tool Result (as text/link).
    *   Instruction: "Please authenticate in your browser: [Link]".

### 2. Authentication Flow

1.  **Tool Call**: User requests an action (e.g., "List Pods"). Cursor calls `muster agent`, which forwards the call to `muster server`.
2.  **Auth Check**: `muster server` attempts to call the Remote MCP (`mcp-kubernetes`).
    *   If the request fails with `401 Unauthorized` or if no token exists for this session:
    *   The Server generates an OAuth Authorization URL for the Remote MCP.
    *   The Server generates a unique `state` parameter to link the flow.
3.  **Auth Challenge**: `muster server` returns a structured "Auth Required" error/response to the `muster agent`.
    *   Payload: `{ "status": "auth_required", "auth_url": "https://..." }`
4.  **User Interaction**: `muster agent` formats this into a user-friendly message for Cursor.
    *   Example Tool Output: `Authentication required for Kubernetes. Please visit: https://remote-mcp/oauth/authorize?...`
5.  **Browser Flow**:
    *   User clicks the link.
    *   User authenticates with the Identity Provider (e.g., Dex/Google).
    *   Browser redirects to `muster server`'s callback URL: `https://muster.giantswarm.io/oauth/callback?code=...&state=...`.
6.  **Token Exchange**:
    *   `muster server` receives the code.
    *   Exchanges it for an Access Token (and Refresh Token).
    *   Stores the token in memory (or persistent store) mapped to the user's session.
    *   Displays an HTML success page: "Authentication Successful. You may return to Cursor."
7.  **Retry**:
    *   User sees the success message.
    *   User retries the instruction in Cursor (e.g., "Try again").
    *   `muster server` now finds the valid token and proxies the request successfully.

### 3. Session Management

To link the Tool Call (Step 1) with the Callback (Step 6), we need a session identifier.
*   The `muster agent` should generate a persistent `session_id` (e.g., UUID) on startup.
*   This `session_id` is sent with every request to `muster server` (e.g., in a Header `X-Muster-Session-ID`).
*   The `muster server` uses this ID to store and retrieve tokens.

### 4. Client Registration (CIMD on GitHub Pages)

To simplify registration and establish trust with Remote MCP servers, we will publish the **Client ID Metadata Document (CIMD)** on GitHub Pages (or similar static hosting), following the pattern used by `mcp-debug`.

*   **Hosting**: The metadata document will be hosted at a stable HTTPS URL, e.g., `https://giantswarm.github.io/muster/oauth-client.json`.
*   **Client ID**: This URL itself will be the `client_id` used by Muster Server when authenticating with `mcp-kubernetes`.
*   **Content**: The JSON document will define the client's properties:
    ```json
    {
      "client_id": "https://giantswarm.github.io/muster/oauth-client.json",
      "client_name": "Muster Aggregator",
      "redirect_uris": ["https://muster.giantswarm.io/oauth/callback"],
      "grant_types": ["authorization_code", "refresh_token"],
      "response_types": ["code"],
      "token_endpoint_auth_method": "none"
    }
    ```
*   **Dynamic vs Static**: Since the `redirect_uris` might depend on the specific deployment of Muster Server (e.g., customer environments vs SaaS), we may need multiple CIMD files or a way to parameterize/host them. For the SaaS/Managed instance, a static GitHub Pages file is sufficient. For self-hosted, users might host their own CIMD or rely on manual registration.

### 5. Single Sign-On (SSO) and Token Reuse

To support SSO across multiple MCP servers that share the same Authenticator (IdP), we will implement a Token Reuse Strategy.

*   **Browser-Based SSO (Default)**: Since authentication happens in the browser, users will benefit from the existing session with the IdP (e.g., Dex/Google). If they authenticate with Server A, subsequent authentication requests for Server B (using the same IdP) will result in an immediate redirect back to Muster without a login prompt.
*   **Token Reuse Strategy**:
    *   Muster Server will inspect the `WWW-Authenticate` header from `401 Unauthorized` responses.
    *   It will extract the `realm` (Issuer URL) and `scope`.
    *   Tokens in the store will be indexed by `(SessionID, Issuer, Scope)` in addition to `ServerID`.
    *   Before triggering the auth flow, Muster will check if a valid token already exists for the requested `Issuer` and `Scope`.
    *   If a match is found, Muster will retry the request with the existing token, effectively enabling transparent SSO for servers sharing the same configuration.

## Consequences

*   **Public Reachability**: `muster server` requires a public URL (Ingress) to receive OAuth callbacks.
*   **Stateful Server**: The server needs to manage user sessions and tokens. For HA, a distributed store (e.g., Redis/Valkey) might be needed in the future, but in-memory is sufficient for the initial MVP (single replica).
*   **User Experience**: The user must manually click a link and then retry the action in Cursor. This is a limitation of the decoupled architecture but provides high security (token never leaves the server boundary). With SSO/Token Reuse, subsequent auths become transparent or one-click.
*   **CIMD Hosting**: We need to maintain the `oauth-client.json` file in the `gh-pages` branch or `docs/` folder of the repo.

## Implementation Steps

1.  **Muster Server**:
    *   Add `internal/oauth/client` package.
    *   Implement `/oauth/callback` handler.
    *   Implement `/.well-known/oauth-client` handler (optional, if self-hosting CIMD).
    *   Add Session/Token Store (In-Memory).
    *   Update `aggregator` to intercept 401s and trigger flow.
    *   **New**: Implement logic to parse `WWW-Authenticate` headers and lookup reusable tokens.
2.  **Muster Agent**:
    *   Update `agent` to handle "Auth Required" responses and format them for Cursor.
3.  **Configuration**:
    *   Add flags for `public-url` (for callback construction).
    *   Add flag for `client-id` (to use the CIMD URL).
4.  **Documentation**:
    *   Create and publish `oauth-client.json` to GitHub Pages.
