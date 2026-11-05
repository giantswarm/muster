package mcpserver

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
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

// errServerChanged reports that a server just created was replaced, or its
// authorization server changed, before its client Secret was named in it.
var errServerChanged = fmt.Errorf("it was replaced or its auth.authorizationServer changed while its client was stored")

// prepareCreateClient checks the clientCredentials of a create and makes the
// new server start stopped (spec.suspended): it has no client until
// nameClientSecret names its Secret and releases it, so no sign-in runs
// without the client. It returns the caller's Secret writer (nil without
// clientCredentials), or a tool error.
func prepareCreateClient(writer mcpServerWriter, req *api.MCPServerCreateRequest, server *musterv1alpha1.MCPServer) (secretWriter, *api.CallToolResult) {
	if req.ClientCredentials == nil {
		return nil, nil
	}
	secrets, refused := checkClientCredentials(writer, req.ClientCredentials, server, requestNamesClientSecretRef(req.Auth))
	if refused != nil {
		return nil, refused
	}
	server.Spec.Suspended = true
	return secrets, nil
}

// nameClientSecret points the authorizationServer of a server just created at
// its client Secret and releases the server (prepareCreateClient), as the
// caller, on the server's current version. It returns errServerChanged when
// the server was replaced or its authorization server changed in the
// meantime: the Secret was written for this one.
func (a *Adapter) nameClientSecret(ctx context.Context, writer mcpServerWriter, server *musterv1alpha1.MCPServer, name string) error {
	destination := destinationDigest(authorizationServerOf(&server.Spec))
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := a.client.GetMCPServer(ctx, server.Name, server.Namespace)
		if err != nil {
			return err
		}
		as := authorizationServerOf(&current.Spec)
		if current.UID != server.UID || as == nil || destinationDigest(as) != destination {
			return errServerChanged
		}
		as.ClientCredentialsSecretRef = &musterv1alpha1.ClientCredentialsSecretRef{Name: name}
		current.Spec.Suspended = false
		return writer.UpdateMCPServer(ctx, current)
	})
}

// storeClientCredentialsOnCreate stores the client of a server just created
// without a client Secret reference: it creates the Secret, then names it in
// the server. When either fails it deletes the server again, only while it
// is the one created, so no connector is left without its client and
// without a message; the answer says when that delete fails too. A server
// changed in the meantime (errServerChanged) is someone else's edit and is
// left as it is. It returns the Secret's name, or the tool error.
func (a *Adapter) storeClientCredentialsOnCreate(ctx context.Context, writer mcpServerWriter, secrets secretWriter,
	server *musterv1alpha1.MCPServer, creds *api.MCPServerClientCredentials) (string, string) {
	name, cause, err := a.storeClientSecret(ctx, writer, secrets, server, creds)
	switch {
	case cause == "":
		return name, ""
	case err == errServerChanged:
		return "", fmt.Sprintf("Cannot store the client credentials of MCP server '%s': %s. It is left as it is, stopped if it was not replaced: enter the client again with core_mcpserver_update clientCredentials and suspended false",
			server.Name, cause)
	}
	if err := secrets.DeleteMCPServerUID(ctx, server.Name, server.Namespace, server.UID); err != nil && !errors.IsNotFound(err) && !errors.IsConflict(err) {
		return "", fmt.Sprintf("Cannot store the client credentials of MCP server '%s': %s. Removing the server failed too, so it is left behind without its client: delete MCP server '%s' (%v)",
			server.Name, cause, server.Name, err)
	}
	return "", fmt.Sprintf("Cannot store the client credentials of MCP server '%s', so it was not created: %s", server.Name, cause)
}

// storeClientSecret creates a new server's client Secret and names it in the
// server. It returns the Secret's name, or why it failed and the error.
func (a *Adapter) storeClientSecret(ctx context.Context, writer mcpServerWriter, secrets secretWriter,
	server *musterv1alpha1.MCPServer, creds *api.MCPServerClientCredentials) (string, string, error) {
	if server.UID == "" {
		return "", "the API server returned no UID for the new MCP server", nil
	}
	name, err := createClientSecret(ctx, secrets, server, creds)
	if err != nil {
		return "", a.describeSecretWriteError(err, name), err
	}
	if err := a.nameClientSecret(ctx, writer, server, name); err != nil {
		if err == errServerChanged {
			return "", err.Error(), err
		}
		if msg := a.describeWriteAuthError(err, "update", server.Name); msg != "" {
			return "", msg, err
		}
		return "", fmt.Sprintf("failed to name Secret %q in the MCP server: %v", name, err), err
	}
	return name, "", nil
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
		result, _ := simpleError("clientCredentials need caller-identity writes: muster stores the client in a Secret written as you, and this muster writes as itself")
		return nil, result
	}
	return secrets, nil
}

// storedClient is the client Secret an update wrote, kept across the
// update's conflict retries so that a retry names it instead of writing
// another.
type storedClient struct {
	name string
	uid  types.UID
}

// applyClientUpdate applies the client side of an update onto the live
// server, after applyMCPServerUpdate: it keeps a generated client Secret an
// auth without one would drop, refuses moving a stored client, checks for a
// conflicting authorization server, and stores new clientCredentials in a
// new Secret owned by the server (once per update). The previous Secret
// stays until the server is deleted: editors may not delete Secrets.
func (a *Adapter) applyClientUpdate(ctx context.Context, writer mcpServerWriter, existing *musterv1alpha1.MCPServer,
	previousAuth *musterv1alpha1.MCPServerAuth, req *api.MCPServerUpdateRequest, client *storedClient) *api.CallToolResult {
	previousAS := authorizationServerOf(&musterv1alpha1.MCPServerSpec{Auth: previousAuth})
	if req.Auth != nil {
		keepGeneratedClientSecretRef(existing, previousAS)
		if req.ClientCredentials == nil {
			if refused := refuseMovedClient(existing, previousAS); refused != nil {
				return refused
			}
		}
	}
	var secrets secretWriter
	if req.ClientCredentials != nil {
		var refused *api.CallToolResult
		if secrets, refused = checkClientCredentials(writer, req.ClientCredentials, existing,
			requestNamesClientSecretRef(req.Auth)); refused != nil {
			return refused
		}
	}
	// Only an update that changes the pinned description, or stores a new
	// client, is checked: a conflict it did not make does not block it.
	if req.Auth != nil || req.ClientCredentials != nil {
		if refused := a.refuseDescriptionConflict(existing, previousAuth, "update", req.ClientCredentials != nil); refused != nil {
			return refused
		}
	}
	if secrets == nil {
		return nil
	}
	if existing.UID == "" || (client.name != "" && client.uid != existing.UID) {
		result, _ := simpleError(fmt.Sprintf("Cannot store the client credentials of MCP server '%s': it has no UID to own the Secret, or was replaced meanwhile", existing.Name))
		return result
	}
	if client.name == "" {
		name, err := createClientSecret(ctx, secrets, existing, req.ClientCredentials)
		if err != nil {
			result, _ := simpleError(fmt.Sprintf("Cannot store the client credentials of MCP server '%s', so it was not updated: %s",
				existing.Name, a.describeSecretWriteError(err, name)))
			return result
		}
		client.name, client.uid = name, existing.UID
	}
	authorizationServerOf(&existing.Spec).ClientCredentialsSecretRef = &musterv1alpha1.ClientCredentialsSecretRef{Name: client.name}
	return nil
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
