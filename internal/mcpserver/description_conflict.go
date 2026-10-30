package mcpserver

import (
	"fmt"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

// refuseDescriptionConflict refuses, before anything is written, a server
// whose spec.auth.authorizationServer describes an issuer differently from
// another MCPServer that pins under the same issuer key: the server would
// be saved and then sit Failed (api.ErrAuthorizationServerConflict).
// newClient says the request stores a client of its own (clientCredentials),
// which no other MCPServer can share.
func (a *Adapter) refuseDescriptionConflict(server *musterv1alpha1.MCPServer, verb string, newClient bool) *api.CallToolResult {
	checker, ok := api.GetAggregator().(api.AuthorizationServerDescriptionChecker)
	if !ok {
		return nil
	}
	auth := convertCRDAuthToAPI(server.Spec.Auth)
	if auth == nil || auth.AuthorizationServer == nil {
		return nil
	}
	if newClient {
		auth.AuthorizationServer.ClientCredentialsSecretRef = &api.ClientCredentialsSecretRef{
			Name: generatedClientSecretPrefix(server.Name) + "new",
		}
	}
	key := api.MCPServerKey(server.Namespace, server.Name, a.namespace)
	if err := checker.DescriptionConflict(key, auth); err != nil {
		result, _ := simpleError(fmt.Sprintf("Cannot %s MCP server '%s': %v. One issuer, one client: use an existing Secret with that server's client, or another issuer",
			verb, server.Name, err))
		return result
	}
	return nil
}
