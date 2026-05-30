# Authorization Hardening (MCP 2026-07-28)

## 1. What the spec says

The 2026-07-28 release candidate folds six authorization SEPs into the draft
specification under `basic/authorization`. They do not redesign MCP's OAuth 2.1
profile; they close concrete interoperability and security gaps that have surfaced
since the 2025-11-25 release, primarily where MCP clients connect to OpenID
Connect-flavoured authorization servers and where a single client speaks to many
authorization servers.

### SEP-2468 — Authorization-server issuer (`iss`) validation per RFC 9207

The draft now requires authorization servers to include the `iss` query parameter
on every authorization response (success or error), as specified in RFC 9207
section 2. Servers advertise support via the
`authorization_response_iss_parameter_supported: true` metadata flag (RFC 9207
section 3). Clients MUST extract `iss` from the redirect URL, compare it as a
simple string (RFC 3986 §6.2.1) against the issuer they recorded before
redirecting, and abort the flow on mismatch.

The SEP intentionally diverges from RFC 9207 §2.4's "SHOULD discard if not
advertised" recommendation: it specifies "compare if present, reject on
mismatch" so that authorization servers that begin emitting `iss` before
publishing the metadata flag do not break legitimate flows. The SEP notes that
a future revision is expected to upgrade the server-side SHOULD to a MUST and
to require clients to reject responses that omit `iss`.

The mix-up attack class this mitigates is described in the OAuth 2.0 Security
Best Current Practice and in the formal analyses cited by RFC 9207
(arXiv:1508.04324, arXiv:1601.01229). It is relevant to any client that talks
to more than one authorization server — exactly muster's deployment shape.

### SEP-837 — OIDC `application_type` during Dynamic Client Registration

The draft now references OpenID Connect Dynamic Client Registration 1.0 as a
normative input and adds an "Application Type and Redirect URI Constraints"
subsection under DCR. MCP clients MUST send `application_type` on DCR requests
to OIDC-aware servers. Native applications (desktops, CLIs, locally hosted
clients on `localhost`) SHOULD use `application_type: "native"`; remote
browser-based applications SHOULD use `application_type: "web"`.

Without this hint, OIDC-flavoured authorization servers default to `"web"` and
then reject the localhost redirect URIs that a desktop or CLI client needs. The
SEP also requires clients to surface a meaningful error on registration
failure and permits retrying with an adjusted `application_type` or
type-conforming redirect URIs. Non-OIDC servers ignore the parameter, so the
field is safe to send unconditionally.

### SEP-2352 — Authorization server binding and migration

Two new clauses are added to the draft:

1. A clarification under Protected Resource Metadata that each entry in
   `authorization_servers` is an independent OAuth 2.0 authorization server
   (RFC 6749 §2.2), and that clients MUST maintain separate registration state
   per authorization server and MUST NOT assume credentials valid for one are
   accepted by another.
2. A new "Authorization Server Binding" subsection that distinguishes the two
   credential models:
   - **DCR-issued or pre-registered credentials** MUST be keyed by the issuer
     they were registered with. When protected resource metadata starts
     pointing to a different authorization server, clients MUST NOT reuse the
     old credentials and MUST re-register; they SHOULD surface an error rather
     than silently retrying with a mismatched `client_id`.
   - **Client ID Metadata Documents (CIMD)** are explicitly called out as
     portable across authorization servers, because the document is a
     self-hosted HTTPS URL that the authorization server resolves on demand.
     No re-registration is needed when the authorization server changes.

### SEP-2207 — OIDC-flavoured refresh token guidance

OAuth 2.1 has no standard way for a client to request a refresh token; OIDC
introduced the `offline_access` scope for this purpose. SEP-2207 standardises
the cross-cutting guidance:

- MCP clients that intend to use refresh tokens SHOULD advertise
  `refresh_token` in their `grant_types` client metadata, MUST keep refresh
  tokens confidential in transit and at rest per OAuth 2.1 §4.3, and MAY add
  `offline_access` to the `scope` parameter of the authorization and token
  requests when the authorization server advertises `offline_access` in
  `scopes_supported`.
- Clients MUST NOT assume a refresh token will be issued: the authorization
  server retains discretion.
