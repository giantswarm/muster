package cmd

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/cli"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// signIn signs in to the aggregator at endpoint and returns nil only once
// verify, a call to the server, accepts the session. Without force it first
// reuses what Login judges usable (a stored token, a refresh, a silent
// re-authentication); Login judges a stored token by its expiry alone, so when
// the server still rejects the session -- it dropped it before the stored
// expiry -- signIn runs the interactive flow as force does. A session the
// server does not accept after the flow is a *cli.AuthFailedError.
func signIn(ctx context.Context, handler api.AuthHandler, endpoint string, force bool, verify func() error) error {
	if !force {
		if err := handler.Login(ctx, endpoint); err != nil {
			return err
		}
		err := verify()
		if !pkgoauth.IsOAuthUnauthorizedError(err) {
			return notSignedIn(endpoint, err)
		}
		authPrint("%s rejected the stored session; signing in again\n", endpoint)
	}
	if err := handler.Relogin(ctx, endpoint); err != nil {
		return err
	}
	return notSignedIn(endpoint, verify())
}

// notSignedIn turns the error of the call that verified a sign-in into an
// authentication failure for endpoint; nil stays nil.
func notSignedIn(endpoint string, err error) error {
	if err == nil {
		return nil
	}
	return &cli.AuthFailedError{
		Endpoint: endpoint,
		Reason:   fmt.Errorf("the server does not accept the session: %w", err),
	}
}
