package cmd

import (
	"fmt"

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

An expired access token is renewed through the session first. The ID token is
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

	// Connecting first lets the mcp-go transport renew an expired access token.
	if err := tryMCPConnection(cmd.Context(), handler, endpoint); err != nil {
		if pkgoauth.IsOAuthUnauthorizedError(err) {
			return fmt.Errorf("not authenticated to %s; run 'muster auth login' first", endpoint)
		}
		return fmt.Errorf("failed to connect to %s: %w", endpoint, err)
	}
	accessToken, err := handler.GetAccessToken(endpoint)
	if err != nil {
		return err
	}
	fmt.Println(accessToken)
	return nil
}
