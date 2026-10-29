package mcpserver

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/callerwrite"
)

// A connector's client (giantswarm/giantswarm#38849): core_mcpserver_create
// and core_mcpserver_update take clientCredentials and write them, as the
// caller, to a Secret with a generated name owned by the MCPServer. The
// chart's admission policy (mcpserver-secret-references.yaml) does not ask
// the writer to get a Secret with that name, since editors may only create
// Secrets; muster instead reads it only with an ownerReference to the
// MCPServer's UID (checkGeneratedOwner). The pattern lives in both places and
// must agree.

const (
	// generatedClientSecretInfix separates the server's name from the random
	// suffix in a generated client Secret name.
	generatedClientSecretInfix = "-oauth-client-" //nolint:gosec // G101: part of a Secret name, not a credential
	// generatedClientSecretSuffixLen is the length of the random suffix.
	generatedClientSecretSuffixLen = 5
	// generatedClientSecretMaxServer is how much of the server's name a
	// generated name keeps, so it stays a DNS label (63 characters).
	generatedClientSecretMaxServer = validation.DNS1123LabelMaxLength - len(generatedClientSecretInfix) - generatedClientSecretSuffixLen
	// generatedClientSecretAlphabet is the alphabet of the random suffix.
	generatedClientSecretAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

	// MCPServerSecretAnnotation names, on a Secret muster writes for a
	// server, the server it belongs to. Informational: the ownerReference
	// binds it.
	MCPServerSecretAnnotation = "muster.giantswarm.io/mcpserver"
)

// generatedClientSecretPrefix is the part of a generated client Secret name
// before its random suffix.
func generatedClientSecretPrefix(server string) string {
	if len(server) > generatedClientSecretMaxServer {
		server = server[:generatedClientSecretMaxServer]
	}
	return server + generatedClientSecretInfix
}

// newGeneratedClientSecretName returns a fresh client Secret name for a
// server: <server>-oauth-client-<5 of [a-z0-9]>.
func newGeneratedClientSecretName(server string) (string, error) {
	suffix := make([]byte, generatedClientSecretSuffixLen)
	limit := big.NewInt(int64(len(generatedClientSecretAlphabet)))
	for i := range suffix {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("generate a Secret name: %w", err)
		}
		suffix[i] = generatedClientSecretAlphabet[n.Int64()]
	}
	return generatedClientSecretPrefix(server) + string(suffix), nil
}

// isGeneratedClientSecretName reports whether name has the form of a client
// Secret name generated for server.
func isGeneratedClientSecretName(server, name string) bool {
	suffix, ok := strings.CutPrefix(name, generatedClientSecretPrefix(server))
	if !ok || len(suffix) != generatedClientSecretSuffixLen {
		return false
	}
	for _, c := range suffix {
		if !strings.ContainsRune(generatedClientSecretAlphabet, c) {
			return false
		}
	}
	return true
}

// checkGeneratedOwner refuses a Secret with a generated client Secret name
// for the owner unless it lives in the owner's namespace and has an
// ownerReference to the owner's MCPServer UID. The admission policy lets an
// MCPServer name such a Secret without its writer's get, so ownership is
// what ties the Secret to this server. Other names pass.
func checkGeneratedOwner(req *secretRequest, namespace string, secret *corev1.Secret) error {
	if req.field != api.SecretFieldAuthorizationServer || !isGeneratedClientSecretName(req.owner.Name, secret.Name) {
		return nil
	}
	ownNamespace := req.owner.Namespace
	if ownNamespace == "" {
		ownNamespace = metav1.NamespaceDefault
	}
	if namespace == ownNamespace && req.owner.UID != "" {
		for _, ref := range secret.OwnerReferences {
			if ref.Kind == "MCPServer" && string(ref.UID) == req.owner.UID &&
				strings.HasPrefix(ref.APIVersion, musterv1alpha1.GroupVersion.Group+"/") {
				return nil
			}
		}
	}
	return fmt.Errorf("MCPServer %s may not read Secret %s/%s named in %s: %w: a Secret with a generated client name must have an ownerReference to this MCPServer; set the client with core_mcpserver_update clientCredentials",
		req.owner, namespace, secret.Name, req.field, api.ErrSecretNotOwned)
}

// secretWriter is the caller-identity write surface for Secrets. Only the
// per-call caller client has it: in filesystem mode there is none.
type secretWriter interface {
	CreateSecret(ctx context.Context, secret *corev1.Secret) error
}

// validateClientCredentials checks clientCredentials against the request's
// auth: both values set, an authorizationServer to use them, and no explicit
// Secret reference besides them. as is the authorizationServer of the spec
// the request results in; explicitRef reports whether the request names a
// clientCredentialsSecretRef.
func validateClientCredentials(creds *api.MCPServerClientCredentials, as *musterv1alpha1.MCPServerAuthAuthorizationServer, explicitRef bool) error {
	if strings.TrimSpace(creds.ClientID) == "" || strings.TrimSpace(creds.ClientSecret) == "" {
		return fmt.Errorf("clientCredentials needs clientId and clientSecret")
	}
	if explicitRef {
		return fmt.Errorf("clientCredentials and auth.authorizationServer.clientCredentialsSecretRef are mutually exclusive")
	}
	if as == nil {
		return fmt.Errorf("clientCredentials needs auth.authorizationServer")
	}
	return nil
}

// authorizationServerOf returns the spec's authorizationServer, or nil.
func authorizationServerOf(spec *musterv1alpha1.MCPServerSpec) *musterv1alpha1.MCPServerAuthAuthorizationServer {
	if spec.Auth == nil {
		return nil
	}
	return spec.Auth.AuthorizationServer
}

