package mcpserver

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

// The client side of core_mcpserver_update: clientCredentials replace the
// client, clearClientCredentials drop it, and an auth without a reference
// keeps it.

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
	switch {
	case req.ClearClientCredentials:
		if refused := clearClientSecretRef(existing, req); refused != nil {
			return refused
		}
	case req.Auth != nil:
		keepGeneratedClientSecretRef(existing, previousAS)
		if req.ClientCredentials == nil {
			if refused := a.refuseMovedClient(ctx, existing, previousAS); refused != nil {
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
	if req.Auth != nil || req.ClientCredentials != nil || req.ClearClientCredentials {
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
				existing.Name, describeSecretWriteError(err, name, existing.Namespace)))
			return result
		}
		client.name, client.uid = name, existing.UID
	}
	authorizationServerOf(&existing.Spec).ClientCredentialsSecretRef = &musterv1alpha1.ClientCredentialsSecretRef{Name: client.name}
	return nil
}

// clearClientSecretRef drops the generated client Secret reference of a
// server for clearClientCredentials, after the request's auth was applied:
// the server signs in without its stored client from this update on. Not
// with clientCredentials or an explicit reference; the Secret goes with the
// server.
func clearClientSecretRef(server *musterv1alpha1.MCPServer, req *api.MCPServerUpdateRequest) *api.CallToolResult {
	if req.ClientCredentials != nil || requestNamesClientSecretRef(req.Auth) {
		result, _ := simpleError("Invalid MCP server definition: clearClientCredentials and clientCredentials or auth.authorizationServer.clientCredentialsSecretRef are mutually exclusive")
		return result
	}
	if as := authorizationServerOf(&server.Spec); as != nil && ownGeneratedRef(server, as.ClientCredentialsSecretRef) {
		as.ClientCredentialsSecretRef = nil
	}
	return nil
}

// ownGeneratedRef reports whether ref names a client Secret generated for
// server, in its namespace.
func ownGeneratedRef(server *musterv1alpha1.MCPServer, ref *musterv1alpha1.ClientCredentialsSecretRef) bool {
	return ref != nil && (ref.Namespace == "" || ref.Namespace == server.Namespace) && isGeneratedClientSecretName(server.Name, ref.Name)
}

// refuseMovedClient refuses an update whose authorizationServer keeps a
// generated client Secret but sends it elsewhere than the issuer and
// endpoints it was stored for (the Secret's AuthorizationServerAnnotation,
// read with muster's ServiceAccount): muster would not read it there
// (checkGeneratedOwner). An update that puts back the issuer and endpoints
// the Secret was stored for passes. Without the Secret, it compares with the
// server's previous authorizationServer.
func (a *Adapter) refuseMovedClient(ctx context.Context, server *musterv1alpha1.MCPServer, previous *musterv1alpha1.MCPServerAuthAuthorizationServer) *api.CallToolResult {
	as := authorizationServerOf(&server.Spec)
	if as == nil || !ownGeneratedRef(server, as.ClientCredentialsSecretRef) {
		return nil
	}
	storedFor := destinationDigest(previous)
	secret := &corev1.Secret{}
	if err := a.client.Get(ctx, ctrlclient.ObjectKey{Namespace: server.Namespace, Name: as.ClientCredentialsSecretRef.Name}, secret); err == nil {
		storedFor = secret.Annotations[api.AuthorizationServerAnnotation]
	}
	if destinationDigest(as) == storedFor {
		return nil
	}
	result, _ := simpleError(fmt.Sprintf("Cannot update MCP server '%s': its stored client was entered for other issuer and endpoints of auth.authorizationServer; re-enter it with clientCredentials, or clear it with clearClientCredentials", server.Name))
	return result
}