- MCP servers (protected resources) SHOULD NOT include `offline_access` in
  `WWW-Authenticate` `scope` or in Protected Resource Metadata
  `scopes_supported`, because `offline_access` is a client/AS concern, not a
  resource requirement (OAuth 2.1 §5.3.1 frames `scope` as "the required
  scope of the access token for accessing the requested resource").

### SEP-2350 — Client-side scope accumulation on step-up

The draft changes the framing of "Runtime Insufficient Scope Errors" and
"Step-Up Authorization Flow" to align with RFC 6750 §3.1:

- Servers SHOULD return the scopes required for the **current operation** in
  the `WWW-Authenticate` `scope` parameter, not the union of previously
  granted scopes. Servers SHOULD emit all scopes required for an operation in
  a single challenge (no per-call incremental drips). Minimum / recommended /
  extended approaches are described as a server-side UX trade-off only.
- Clients are now responsible for **scope accumulation**: when re-authorising
  after a 403 with `insufficient_scope`, the client MUST compute the union of
  its previously requested scope set and the scopes from the current
  challenge, then request that union, so that previously granted permissions
  are preserved.
- A new note clarifies hierarchical scopes: a client need not deduplicate
  hierarchically (the AS normalises during token issuance), and servers MUST
  account for hierarchy when deciding whether a token is sufficient.

### SEP-2351 — Default `.well-known` discovery suffix

The draft is updated to state explicitly that MCP uses the default
`oauth-authorization-server` well-known URI suffix defined in RFC 8414 §3.1,
and that MCP does **not** define an application-specific suffix. The
multi-endpoint discovery sequence (RFC 8414 path insertion, OIDC path
insertion, OIDC path append, plus the root-path forms) is otherwise unchanged.
This is a pure clarification — required by RFC 8414's rule that any
application using its mechanism must declare its suffix.

### Net effect

Together these SEPs tighten the MCP authorization profile in three directions:

- **Mix-up defence** moves from "SHOULD" to "MUST" for clients via `iss`
  validation.
- **OIDC interoperability** is closed for two recurring failure modes — DCR
  redirect-URI rejection (SEP-837) and absent refresh tokens (SEP-2207).
- **Multi-AS hygiene** is made explicit: credentials and tokens are bound to
  an `issuer`, scope sets are accumulated on the client side, and resource
  servers stop conflating `offline_access` with resource scopes.

## 2. Linked SEPs and PRs

- SEP-2468 "Recommend Issuer (iss) Parameter in MCP Auth Responses":
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2468>
- SEP-837 "Update authorization spec to clarify client type requirements":
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/837>
- SEP-2352 "Clarify authorization server binding and migration":
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2352>
- SEP-2207 "OIDC-flavored refresh token guidance":
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2207>
- SEP-2350 "Clarify client-side scope accumulation in step-up authorization":
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2350>
- SEP-2351 "Explicitly specify RFC 8414 well-known URI suffix for MCP":
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2351>

## 3. Muster impact

Muster has two distinct OAuth surfaces that this section needs to keep
separate:

- The **agent-side client** (`internal/agent/oauth/`) — a CLI/desktop OAuth
  2.1 client that authenticates the user against muster's own OAuth server
  (typically Dex behind muster). It runs a local callback listener on
  `127.0.0.1`.
- The **server-side proxy** (`internal/oauth/` and `pkg/oauth/`) — muster's
  Aggregator acting as an OAuth client to remote, OAuth-protected MCP
  servers, completing the authorization code flow on behalf of a logged-in
  user and storing the resulting tokens. The callback handler is HTTP-based
  (`/oauth/callback`) and runs on the publicly reachable muster server.

The CIMD model used by muster (the `client_id` is a self-hosted CIMD URL —
see [internal/oauth/client.go](../../../internal/oauth/client.go) lines
196-210 and `Manager.NewManager` in
[internal/oauth/manager.go](../../../internal/oauth/manager.go)) is exactly
the case SEP-2352 calls "portable across authorization servers". That
simplifies large parts of SEP-2352 for the server-side proxy, but the
agent-side flow against muster's own AS (and any DCR fallback added later)
still needs to follow the rules.

### SEP-2468 — `iss` validation

- **Agent-side: already implemented.** The agent's local callback already
  captures the `iss` query parameter
  ([internal/agent/oauth/callback_server.go](../../../internal/agent/oauth/callback_server.go)
  line 164, `CallbackResult.Iss` documented at lines 34-37) and
  [internal/agent/oauth/client.go](../../../internal/agent/oauth/client.go)
  lines 299-318 implement the "compare if present, reject on mismatch"
  policy that SEP-2468 specifies, including the explicit note that empty
  `iss` is treated as "not advertised", not as a mismatch.
- **Server-side: gap.** The proxy callback at
  [internal/oauth/handler.go](../../../internal/oauth/handler.go)
  `HandleCallback` (lines 56-132) reads only `code`, `state`,
  `error`, and `error_description` from the redirect URL. It never reads or
  validates `iss`. The proxy is precisely the multi-AS client this SEP is
  aimed at: every remote MCP server registered with
  `Manager.RegisterServer` (`internal/oauth/manager.go` lines 179-196) can
  point to a different `Issuer`, and the auth flow uses
  `OAuthState.Issuer` to drive token exchange
  ([internal/oauth/types.go](../../../internal/oauth/types.go) lines 21-46,
  see `state.Issuer` reads in `handler.go` lines 94-105).
- **Metadata side:** `pkg/oauth/types.go` `Metadata`
  (lines 142-189) does not yet carry
  `authorization_response_iss_parameter_supported`. The SEP recommends
  reflecting this flag so a future "MUST be present" tightening is
  detectable per-AS.

### SEP-837 — `application_type` in DCR

- Muster does not currently implement Dynamic Client Registration: `client_id`
  on the server side is a CIMD URL
  ([internal/oauth/client.go](../../../internal/oauth/client.go) lines 18-31,
  `GetClientMetadata` lines 196-210), and on the agent side the agent uses
  `mcp-oauth` providers (`internal/agent/oauth/client.go` lines 12-17).
  `pkg/oauth/types.go` `ClientMetadata` (lines 262-278) has no
  `application_type` field.
- Impact today is therefore **latent**, not active: muster does not call
  `registration_endpoint`. The impact is on the **CIMD content** muster
  publishes and on any future DCR fallback. The CIMD served by
  `Handler.ServeCIMD` ([internal/oauth/handler.go](../../../internal/oauth/handler.go)
  lines 197-220) — built from `Client.GetClientMetadata` — has no client-type
  hint. Authorization servers that resolve a CIMD URL and synthesise a
  registration may apply the same `"web"` default the SEP warns about.
- The agent path matters because the agent runs against `127.0.0.1`
  redirects ([internal/agent/oauth/callback_server.go](../../../internal/agent/oauth/callback_server.go)
  line 81). If muster's AS is OIDC and ever enables DCR, the agent must
  send `application_type: "native"`. The CLI binary (`cmd/auth_login.go`,
  `cmd/auth_helpers.go`) is the seam where that intent is known.

### SEP-2352 — Issuer binding and re-registration

- **Token store: already correct.** Tokens are keyed by
  `TokenKey{SessionID, Issuer, Scope}`
  ([internal/oauth/types.go](../../../internal/oauth/types.go) lines 7-15) and
  `OAuthState` persists `Issuer` for the duration of the flow (lines 21-46),
  so a successful callback can only write into the issuer it started with.
- **Client credential binding: not directly applicable to muster today.**
  The server-side proxy uses one CIMD `client_id` for all upstream
  authorization servers, which SEP-2352 explicitly designates as portable
  — no re-registration is required when an AS changes.
- **Multi-AS clarification matters at config time:** muster's per-server
  `AuthServerConfig`
  ([internal/oauth/manager.go](../../../internal/oauth/manager.go) lines
  40-45) already keys server configuration by `Issuer`. Treating `Issuer`
  changes for an already-registered MCP server as an "AS migration"
  (drop and re-establish tokens, surface a clear error) is the
  SEP-2352-aligned behaviour. Today `RegisterServer` overwrites the entry
  silently (`manager.go` lines 179-196); cached tokens for the old issuer
  would simply stop being looked up because the `TokenKey.Issuer` changes,
  but no explicit error is surfaced.
- **Pre-registered DCR credentials** (if muster ever introduces them as a
  fallback to CIMD) MUST be persisted alongside the `issuer` they belong
  to, with a check on read.

### SEP-2207 — Refresh tokens with OIDC

- **Agent: already aligned.** `agentOAuthScopes` in
  [internal/agent/oauth/client.go](../../../internal/agent/oauth/client.go)
  line 25 already includes `offline_access`, so the agent advertises its
  intent to refresh. Tokens persisted in
  [pkg/oauth/types.go](../../../pkg/oauth/types.go) `Token` (lines 67-91)
  preserve `RefreshToken` and `ExpiresAt`.
- **Server-side CIMD: already advertises `refresh_token`.**
  `Client.GetClientMetadata` returns
  `GrantTypes: ["authorization_code", "refresh_token"]`
  ([internal/oauth/client.go](../../../internal/oauth/client.go) lines
  196-210). That satisfies the "advertise capability" half of SEP-2207.
- **Server-side scope handling: partial gap.** The per-server `Scope`
  configured in `AuthServerConfig` is fed directly into
  `Client.GenerateAuthURL`
  ([internal/oauth/client.go](../../../internal/oauth/client.go) lines
  108-148). There is no logic to conditionally append `offline_access`
  when the discovered authorization server metadata lists
  `offline_access` in `scopes_supported`. SEP-2207 frames this as
  "MAY" for clients that want a refresh token; muster does want one
  (sessions outlive a single 401 retry), so this is worth adding for the
  server-side proxy path as well, gated on metadata.
- **Resource-server side:** muster acts as a protected resource via its
  own MCP API (`internal/aggregator/auth_resource.go`,
  `internal/aggregator/auth_tools.go`). Any
  `WWW-Authenticate` or Protected Resource Metadata muster ever returns
  MUST NOT include `offline_access`. Today muster does not synthesise
  these payloads in code paths I grepped (the `WWW-Authenticate` headers
  parsed by `pkg/oauth/www_authenticate.go` are inbound, from upstream
  MCP servers); this is therefore a guardrail to keep in mind when the
  inbound-auth surface grows.

### SEP-2350 — Scope accumulation on step-up

- **Parser: existing.** `pkg/oauth/www_authenticate.go` lines 26-71 parses
  `scope=` out of the `WWW-Authenticate` header into
  `AuthChallenge.Scope`
  ([pkg/oauth/types.go](../../../pkg/oauth/types.go) lines 191-216). Note
  that `authParamRegex` only matches `key="value"`; unquoted scope values
  (technically allowed by RFC 6750 §3) are not picked up — orthogonal but
  worth a follow-up.
- **Accumulation: missing on the server-side proxy.**
  `Client.GenerateAuthURL` uses whatever `scope` string the caller hands
  it; there is no logic that says "take the union of the scope previously
  granted to this `(SessionID, Issuer)` and the scope in the new 403
  challenge". The token store already has the previous scope (it is part
  of `TokenKey`), so the data is available.
