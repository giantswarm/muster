package api

import (
	"errors"
	"fmt"
)

// MCPServerSecretAnnotation binds a Secret to the MCPServers it lists, comma
// separated: "<namespace>/<name>" (any namespace) or "<name>" (the Secret's
// namespace). An ownerReference to an MCPServer of the Secret's namespace
// binds it too; the annotation also binds a Secret shared by several
// MCPServers, or rendered by GitOps before the MCPServer has a UID.
const MCPServerSecretAnnotation = "muster.giantswarm.io/mcpserver"

// SecretBindingMode is what muster does with a Secret an MCPServer's spec
// names but that is not bound to that MCPServer.
type SecretBindingMode string

const (
	// SecretBindingWarn reads an unbound Secret and logs a warning. The
	// default, so existing Secrets keep working until they are bound.
	SecretBindingWarn SecretBindingMode = "warn"
	// SecretBindingEnforce refuses an unbound Secret.
	SecretBindingEnforce SecretBindingMode = "enforce"
)

// Fields of an MCPServer spec that name a Secret, for log and error messages.
const (
	SecretFieldHeadersFrom         = "spec.headersFrom"
	SecretFieldAuthorizationServer = "spec.auth.authorizationServer.clientCredentialsSecretRef" //nolint:gosec // G101: a field path, not a credential
	SecretFieldTokenExchange       = "spec.auth.tokenExchange.clientCredentialsSecretRef"       //nolint:gosec // G101: a field path, not a credential
)

// ErrSecretNotBound reports a Secret an MCPServer names that is not bound to
// it (secretBinding.mode enforce).
var ErrSecretNotBound = errors.New("the Secret is not bound to the MCPServer")

// ErrSecretProtected reports a Secret no MCPServer may read: one of muster's
// own (secretBinding.protectedSecrets), or one of a type that never holds a
// connector credential (Helm release, TLS, service-account token, ...).
var ErrSecretProtected = errors.New("muster never reads this Secret for an MCPServer")

// SecretOwner is the MCPServer whose spec names a Secret. Only a Secret bound
// to it is read on its behalf (see MCPServerSecretAnnotation).
type SecretOwner struct {
	// Name is the MCPServer object's name (not its key).
	Name string
	// Namespace is the MCPServer's namespace, the default namespace of its
	// Secret references. Empty in filesystem mode ("default").
	Namespace string
	// UID is the MCPServer's UID; an ownerReference must match it when set.
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