// requestNamesClientSecretRef reports whether a request's auth names an
// authorizationServer client Secret.
func requestNamesClientSecretRef(auth *api.MCPServerAuth) bool {
	return auth != nil && auth.AuthorizationServer != nil && auth.AuthorizationServer.ClientCredentialsSecretRef != nil
}

// clientCredentialsSecret builds the Secret holding a server's client, owned
// by the server.
func clientCredentialsSecret(name string, server *musterv1alpha1.MCPServer, creds *api.MCPServerClientCredentials) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   server.Namespace,
			Annotations: map[string]string{MCPServerSecretAnnotation: server.Name},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: musterv1alpha1.GroupVersion.String(),
				Kind:       "MCPServer",
				Name:       server.Name,
				UID:        server.UID,
			}},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			DefaultClientIDKey:     []byte(creds.ClientID),
			DefaultClientSecretKey: []byte(creds.ClientSecret),
		},
	}
	if len(validation.IsValidLabelValue(server.Name)) == 0 {
		secret.Labels = map[string]string{MCPServerSecretAnnotation: server.Name}
	}
	return secret
}

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

// storeClientCredentialsOnCreate writes a new server's client to the Secret
// its spec names, after the server was created. When the Secret write fails
// it deletes the server again, so no connector is left without its client
// and without a message; the answer says when that delete fails too.
func (a *Adapter) storeClientCredentialsOnCreate(ctx context.Context, writer mcpServerWriter, secrets secretWriter,
	server *musterv1alpha1.MCPServer, secretName string, creds *api.MCPServerClientCredentials) string {
	var cause string
	if server.UID == "" {
		cause = "the API server returned no UID for the new MCP server"
	} else if err := secrets.CreateSecret(ctx, clientCredentialsSecret(secretName, server, creds)); err != nil {
		cause = a.describeSecretWriteError(err, secretName)
	} else {
		return ""
	}
	if err := writer.DeleteMCPServer(ctx, server.Name, server.Namespace); err != nil && !errors.IsNotFound(err) {
		return fmt.Sprintf("Cannot store the client credentials of MCP server '%s': %s. Removing the server failed too, so it is left behind without its client: delete MCP server '%s' (%v)",
			server.Name, cause, server.Name, err)
	}
	return fmt.Sprintf("Cannot store the client credentials of MCP server '%s', so it was not created: %s", server.Name, cause)
}

// prepareClientCredentials checks clientCredentials against the spec the
// request results in and points its authorizationServer at a newly generated
// Secret name. It returns the caller's Secret writer and that name, or a tool
// error.
func (a *Adapter) prepareClientCredentials(writer mcpServerWriter, creds *api.MCPServerClientCredentials,
	server *musterv1alpha1.MCPServer, explicitRef bool) (secretWriter, string, *api.CallToolResult) {
	as := authorizationServerOf(&server.Spec)
	if err := validateClientCredentials(creds, as, explicitRef); err != nil {
		result, _ := simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
		return nil, "", result
	}
	secrets, ok := writer.(secretWriter)
	if !ok {
		result, _ := simpleError("clientCredentials need muster in Kubernetes mode: muster stores them in a Secret")
		return nil, "", result
	}
	name, err := newGeneratedClientSecretName(server.Name)
	if err != nil {
		result, _ := simpleError(fmt.Sprintf("Cannot store the client credentials of MCP server '%s': %v", server.Name, err))
		return nil, "", result
	}
	as.ClientCredentialsSecretRef = &musterv1alpha1.ClientCredentialsSecretRef{Name: name}
	return secrets, name, nil
}

// keepGeneratedClientSecretRef keeps, on an update whose auth names no
// authorizationServer client Secret, the generated one the server had: an
// edit that re-sends auth without the reference keeps the stored client. An
// auth without authorizationServer drops it.
func keepGeneratedClientSecretRef(server *musterv1alpha1.MCPServer, previous *musterv1alpha1.MCPServerAuthAuthorizationServer) {
	as := authorizationServerOf(&server.Spec)
	if as == nil || as.ClientCredentialsSecretRef != nil || previous == nil || previous.ClientCredentialsSecretRef == nil {
		return
	}
	ref := previous.ClientCredentialsSecretRef
	if (ref.Namespace == "" || ref.Namespace == server.Namespace) && isGeneratedClientSecretName(server.Name, ref.Name) {
		as.ClientCredentialsSecretRef = ref.DeepCopy()
	}
}

// storedInSecret is the note a create or update result adds for the Secret
// it wrote the client to: its name, never its values.
func storedInSecret(name string) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf(" (client credentials stored in Secret '%s')", name)
}

// unusedSecret is the note a failed update adds for the Secret it wrote
// before: owned by the server, it goes with it.
func unusedSecret(name string) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf(" The client credentials were stored in Secret '%s', which the server does not use yet; it is deleted with the server.", name)
}

// clientCredentialsArg is the clientCredentials argument of
// core_mcpserver_create and core_mcpserver_update.
func clientCredentialsArg(description string) api.ArgMetadata {
	return api.ArgMetadata{Name: "clientCredentials", Type: api.ArgTypeObject, Required: false, Description: description, Schema: map[string]interface{}{
		api.SchemaKeyType:        string(api.ArgTypeObject),
		api.SchemaKeyDescription: "Write-only; never returned. Not with auth.authorizationServer.clientCredentialsSecretRef",
		api.SchemaKeyProperties: map[string]interface{}{
			"clientId":     map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString), api.SchemaKeyDescription: "OAuth client ID"},
			"clientSecret": map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString), api.SchemaKeyDescription: "OAuth client secret"},
		},
		api.SchemaKeyRequired: []string{"clientId", "clientSecret"},
	}}
}