- **Insufficient-scope retries on the inbound side:** muster's aggregator
  surface
  ([internal/aggregator/auth_resource.go](../../../internal/aggregator/auth_resource.go),
  `auth_tools.go`) returns authentication status to MCP clients but the
  upstream-401 retry loop lives where the outbound MCP client meets
  `pkg/oauth`. Wherever that retry triggers a re-auth, it needs to feed
  the union scope set into `GenerateAuthURL`.

### SEP-2351 — `.well-known` discovery suffix

- **Already aligned.** `Client.doDiscoverMetadata` in
  [pkg/oauth/client.go](../../../pkg/oauth/client.go) lines 120-165 tries
  `/.well-known/oauth-authorization-server` first and falls back to
  `/.well-known/openid-configuration`, using the path-insertion and
  path-append forms RFC 8414 §3.1 and §5 (and the existing 2025-11-25 MCP
  spec) require. Constants are in
  [pkg/oauth/types.go](../../../pkg/oauth/types.go) lines 41-51.
- The SEP is a clarification, not a behavioural change for muster.

## 4. Required changes / migration notes

Concrete work items, grouped by SEP and ordered by risk:

1. **SEP-2468 — server-side proxy `iss` validation (highest priority).**
   - In [internal/oauth/handler.go](../../../internal/oauth/handler.go)
     `HandleCallback`, extract `r.URL.Query().Get("iss")` alongside
     `code` / `state`.
   - Compare against `state.Issuer` (and against the discovered
     `Metadata.Issuer` when available — SEP-2468 mandates an exact match
     against the issuer-metadata document the client validated per RFC
     8414 §3.3). Mirror the agent's "empty = not advertised, present =
     must match" rule
     ([internal/agent/oauth/client.go](../../../internal/agent/oauth/client.go)
     lines 299-318).
   - On mismatch: render the existing error page and abort *before*
     calling `ExchangeCode`, identical to the agent's
     `cancelCurrentFlow()` path.
   - Add an `IssParameterSupported bool` field to
     [pkg/oauth/types.go](../../../pkg/oauth/types.go) `Metadata` so the
     parsed metadata reflects the AS flag and enables a future tightening
     to "reject when absent and advertised".

