// Package credprovider makes muster a credential provider of Substrate's
// egress gateway, so a workspace Session's git and gh act as the person who
// started it without the person's token ever reaching the sandbox.
//
// Two surfaces:
//
//   - Session bindings, on muster's authenticated HTTP port: agent-manager
//     calls PUT /session-bindings as the person (their forwarded ID token)
//     with the Session's sandbox actor SPIFFE id and the providers the Session
//     may use, and DELETE /session-bindings?actor=<id> when the Session goes.
//     muster binds only the actor of a Session the kagent controller answers
//     to the caller's own bearer. A binding belongs to the person who made
//     it; nobody else can replace or remove it, and it is pinned to the uid
//     of the first actor JWT accepted under it. It lives for the Session idle TTL, restarted by every bind
//     and every answered fetch, in Valkey when muster runs on Valkey.
//   - CredentialProvider.FetchSecret, on a gRPC listener of its own that
//     requires the egress gateway's pod certificate (mutual TLS, one SPIFFE
//     id). For ate-secret://<authority>/<provider>/<git|api> and the actor the
//     gateway verified, it answers the bound person's current access token
//     of the provider's grant -- base64 of x-access-token:<token> for git
//     (the gateway adds "Basic "), the token itself for the API (it adds
//     "Bearer ") -- when the actor is bound, the provider is in the binding
//     and on the configured releasable list. The refresh token never leaves
//     muster. Every answer and every refusal is logged.
//
// The grant itself is muster's OAuth proxy grant (grantScope: subject),
// looked up and refreshed through the GrantSource the aggregator wires in.
package credprovider
