// Package valkeyclient builds the Valkey clients muster's stores share: the
// aggregator's session auth and capability stores and the OAuth server's DPoP
// replay cache. The OAuth server's token store applies the same bound through
// mcp-oauth's own OperationTimeout.
//
// Every command runs under OperationTimeout. Without it a command waits as long
// as its caller's context lives: valkey-go retries a read with backoff while
// the connection is down, and on a request context that is the whole request.
// An authenticated request mirrors its login token and touches the session
// stores before it is served, so a Valkey that restarts would hold every
// request -- `initialize` included -- until Valkey answered again.
//
// A command that fails to reach Valkey also fails the commands after it at
// once for a short while: a request sends several in a row, and without that
// their timeouts would add up. Bounded, a request answers without what Valkey
// would have added, or fails fast where it cannot do without Valkey.
package valkeyclient