2. **SEP-837 — `application_type` plumbed through CIMD and (future) DCR.**
   - Add an optional `ApplicationType string` field to
     [pkg/oauth/types.go](../../../pkg/oauth/types.go) `ClientMetadata`.
   - The server-side proxy's CIMD is published at the public muster URL
     ([internal/oauth/client.go](../../../internal/oauth/client.go) lines
     196-210, `Handler.ServeCIMD`
     [internal/oauth/handler.go](../../../internal/oauth/handler.go)
     lines 197-220) — set `application_type: "web"` there.
   - The agent CLI runs against `localhost` callbacks — when (or if) it
     ever performs DCR, it MUST send `application_type: "native"`.
     `cmd/auth_login.go` and `cmd/auth_helpers.go` are the right seam to
     thread that intent through `internal/agent/oauth/client.go`.

3. **SEP-2207 — opportunistic `offline_access` for the server-side proxy.**
   - In
     [internal/oauth/client.go](../../../internal/oauth/client.go)
     `GenerateAuthURL`, after `DiscoverMetadata`, check whether the
     returned `Metadata.ScopesSupported` contains `offline_access`. If
     yes, and the caller did not already include it, append it to the
     scope string. Pure addition; non-OIDC servers are unaffected.
   - Keep the CIMD's `grant_types` advertising `refresh_token` (already
     in place, lines 196-210).
   - When muster grows an inbound `WWW-Authenticate` or Protected
     Resource Metadata path on its own API, never include
     `offline_access` in either field
     (`internal/aggregator/auth_resource.go` /
     `internal/aggregator/auth_tools.go`).

