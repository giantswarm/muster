package mcpserver

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/giantswarm/muster/v5/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// CredentialsOption configures a CredentialsAdapter.
type CredentialsOption func(*CredentialsAdapter)

// WithSecretBindingMode sets what happens to a Secret an MCPServer names that
// is not bound to it: refused (enforce) or read with a warning (warn, the
// default). An unknown mode is treated as warn.
func WithSecretBindingMode(mode api.SecretBindingMode) CredentialsOption {
	return func(a *CredentialsAdapter) {
		a.binding.mode = mode
	}
}

// WithProtectedSecrets names Secrets no MCPServer may read, in either mode:
// muster's own (secretBinding.protectedSecrets).
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

// ownerNamespace is the namespace of the requesting MCPServer, "default" when
// it has none (filesystem mode).
func (r *secretRequest) ownerNamespace() string {
	if r.owner.Namespace == "" {
		return metav1.NamespaceDefault
	}
	return r.owner.Namespace
}

// qualifiedName is "<namespace>/<name>" of the requesting MCPServer, the
// MCPServerSecretAnnotation value that binds a Secret of any namespace.
func (r *secretRequest) qualifiedName() string {
	return r.ownerNamespace() + "/" + r.owner.Name
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
	mode      api.SecretBindingMode
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
		mode:      api.SecretBindingWarn,
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

// checkBound reports a Secret not bound to the requesting MCPServer: an error
// in enforce mode, a warning (once) in warn mode.
func (b *secretBinding) checkBound(req *secretRequest, namespace string, secret *corev1.Secret) error {
	if boundTo(req, namespace, secret) {
		return nil
	}
	how := b.howToBind(req, namespace)
	if b.mode == api.SecretBindingEnforce {
		return fmt.Errorf("MCPServer %s may not read Secret %s/%s named in %s: %w; %s",
			req.owner, namespace, secret.Name, req.field, api.ErrSecretNotBound, how)
	}
	b.warnOnce(req, namespace, secret.Name, "unbound", "MCPServer %s reads Secret %s/%s (%s), which is not bound to it; "+
		"secretBinding.mode enforce will refuse this read: %s",
		req.owner, namespace, secret.Name, req.field, how)
	return nil
}

// howToBind tells the operator what to add to the Secret.
func (b *secretBinding) howToBind(req *secretRequest, namespace string) string {
	annotation := fmt.Sprintf("add %s to the Secret's annotation %s (a comma-separated list binds several MCPServers)",
		req.qualifiedName(), api.MCPServerSecretAnnotation)
	if namespace != req.ownerNamespace() {
		return annotation
	}
	uid := req.owner.UID
	if uid == "" {
		uid = "<its uid>"
	}
	return fmt.Sprintf("add %s to the Secret's annotation %s (a comma-separated list binds several MCPServers), "+
		"or an ownerReference to the MCPServer (apiVersion %s, kind MCPServer, name %s, uid %s)",
		req.owner.Name, api.MCPServerSecretAnnotation, musterv1alpha1.GroupVersion.String(), req.owner.Name, uid)
}

// boundTo reports whether a Secret in namespace is bound to the requesting
// MCPServer:
//   - an ownerReference to it, in its namespace, whose UID matches the
//     MCPServer's (never without a known UID);
//   - MCPServerSecretAnnotation, a comma-separated list, with an entry
//     "<namespace>/<name>" in any namespace, or its name in its namespace.
func boundTo(req *secretRequest, namespace string, secret *corev1.Secret) bool {
	if req.owner.Name == "" {
		return false
	}
	sameNamespace := namespace == req.ownerNamespace()
	for _, entry := range strings.Split(secret.Annotations[api.MCPServerSecretAnnotation], ",") {
		entry = strings.TrimSpace(entry)
		if entry == req.qualifiedName() || (sameNamespace && entry == req.owner.Name) {
			return true
		}
	}
	if !sameNamespace {
		return false
	}
	if req.owner.UID == "" {
		return false
	}
	for _, ref := range secret.OwnerReferences {
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err == nil && gv.Group == musterv1alpha1.GroupVersion.Group && ref.Kind == "MCPServer" &&
			ref.Name == req.owner.Name && string(ref.UID) == req.owner.UID {
			return true
		}
	}
	return false
}
