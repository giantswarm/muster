package mcpserver

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/callerwrite"
)

// The writes of clientCredentials (client_credentials.go). A Secret is
// created before any spec names it: a spec that named a Secret not yet there
// would let anyone who reads the name create it first.

// describeSecretWriteError explains a failed Secret write as the caller,
// naming the missing permission where the API server refused it.
func (a *Adapter) describeSecretWriteError(err error, name string) string {
	if msg := callerwrite.DescribeWriteAuthError(err, "create", "Secret", "secrets", name, a.namespace, "mcpserver-editor"); msg != "" {
		return msg
	}
	if errors.IsAlreadyExists(err) {
		return fmt.Sprintf("Secret %q already exists in namespace %q; retry to generate another name", name, a.namespace)
	}
	return fmt.Sprintf("failed to create Secret %q in namespace %q: %v", name, a.namespace, err)
}

// createClientSecret creates, as the caller, the Secret holding a server's
// client under a newly generated name, owned by the server. A name another
// Secret already has is generated once more. It returns the name it tried
// last.
func createClientSecret(ctx context.Context, secrets secretWriter, server *musterv1alpha1.MCPServer,
	creds *api.MCPServerClientCredentials) (string, error) {
	var name string
	var err error
	for range 2 {
		name, err = newGeneratedClientSecretName(server.Name)
		if err != nil {
			return "", err
		}
		err = secrets.CreateSecret(ctx, clientCredentialsSecret(name, server, creds))
		if !errors.IsAlreadyExists(err) {
			return name, err
		}
	}
	return name, err
}

// nameClientSecret points the authorizationServer of a server just created at
// its client Secret, as the caller, on the server's current version. It
// refuses when the server was replaced or its authorization server changed
// in the meantime: the Secret was written for this one.
func (a *Adapter) nameClientSecret(ctx context.Context, writer mcpServerWriter, server *musterv1alpha1.MCPServer, name string) error {
	destination := destinationDigest(authorizationServerOf(&server.Spec))
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := a.client.GetMCPServer(ctx, server.Name, server.Namespace)
		if err != nil {
			return err
		}
		as := authorizationServerOf(&current.Spec)
		if current.UID != server.UID || as == nil || destinationDigest(as) != destination {
			return fmt.Errorf("the MCP server was changed while its client was stored")
		}
		as.ClientCredentialsSecretRef = &musterv1alpha1.ClientCredentialsSecretRef{Name: name}
		return writer.UpdateMCPServer(ctx, current)
	})
}

// storeClientCredentialsOnCreate stores the client of a server just created
// without a client Secret reference: it creates the Secret, then names it in
// the server. When either fails it deletes the server again, so no connector
// is left without its client and without a message; the answer says when
// that delete fails too. It returns the Secret's name, or the tool error.
func (a *Adapter) storeClientCredentialsOnCreate(ctx context.Context, writer mcpServerWriter, secrets secretWriter,
	server *musterv1alpha1.MCPServer, creds *api.MCPServerClientCredentials) (string, string) {
	name, cause := a.storeClientSecret(ctx, writer, secrets, server, creds)
	if cause == "" {
		return name, ""
	}
	if err := writer.DeleteMCPServer(ctx, server.Name, server.Namespace); err != nil && !errors.IsNotFound(err) {
		return "", fmt.Sprintf("Cannot store the client credentials of MCP server '%s': %s. Removing the server failed too, so it is left behind without its client: delete MCP server '%s' (%v)",
			server.Name, cause, server.Name, err)
	}
	return "", fmt.Sprintf("Cannot store the client credentials of MCP server '%s', so it was not created: %s", server.Name, cause)
}

// storeClientSecret creates a new server's client Secret and names it in the
// server. It returns the Secret's name, or why it failed.
func (a *Adapter) storeClientSecret(ctx context.Context, writer mcpServerWriter, secrets secretWriter,
	server *musterv1alpha1.MCPServer, creds *api.MCPServerClientCredentials) (string, string) {
	if server.UID == "" {
		return "", "the API server returned no UID for the new MCP server"
	}
	name, err := createClientSecret(ctx, secrets, server, creds)
	if err != nil {
		return "", a.describeSecretWriteError(err, name)
	}
	if err := a.nameClientSecret(ctx, writer, server, name); err != nil {
		if msg := a.describeWriteAuthError(err, "update", server.Name); msg != "" {
			return "", msg
		}
		return "", fmt.Sprintf("failed to name Secret %q in the MCP server: %v", name, err)
	}
	return name, ""
}

// checkClientCredentials checks clientCredentials against the spec the
// request results in. It returns the caller's Secret writer, or a tool
// error.
func checkClientCredentials(writer mcpServerWriter, creds *api.MCPServerClientCredentials,
	server *musterv1alpha1.MCPServer, explicitRef bool) (secretWriter, *api.CallToolResult) {
	if err := validateClientCredentials(creds, authorizationServerOf(&server.Spec), explicitRef); err != nil {
		result, _ := simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
		return nil, result
	}
	secrets, ok := writer.(secretWriter)
	if !ok {
		result, _ := simpleError("clientCredentials need muster in Kubernetes mode: muster stores them in a Secret")
		return nil, result
	}
	return secrets, nil
}

// replaceClientCredentials stores the new client of an existing server in a
// new Secret owned by it and points the server's spec at it; the caller
// writes the spec. The previous Secret stays until the server is deleted:
// editors may not delete Secrets.
func (a *Adapter) replaceClientCredentials(ctx context.Context, secrets secretWriter, server *musterv1alpha1.MCPServer,
	creds *api.MCPServerClientCredentials) (string, *api.CallToolResult) {
	if server.UID == "" {
		result, _ := simpleError(fmt.Sprintf("Cannot store the client credentials of MCP server '%s': it has no UID to own the Secret", server.Name))
		return "", result
	}
	name, err := createClientSecret(ctx, secrets, server, creds)
	if err != nil {
		result, _ := simpleError(fmt.Sprintf("Cannot store the client credentials of MCP server '%s', so it was not updated: %s",
			server.Name, a.describeSecretWriteError(err, name)))
		return "", result
	}
	authorizationServerOf(&server.Spec).ClientCredentialsSecretRef = &musterv1alpha1.ClientCredentialsSecretRef{Name: name}
	return name, nil
}

// refuseMovedClient refuses an update that changes where the stored client of
// a server is sent (the issuer or an endpoint of its authorizationServer)
// while keeping its generated Secret: muster would not read it there
// (checkGeneratedOwner). The client must be entered again.
func refuseMovedClient(server *musterv1alpha1.MCPServer, previous *musterv1alpha1.MCPServerAuthAuthorizationServer) *api.CallToolResult {
	as := authorizationServerOf(&server.Spec)
	if as == nil || as.ClientCredentialsSecretRef == nil {
		return nil
	}
	ref := as.ClientCredentialsSecretRef
	if (ref.Namespace != "" && ref.Namespace != server.Namespace) || !isGeneratedClientSecretName(server.Name, ref.Name) {
		return nil
	}
	if destinationDigest(as) == destinationDigest(previous) {
		return nil
	}
	result, _ := simpleError(fmt.Sprintf("Cannot update MCP server '%s': its stored client belongs to the previous issuer and endpoints of auth.authorizationServer; re-enter it with clientCredentials", server.Name))
	return result
}