4. **SEP-2350 — scope accumulation in the outbound retry.**
   - Where muster sees an upstream `insufficient_scope` from a remote MCP
     server, look up the existing tokens for that
     `(SessionID, Issuer, *)` via
     [internal/oauth/token_store.go](../../../internal/oauth/token_store.go)
     `TokenStore.GetAllForSession` and union the scopes of any non-expired
     entry with `AuthChallenge.Scope` (parsed from the upstream
     `WWW-Authenticate` per
     [pkg/oauth/www_authenticate.go](../../../pkg/oauth/www_authenticate.go)).
   - Pass that union to `Client.GenerateAuthURL`. The previous scope set
     is already available as a side-product of `TokenKey.Scope`; no
     additional storage is required.
   - Drop or normalise duplicates only on whitespace; do not attempt
     hierarchy collapsing on the client side (the AS handles that — see
     SEP-2350's note).

5. **SEP-2352 — explicit error on AS migration.**
   - In `Manager.RegisterServer`
     ([internal/oauth/manager.go](../../../internal/oauth/manager.go)
     lines 179-196), when an existing entry's `Issuer` does not match
     the incoming `Issuer`, log a warning and call
     `tokenStore.DeleteByIssuer(_, oldIssuer)` for already-issued tokens
     before overwriting. Surface this as a status change on the
     `auth://status` resource
     ([internal/aggregator/auth_resource.go](../../../internal/aggregator/auth_resource.go))
     so users see a clean "re-authentication required" rather than
     silent token loss.
   - Because muster uses CIMD for the `client_id`, no DCR
     re-registration step is needed. Document this explicitly in
     [internal/oauth/doc.go](../../../internal/oauth/doc.go) — it is the
     primary architectural reason the SEP-2352 burden on muster is small.

6. **SEP-2351 — documentation only.**
   - No behavioural change. When updating any user-facing docs that
     mention the well-known suffix
     (`docs/contributing/testing/oauth-testing.md` and decision record
     `docs/explanation/decisions/005-muster-auth.md`), reference
     `oauth-authorization-server` as the default per RFC 8414 §3.1.

7. **Tests and scenarios.**
   - `internal/oauth/handler_test.go` needs cases for: `iss` matches,
     `iss` mismatches, `iss` absent (legacy AS).
   - `internal/server/oauth_http_test.go` checks the externally visible
     OAuth HTTP shape; add expectations for `application_type` in the
     CIMD response.
   - BDD scenarios under `internal/testing/scenarios/oauth-*` already
     cover refresh and multi-issuer flows
     (e.g. `oauth-different-issuers.yaml`,
     `oauth-automatic-token-refresh.yaml`,
     `oauth-token-refresh-flow.yaml`,
     `oauth-sso-shared-issuer.yaml`). The new scenarios needed are:
     `iss` mismatch rejection (server-side), `iss` absence acceptance,
     scope accumulation on step-up. These should follow the existing
     `internal/testing/mock/oauth_server.go` mock pattern.

## 5. Open questions

- **AS migration UX.** When `Manager.RegisterServer` sees an issuer flip
  for an existing remote MCP server, should muster invalidate previously
  stored tokens silently, surface a "reauth required" status on
  `auth://status`, or both? SEP-2352 says "surface an error rather than
  silently attempt to use mismatched credentials" — translating that into
  muster's per-server UI vocabulary needs a product call.
- **Strictness toggle for `iss`.** SEP-2468 explicitly leaves "reject
  responses that omit `iss` from servers that advertise support" as a
  near-future tightening. Should muster ship that strict mode now (behind
  config) so operators can opt in per environment? The data already
  exists (RFC 8414 metadata) but the discovery path
  ([pkg/oauth/client.go](../../../pkg/oauth/client.go) lines 80-165) does
  not yet surface the AS flag through
  [pkg/oauth/types.go](../../../pkg/oauth/types.go) `Metadata`.
- **Scope accumulation across `Scope` keys.** Tokens are stored under
  `TokenKey{SessionID, Issuer, Scope}` — meaning a step-up that asks for
  a wider scope produces a *new* token-store entry rather than
  refreshing the old one. Is that the right model after SEP-2350, or
  should we collapse to `TokenKey{SessionID, Issuer}` and treat scope as
  metadata on the entry?
- **Inbound auth surface.** Muster's own MCP API today returns auth
  status via the `auth://status` resource; it does not advertise
  `WWW-Authenticate` or Protected Resource Metadata on its tools. If
  that changes (per ADR-008 evolution in
  `docs/explanation/decisions/005-muster-auth.md`), SEP-2207 / SEP-2350
  guardrails on what *not* to include need to be applied to whatever
  code path generates those payloads.
- **CIMD-vs-DCR matrix.** SEP-2352 is favourable to muster precisely
  because the proxy uses CIMD. The reverse question is whether muster
  should ever fall back to DCR for authorization servers that refuse to
  resolve a CIMD URL, and if so, where the per-AS DCR state lives. That
  is the only path on which SEP-837 (`application_type`) becomes a
  blocking concern instead of a defensive one.

## 6. References

- 2026-07-28 release-candidate announcement, "Authorization Hardening"
  section: <https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/>
- Draft authorization specification:
  <https://modelcontextprotocol.io/specification/draft/basic/authorization>
- SEP-2468 PR (`iss` validation):
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2468>
- SEP-837 PR (`application_type` in DCR):
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/837>
- SEP-2352 PR (authorization server binding and migration):
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2352>
- SEP-2207 PR (OIDC-flavoured refresh token guidance):
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2207>
- SEP-2350 PR (client-side scope accumulation on step-up):
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2350>
- SEP-2351 PR (RFC 8414 well-known URI suffix):
  <https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2351>
- RFC 9207 "OAuth 2.0 Authorization Server Issuer Identification":
  <https://www.rfc-editor.org/rfc/rfc9207>
- RFC 8414 "OAuth 2.0 Authorization Server Metadata":
  <https://datatracker.ietf.org/doc/html/rfc8414>
- RFC 9728 "OAuth 2.0 Protected Resource Metadata":
  <https://datatracker.ietf.org/doc/html/rfc9728>
- RFC 7591 "OAuth 2.0 Dynamic Client Registration Protocol":
  <https://datatracker.ietf.org/doc/html/rfc7591>
- RFC 6750 §3.1 (`scope` attribute on `WWW-Authenticate`):
  <https://datatracker.ietf.org/doc/html/rfc6750#section-3.1>
- RFC 6749 §2.2 (client identifier uniqueness per AS):
  <https://datatracker.ietf.org/doc/html/rfc6749#section-2.2>
- OpenID Connect Dynamic Client Registration 1.0:
  <https://openid.net/specs/openid-connect-registration-1_0.html>
- OAuth 2.0 Security Best Current Practice (mix-up attack class):
  <https://datatracker.ietf.org/doc/html/draft-ietf-oauth-security-topics>
