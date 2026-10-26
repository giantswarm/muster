package aggregator

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/giantswarm/muster/v5/internal/api"
)

// A server outside muster's namespace that the caller sees, and whose
// sign-in cannot start yet, is answered as such by core_auth_login: the
// listings (core_mcpserver_list, list_tools) show it, so "not found" would
// contradict them. The commonest reason is a pin whose client Secret muster
// may not read: a tenant pin's Secret stays in the tenant's namespace, and
// muster reads it only through a Role there that gives its ServiceAccount
// get on that one Secret (D9).

// signInNotReadyMessage is core_auth_login's answer for a server whose
// sign-in cannot start yet.
func signInNotReadyMessage(serverName string, reason error) string {
	return fmt.Sprintf("Cannot sign in to '%s': the server's sign-in is not ready: %v", serverName, reason)
}

// tenantSignInNotReady returns why the sign-in of a tenant server that has
// no registry entry yet cannot start, or nil when there is no definition of
// it (the server does not exist). The reason is the pin Secret muster cannot read,
// else the definition's last error, else its state.
func tenantSignInNotReady(ctx context.Context, serverName string) error {
	manager := api.GetMCPServerManager()
	if manager == nil {
		return nil
	}
	definition, err := manager.GetMCPServer(ctx, serverName)
	if err != nil || definition == nil {
		return nil
	}
	if err := pinSecretReadable(ctx, serverName, definition); err != nil {
		return err
	}
	if definition.Error != "" {
		return fmt.Errorf("%s", definition.Error)
	}
	state := definition.State
	if state == "" {
		state = "unknown"
	}
	return fmt.Errorf("muster has not connected to it yet (state: %s)", state)
}

// pinSecretReadable reads the client Secret a definition's pin names, only
// to report why it cannot be read: nothing is pinned here, so no pin
// outlives a definition that goes before it registers.
func pinSecretReadable(ctx context.Context, serverName string, definition *api.MCPServerInfo) error {
	as := authorizationServerOf(definition.Auth)
	if as == nil || as.ClientCredentialsSecretRef == nil {
		return nil
	}
	secrets := api.GetSecretCredentialsHandler()
	if secrets == nil {
		return nil
	}
	info := &ServerInfo{Name: serverName, Namespace: definition.Namespace, UID: definition.UID}
	namespace := info.GetNamespace()
	if _, err := secrets.LoadMCPServerClientCredentials(ctx, info.SecretOwner(), api.SecretFieldAuthorizationServer, as.ClientCredentialsSecretRef); err != nil {
		return fmt.Errorf("load client credentials for server %s: %w", serverName,
			tenantSecretReadError(serverName, namespace, as.ClientCredentialsSecretRef.Name, err))
	}
	return nil
}

// tenantSecretReadError explains a client Secret of a tenant server that
// muster may not read: what grants the read, and nothing more.
func tenantSecretReadError(serverName, namespace, secret string, err error) error {
	if !api.IsTenantMCPServerKey(serverName) || !apierrors.IsForbidden(err) {
		return err
	}
	return fmt.Errorf("muster may not read the Secret %s/%s of the pin: give muster's ServiceAccount get on that Secret alone "+
		"(a Role in %s with resourceNames: [%s] and a RoleBinding to it, see \"Servers outside muster's namespace\" in muster's docs): %w",
		namespace, secret, namespace, secret, err)
}
