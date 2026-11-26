# HTTP endpoints

muster's programmatic interface is the Model Context Protocol; there is no separate REST API.
This page lists the HTTP paths the aggregator serves, which of them exist only with OAuth
enabled, and the headers that matter.

## Aggregator

Served on `aggregator.host:aggregator.port` (`localhost:8090` for the binary, `0.0.0.0:8090` in
the chart).

| Path | Method | Purpose |
|---|---|---|
| `/mcp` | `POST`, `GET`, `DELETE` | MCP over streamable HTTP: JSON-RPC requests, the notification stream and session termination |
| `/sse` | `GET` | The older SSE transport: the event stream |
| `/message` | `POST` | The older SSE transport: JSON-RPC requests |
| `/health` | `GET` | `{"status":"ok"}` with status 200, without authentication, for the liveness probe; while OIDC discovery is pending also 200, with `{"status":"degraded"}` |
| `/readyz` | `GET` | `{"status":"ok"}` with status 200, without authentication, for the readiness probe; 503 while OIDC discovery is pending |

A client connects to `/mcp`, initialises an MCP session and receives the meta-tools. With OAuth
enabled, an unauthenticated request is answered with `401` and a `WWW-Authenticate` header that
points at the protected-resource metadata, as the MCP authorization specification requires.

```bash
curl -s -X POST http://localhost:8090/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'
```

### Request headers

| Header | Meaning |
|---|---|
| `Authorization: Bearer <token>` | The caller's access token when OAuth protection is on |
| `Mcp-Session-Id` | The MCP session, issued by muster in the `initialize` response and sent back on every request |
| `X-muster-Toolset` | The [toolset](toolsets.md) the request works within, for example `preset:read-only` or `server:kubernetes` |
| `MCP-Protocol-Version` | The protocol revision the client speaks; muster negotiates `2025-11-25` and earlier revisions |

## OAuth server

Present when `oauth.server.enabled` is `true`. muster acts as the OAuth 2.1 authorization
server towards its clients and delegates the sign-in to Dex; these endpoints are provided by
[mcp-oauth](https://github.com/giantswarm/mcp-oauth).

| Path | Purpose |
|---|---|
| `/.well-known/oauth-protected-resource` | Protected-resource metadata (RFC 9728): where clients find the authorization server |
| `/.well-known/oauth-authorization-server` | Authorization-server metadata (RFC 8414): endpoints, supported grants, PKCE methods |
| `/.well-known/jwks.json` | Not served: muster issues opaque access tokens, which have no signing key, so this path answers `404` and the authorization-server metadata has no `jwks_uri`. muster checks a token it issued by looking it up in its own token store; to check one from elsewhere, call `/oauth/introspect` |
| `/oauth/authorize` | Start of the authorization-code flow (PKCE required) |
| `/oauth/callback` | Return from Dex |
| `/oauth/token` | Token issuance and refresh; RFC 8693 token exchange for configured broker clients |
| `/oauth/register` | Dynamic client registration (RFC 7591), gated by the registration token or the public-registration settings |
| `/oauth/revoke`, `/oauth/introspect`, `/oauth/userinfo` | Token revocation, introspection and the identity of the token's subject |

## OAuth client

Present when `oauth.mcpClient.enabled` is `true`: muster logs a person in to remote MCP servers
that run their own OAuth.

| Path | Purpose |
|---|---|
| `/.well-known/oauth-client.json` | muster's client metadata document (CIMD), the `client_id` it presents to authorization servers that support it |
| `/oauth/proxy/start` | Starts a login to a remote server; `core_auth_login` returns URLs pointing here. When muster runs its own OAuth server, the browser first signs in to muster's identity provider and comes back here: the login continues only when the person who signed in is the person the link was issued to (same issuer and `sub` as the session's token; with the `dex` provider the browser signs in through the Dex connector that `sub` was issued through when `dex.identityCheckConnectors` lists it, otherwise through `dex.connectorId`), and only in that browser (`__Host-muster_oauth_browser` cookie, `muster_oauth_browser` over http, required again on the callback), so a forwarded link connects nobody else's account. An optional `redirect` parameter, checked against `postLoginRedirectAllowlist`, sends the browser on after the login |
| `/oauth/proxy/callback` | Return from the remote authorization server (`oauth.mcpClient.callbackPath`) for the servers in muster's namespace |
| `/oauth/ns/<namespace>/callback` | Return from the remote authorization server for the servers of `<namespace>` (with `mcpServerNamespaceSelector`); a flow is accepted only on the callback of its own namespace |

