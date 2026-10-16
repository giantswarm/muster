package cmd

import (
	"fmt"

	"github.com/giantswarm/muster/v5/internal/api"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"

	"github.com/spf13/cobra"
)

// Token-specific flags
var (
	tokenID bool
)

// authTokenCmd represents the auth token command
var authTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Print the current token for use with other clients",
	Long: `Print the access token of the current aggregator session, or with --id the
OIDC ID token the identity provider issued at sign-in.

Only the token is written to stdout, so it can be handed to another client
directly:

  curl -H "Authorization: Bearer $(muster auth token --id)" https://models.example.com/v1/models

A valid stored access token is printed without contacting the aggregator; an
expired one is renewed through the session first. The ID token is
never renewed that way: when it has expired the command fails and names
'muster auth login', which signs in again.

Examples:
  muster auth token                    # Access token of the aggregator session
  muster auth token --id               # OIDC ID token from the sign-in
  muster auth token --id --endpoint <url>`,
	RunE: runAuthToken,
}

func init() {
	authTokenCmd.Flags().BoolVar(&tokenID, "id", false, "Print the OIDC ID token instead of the access token")
}

func runAuthToken(cmd *cobra.Command, args []string) error {
	handler, err := ensureAuthHandler()
	if err != nil {
		return err
	}

	endpoint := authEndpoint
	if endpoint == "" {
		endpoint, err = getEndpointFromConfig()
		if err != nil {
			return err
		}
	}

	if tokenID {
		idToken, err := handler.GetIDToken(endpoint)
		if err != nil {
			return err
		}
		if expired, err := pkgoauth.IsExpired(idToken); expired {
			if err != nil {
				return fmt.Errorf("the stored ID token for %s is unusable: %w", endpoint, err)
			}
			return fmt.Errorf("the ID token for %s has expired; run 'muster auth login' to sign in again", endpoint)
		}
		fmt.Println(idToken)
		return nil
	}

	accessToken, err := currentAccessToken(handler, endpoint, func() error {
		return tryMCPConnection(cmd.Context(), handler, endpoint)
	})
	if err != nil {
		return err
	}
	fmt.Println(accessToken)
	return nil
}

// currentAccessToken is the stored access token of endpoint, renewed first when
// it is no longer valid. A valid stored token is answered without a round trip
// to the aggregator: a caller that asks for the token before every request
// does not depend on the network or a gateway in front of muster for it. Only
// a renewal connects, and the connection lets the mcp-go transport refresh the
// token.
func currentAccessToken(handler api.AuthHandler, endpoint string, renew func() error) (string, error) {
	if token, err := handler.GetAccessToken(endpoint); err == nil {
		return token, nil
	}
	if err := renew(); err != nil {
		if pkgoauth.IsOAuthUnauthorizedError(err) {
			return "", fmt.Errorf("not authenticated to %s; run 'muster auth login' first", endpoint)
		}
		return "", fmt.Errorf("failed to connect to %s: %w", endpoint, err)
	}
	return handler.GetAccessToken(endpoint)
}
