package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Fields of an MCPServer spec that name a Secret, for log and error messages.
const (
	SecretFieldHeadersFrom         = "spec.headersFrom"
	SecretFieldAuthorizationServer = "spec.auth.authorizationServer.clientCredentialsSecretRef" //nolint:gosec // G101: a field path, not a credential
	SecretFieldTokenExchange       = "spec.auth.tokenExchange.clientCredentialsSecretRef"       //nolint:gosec // G101: a field path, not a credential
)

// ErrSecretProtected reports a Secret no MCPServer may read: one of muster's
// own (secretBinding.protectedSecrets), or one of a type that never holds a
// connector credential (Helm release, TLS, service-account token, ...).
var ErrSecretProtected = errors.New("muster never reads this Secret for an MCPServer")

// ErrSecretReferencePolicyInactive reports that muster reads no Secret for an
// MCPServer: the admission policy that admits an MCPServer only when its
// writer may get the Secrets it names is not in force.
var ErrSecretReferencePolicyInactive = errors.New("the MCPServer Secret reference policy is not in force")

// ErrSecretNotOwned reports a Secret with the name core_mcpserver_create
// generates for a server's client that the server does not own: muster
// reads such a Secret only with an ownerReference to the MCPServer's UID.
var ErrSecretNotOwned = errors.New("the Secret is not owned by the MCPServer")

// ErrSecretOtherAuthorizationServer reports a Secret with a generated client
// name that muster wrote for another authorization server than the one the
// MCPServer's spec now names: its issuer or an endpoint changed after the
// client was stored, so the client is not sent there.
var ErrSecretOtherAuthorizationServer = errors.New("the Secret holds a client stored for another authorization server")

// AuthorizationServerAnnotation records, on a Secret muster writes for an
// MCPServer's client, the AuthorizationServerDigest of the authorization
// server it was written for.
const AuthorizationServerAnnotation = "muster.giantswarm.io/authorization-server"

// AuthorizationServerDigest identifies where a client of an authorization
// server is sent: its issuer (and so the endpoints discovered from it) and
// its pinned authorization, token and revocation endpoints. "" for nil.
func AuthorizationServerDigest(as *MCPServerAuthAuthorizationServer) string {
	if as == nil {
		return ""
	}
	destination, _ := json.Marshal(struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorizationEndpoint"`
		TokenEndpoint         string `json:"tokenEndpoint"`
		RevocationEndpoint    string `json:"revocationEndpoint"`
	}{as.Issuer, as.AuthorizationEndpoint, as.TokenEndpoint, as.RevocationEndpoint})
	sum := sha256.Sum256(destination)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SecretOwner is the MCPServer whose spec names a Secret, for the refusals
// and warnings of a read on its behalf.
type SecretOwner struct {
	// Name is the MCPServer object's name (not its key).
	Name string
	// Namespace is the MCPServer's namespace, the default namespace of its
	// Secret references. Empty in filesystem mode ("default").
	Namespace string
	// UID is the MCPServer's UID, which tells a recreated MCPServer apart.
	UID string
	// AuthorizationServer is the AuthorizationServerDigest of the
	// authorization server a client read for spec.auth.authorizationServer
	// is sent to. Set by the readers of that field.
	AuthorizationServer string
}

// ForAuthorizationServer returns the owner of a read of the client of as.
func (o SecretOwner) ForAuthorizationServer(as *MCPServerAuthAuthorizationServer) SecretOwner {
	o.AuthorizationServer = AuthorizationServerDigest(as)
	return o
}

// MCPServerSecretOwner returns the owner of a server's Secret references from
// its key (MCPServerKey), namespace and UID.
func MCPServerSecretOwner(key, namespace, uid string) SecretOwner {
	keyNamespace, name := SplitMCPServerKey(key)
	if namespace == "" {
		namespace = keyNamespace
	}
	return SecretOwner{Name: name, Namespace: namespace, UID: uid}
}

// String returns "<namespace>/<name>".
func (o SecretOwner) String() string {
	ns := o.Namespace
	if ns == "" {
		ns = "default"
	}
	return fmt.Sprintf("%s/%s", ns, o.Name)
}

// IsSignInRefused reports whether err is muster's refusal of a server's
// sign-in, which no retry or login fixes until its spec or its Secret
// changes: its client Secret is refused (ErrSecretProtected,
// ErrSecretNotOwned, ErrSecretOtherAuthorizationServer), the Secret reference
// policy is not in force, or another MCPServer describes its authorization
// server differently (ErrAuthorizationServerConflict).
func IsSignInRefused(err error) bool {
	for _, refusal := range []error{ErrSecretProtected, ErrSecretReferencePolicyInactive, ErrSecretNotOwned,
		ErrSecretOtherAuthorizationServer, ErrAuthorizationServerConflict} {
		if errors.Is(err, refusal) {
			return true
		}
	}
	return false
}