## Credential provider

With [`aggregator.credentialProvider`](configuration.md#credential-provider-for-substrates-egress-gateway) enabled:

| Path or service | Where | Purpose |
|---|---|---|
| `/session-bindings` `PUT` | session bindings port (`:8091`; on the aggregator port too while `credentialProvider.sessionBindings.onMainPort`, deprecated), bearer required | Binds a Session's sandbox actor to the caller: `{"actor", "session", "providers", "toolset", "agent", "approval"}` (`toolset` optional: the [toolset bound to the Session](toolsets.md#the-toolset-bound-to-a-session)); the binding pins the session the bearer came from, whose login ID token the actor's requests carry. `agent` (`<namespace>/<name>`) and `approval` (`{"entries": [{"entry", "setting"}], "default", "grants": [{"entry", "server", "uid"}]}`, [an agent's approval settings](mcp-tools.md#an-agents-approval-settings); each entry also in `toolset`) are taken only with `X-Muster-Approval-Sender` passing the sender review, and dropped (logged `approval_settings_ignored`) without it. The answer's `X-Muster-Approval-Settings` says `bound`, `ignored`, `none` or, on a refused bind, `refused`. A binding written by an approval sender is replaced only by that same sender: without one 409, by another sender 403. Body at most 64 KiB; 204, 400, 403 (bound to another person, or the sender refused), 409, 503 (the review failed) |
| `/session-bindings/grants` `PATCH` | session bindings port (see `PUT`), no bearer; `X-Muster-Approval-Sender` required and reviewed before the body is read | Removes grant marks from every live binding of an agent: `{"agent": "<namespace>/<name>", "keep": [{"entry", "server", "uid", "tools"}]}` (at most 256 KiB); each mark not in `keep` goes, nothing else changes (person, Session, settings, expiry), whoever bound the Session. 200 `{"bindings", "dropped"}`, 400, 403 (no sender, or refused), 503 (review or store failed) |
| `/session-bindings?actor=<id>` `DELETE` | session bindings port (see `PUT`), bearer required | Removes the caller's binding; a binding an approval sender wrote only with that sender's `X-Muster-Approval-Sender`. After a sender-written binding ends (removed or expired) the actor stays sealed for two binding TTLs: a bind of it without that sender is refused 409. 204, 403 (another person's, or another sender), 409 (sender-written, no sender) |
| `credprovider.CredentialProvider/FetchSecret` | gRPC, `listenAddress` (`:50051`), mutual TLS | The egress gateway's credential lookup; answers only the gateway's certificate |

## Metrics

muster's own Prometheus exporter is switched on with the standard OpenTelemetry environment
variables: `OTEL_METRICS_EXPORTER=prometheus` serves `/metrics` on
`OTEL_EXPORTER_PROMETHEUS_HOST:OTEL_EXPORTER_PROMETHEUS_PORT` (`localhost:9464` by default). The
chart sets them from `muster.observability.metrics` and, with `serviceMonitor.enabled`, scrapes
the port. [Observability](../explanation/observability.md) lists the metrics.

## Admin listener

`aggregator.admin.enabled: true` starts a small web UI for sessions on its own listener,
`127.0.0.1:9999` by default. It has no authentication of its own; keep it on the loopback
address and reach it with `kubectl port-forward`.

| Path | Purpose |
|---|---|
| `/`, `/sessions` | List of sessions with identity, age and connected servers |
| `/sessions/{id}` | One session in detail |
| `/mcps`, `/mcps/{name}` | Registered servers and their state |
| `POST /sessions/{id}/delete` | End a session |
| `POST /sessions/{id}/servers/{name}/reconnect` | Reconnect one server for one session |
| `DELETE /auth/{server}` | Drop the stored grants for a server |
| `DELETE /user-tokens` | Drop every stored user token |

## Related

- [MCP tools](mcp-tools.md): what a client finds behind `/mcp`.
- [Configuration](configuration.md): the keys that switch these surfaces on.
- [Security](../operations/security.md): the token lifecycle behind the OAuth endpoints.
