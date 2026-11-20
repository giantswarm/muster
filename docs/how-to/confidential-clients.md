# Confidential OAuth clients

Register each integration as a separate client of muster's OAuth server. Its
client ID identifies the integration; its access-token session identifies the
person and grant. Request shape, user agent and scope names do not authenticate
an integration.

Set `aggregator.oauth.server.allowPublicClientRegistration: false` and provide
`registrationTokenFile`. Registration requires the provisioning bearer token
unless a configured trusted redirect URI or scheme authorizes the request.
Use HTTPS redirects in production and PKCE S256 with authorization code grants.

Enable `aggregator.oauth.server.enableClientManagementEndpoint` to manage
registrations through RFC 7592. The chart value is
`muster.oauth.server.enableClientManagementEndpoint`; it defaults to false.
Store the registration response's client secret, registration access token and
registration client URI in a secret manager. Management authenticates with the
registration access token, independently of the provisioning token and client
secret.

For rotation, provision a replacement client, configure and verify the
integration with it, then DELETE the old registration client URI using its
registration access token. Successful deletion returns 204. Users authorize the
replacement client separately. Verify the old access and refresh tokens fail.

Dynamic registrations live in OAuth storage. After complete Valkey data loss,
register a new client, replace the integration's credentials and obtain fresh
user authorizations. Restoring a snapshot can lose registrations and grants
created after that snapshot. A PVC alone does not guarantee that recent writes
have been persisted.
