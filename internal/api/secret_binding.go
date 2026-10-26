package api

import (
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
