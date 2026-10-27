package mcpserver

import (
	"fmt"
	"sync"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// CredentialsOption configures a CredentialsAdapter.
type CredentialsOption func(*CredentialsAdapter)

// WithProtectedSecrets names Secrets no MCPServer may read: muster's own
// (secretBinding.protectedSecrets).
func WithProtectedSecrets(secrets ...types.NamespacedName) CredentialsOption {
	return func(a *CredentialsAdapter) {
		for _, s := range secrets {
			if s.Name == "" {
				continue
			}
			if s.Namespace == "" {
				s.Namespace = metav1.NamespaceDefault
			}
			a.binding.protected[s] = struct{}{}
		}
	}
}

// secretRequest is a read on behalf of an MCPServer: who asks, and which field
// of its spec names the Secret.
type secretRequest struct {
	owner api.SecretOwner
	field string
}

// reservedSecretTypes are Secret types that never hold a connector's
// credential and always hold muster's or the cluster's: Helm release state
// (the rendered values, muster's OAuth Secret data included), TLS keys
// (muster's ingress certificate), ServiceAccount and bootstrap tokens
// (Kubernetes API credentials) and image pull credentials. Their names vary
// (a Helm release Secret per revision), so they are refused by type.
var reservedSecretTypes = map[corev1.SecretType]bool{
	"helm.sh/release.v1":                 true,
	corev1.SecretTypeTLS:                 true,
	corev1.SecretTypeServiceAccountToken: true,
	corev1.SecretTypeBootstrapToken:      true,
	corev1.SecretTypeDockerConfigJson:    true,
	corev1.SecretTypeDockercfg:           true,
}

// secretBinding holds the rules for reads on behalf of an MCPServer.
type secretBinding struct {
	protected map[types.NamespacedName]struct{}
	// logged records the warnings already written, so a Secret read at every
	// connect or login is reported once per MCPServer, UID, Secret, field and
	// reason. The adapter learns of no deleted MCPServer, so the record is
	// capped: past maxLoggedReads entries it starts over, and a warning may
	// be written once more.
	logged      sync.Map
	loggedCount atomic.Int64
}

// maxLoggedReads bounds secretBinding.logged.
const maxLoggedReads = 4096

func newSecretBinding() *secretBinding {
	return &secretBinding{
		protected: map[types.NamespacedName]struct{}{},
	}
}

// warnOnce logs a warning about a read once per request, Secret and reason.
func (b *secretBinding) warnOnce(req *secretRequest, namespace, name, reason, format string, args ...any) {
	key := req.owner.String() + "|" + req.owner.UID + "|" + namespace + "/" + name + "|" + req.field + "|" + reason
	if _, seen := b.logged.LoadOrStore(key, struct{}{}); seen {
		return
	}
	if b.loggedCount.Add(1) > maxLoggedReads {
		b.logged.Clear()
		b.loggedCount.Store(1)
		b.logged.Store(key, struct{}{})
	}
	logging.Warn("SecretCredentials", format, args...)
}

// refuseReserved is the error for a Secret muster keeps for itself.
func refuseReserved(req *secretRequest, namespace, name, why string) error {
	return fmt.Errorf("MCPServer %s may not read Secret %s/%s named in %s: %w (%s); reference a Secret of its own",
		req.owner, namespace, name, req.field, api.ErrSecretProtected, why)
}

// checkProtected refuses, before it is read, a Secret muster's configuration
// lists as its own.
func (b *secretBinding) checkProtected(req *secretRequest, namespace, name string) error {
	if _, ok := b.protected[types.NamespacedName{Namespace: namespace, Name: name}]; !ok {
		return nil
	}
	b.warnOnce(req, namespace, name, "protected", "MCPServer %s names muster's own Secret %s/%s in %s; refused",
		req.owner, namespace, name, req.field)
	return refuseReserved(req, namespace, name, "one of muster's own")
}

// checkReserved refuses a Secret whose type is never a connector's credential.
func (b *secretBinding) checkReserved(req *secretRequest, namespace string, secret *corev1.Secret) error {
	if !reservedSecretTypes[secret.Type] {
		return nil
	}
	b.warnOnce(req, namespace, secret.Name, "reserved type", "MCPServer %s names Secret %s/%s of type %s in %s; refused",
		req.owner, namespace, secret.Name, secret.Type, req.field)
	return refuseReserved(req, namespace, secret.Name, "type "+string(secret.Type))
}
