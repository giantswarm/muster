# Confidential OAuth clients

Use a muster release containing the corrected client lifecycle from
[mcp-oauth #745](https://github.com/giantswarm/mcp-oauth/pull/745). Upgrade every
replica before relying on immediate deletion. Private-key assertion replay
across replicas also requires the integration in
[muster #1889](https://github.com/giantswarm/muster/pull/1889). Client management
includes that integration so enabling it cannot expose process-local replay
protection on a replicated deployment.

Register each integration as a separate client of muster's OAuth server. Its
client ID identifies the integration; its access-token session identifies the
person and grant. Request shape, user agent and scope names do not authenticate
an integration.

Set `aggregator.oauth.server.allowPublicClientRegistration: false` and provide
`registrationTokenFile`. Registration requires the provisioning bearer token
unless a configured trusted redirect URI or scheme authorizes the request.
Use HTTPS redirects in production and PKCE S256 with authorization code grants.

Set `aggregator.oauth.server.resourceIdentifier` to the canonical MCP resource
URI, including `/mcp`, before provisioning clients. Verify this matches the
`resource` in protected-resource metadata. MCP clients send that value in both
authorization and token requests. A client omitting `resource` can use the
server's default resource, but that client flow does not meet MCP's resource
indicator requirements.

Enable `aggregator.oauth.server.enableClientManagementEndpoint` to manage
registrations through RFC 7592. The chart value is
`muster.oauth.server.enableClientManagementEndpoint`; it defaults to false.
Store the registration response's client secret, registration access token and
registration client URI in a secret manager. Management authenticates with the
registration access token, independently of the provisioning token and client
secret.

For `private_key_jwt`, use the authorization-server metadata's `issuer` as the
assertion's sole audience. The token endpoint URL and additional audiences are
rejected. Use a fresh `jti` for every request, including retries, and keep every
replica on the shared Valkey replay store. Assertions authenticate the client;
PKCE and the user's authorization grant still protect code exchange.

For rotation, provision a replacement client, configure and verify the
integration with it, then DELETE the old registration client URI using its
registration access token. Successful deletion returns 204. Users authorize the
replacement client separately. Verify the old access and refresh tokens fail.
Deletion permanently retires the client ID and invalidates existing access
tokens. Client deletion markers are checked on each request
and shared through Valkey, so all replicas must run a version supporting this
check before relying on immediate invalidation. Other clients belonging to the
same person retain their grants and shared upstream identity-provider tokens.

Dynamic registrations live in OAuth storage. After complete Valkey data loss,
register a new client, replace the integration's credentials and obtain fresh
user authorizations. Restoring a snapshot can lose registrations and grants
created after that snapshot and can restore registrations retired since then.
Retirement markers must be preserved with the rest of OAuth storage. A PVC
alone does not guarantee that recent writes have been persisted.

A complete Valkey wipe removes muster's opaque grants together with their
storage records. After restoring a snapshot, close protected traffic until the
restored registrations and grants have been checked against the intended state.
