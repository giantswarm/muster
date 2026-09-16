package mcpserver

import "github.com/giantswarm/muster/v5/internal/mcpserver"

// machineIdentityUnauthorizedError is a 401 from a server whose auth type has
// no interactive login (a machine identity such as SigV4). It carries the
// AuthRequiredError's message for the status and the failure event, but not
// its marker: there is no user to send to a login flow, so no caller that
// classifies a start result with api.IsAuthRequiredError may take the server
// for one awaiting a sign-in. The underlying transport error stays reachable
// through Unwrap.
type machineIdentityUnauthorizedError struct {
	authErr *mcpserver.AuthRequiredError
}

func (e *machineIdentityUnauthorizedError) Error() string {
	return e.authErr.Error()
}

func (e *machineIdentityUnauthorizedError) Unwrap() error {
	return e.authErr.Err
}
