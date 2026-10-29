package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/giantswarm/muster/v5/internal/api"
)

// pinSecretError reports that muster refuses the client Secret of the
// server's spec.auth.authorizationServer: it is one muster keeps for itself,
// the admission policy on Secret references is not in force, or it has a
// generated client name and the server does not own it or it was stored for
// another authorization server. The pin is
// never applied, so no sign-in can succeed: the server is Failed with the
// refusal, retried with backoff, and reads Auth Required once the spec names
// another Secret or the policy is in force.
type pinSecretError struct {
	err error
}

func (e *pinSecretError) Error() string {
	return fmt.Sprintf("authorizationServer client credentials refused (%v)", e.err)
}

func (e *pinSecretError) Unwrap() error { return e.err }

// refusedPinSecret reads the client Secret of spec.auth.authorizationServer
// the way the aggregator's pin does and returns a pinSecretError when muster
// refuses it. A Secret that is merely missing or unreadable is not reported
// here: the pin is deferred and every login retries it, as before.
func (s *Service) refusedPinSecret(ctx context.Context) error {
	auth := s.definition.Auth
	if auth == nil || auth.AuthorizationServer == nil || auth.AuthorizationServer.ClientCredentialsSecretRef == nil {
		return nil
	}
	handler := api.GetSecretCredentialsHandler()
	if handler == nil {
		return nil
	}
	_, err := handler.LoadMCPServerClientCredentials(ctx, s.secretOwner().ForAuthorizationServer(auth.AuthorizationServer), api.SecretFieldAuthorizationServer,
		auth.AuthorizationServer.ClientCredentialsSecretRef)
	if errors.Is(err, api.ErrSecretProtected) || errors.Is(err, api.ErrSecretReferencePolicyInactive) ||
		errors.Is(err, api.ErrSecretNotOwned) || errors.Is(err, api.ErrSecretOtherAuthorizationServer) {
		return &pinSecretError{err: err}
	}
	return nil
}

// conflictingAuthorizationServer returns the aggregator's refusal of the
// server's spec.auth.authorizationServer when an older MCPServer of its
// namespace describes the same issuer differently
// (api.ErrAuthorizationServerConflict). The server is Failed with it and
// retried with backoff, and reads Auth Required once the descriptions match
// or the other MCPServer goes.
func (s *Service) conflictingAuthorizationServer() error {
	checker, ok := api.GetAggregator().(api.AuthorizationServerConflictChecker)
	if !ok {
		return nil
	}
	return checker.AuthorizationServerConflict(s.GetName())
}
