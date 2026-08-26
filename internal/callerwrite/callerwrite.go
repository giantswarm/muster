// Package callerwrite implements the shared caller-identity write gate for
// session-initiated CR mutations (issues #1056, #1069). In Kubernetes mode,
// spec mutations triggered by MCP sessions must not go through muster's
// ServiceAccount: the session's dex id_token becomes the bearer of a per-call
// client, so the apiserver authenticates the real user, Kubernetes RBAC
// authorizes the write, and the audit log records the true subject. Each
// CR-owning adapter (MCPServer, Workflow) wraps the Gate into its own typed
// write surface.
package callerwrite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/server"
)

// DefaultKubernetesAudience is the audience the local kube-apiserver trusts on
// dex id_tokens (the dex-k8s-authenticator cross-client audience). Overridable
// via writesAsCaller.kubernetesAudience in the muster config.
const DefaultKubernetesAudience = "dex-k8s-authenticator"

// ClientFactory builds a Kubernetes client authenticated with the caller's
// own bearer token. Each session-initiated spec mutation gets a per-call
// client so the apiserver authenticates the real user and k8s RBAC decides —
// never muster's ServiceAccount, never impersonation headers (dex-only
// ruling).
type ClientFactory func(bearerToken string) (ctrlclient.Client, error)

// NewKubernetesClientFactory returns a factory deriving per-call clients from
// the given base REST config: same apiserver host and CA, all ambient
// credentials stripped, the caller's bearer as the only authentication.
func NewKubernetesClientFactory(base *rest.Config) ClientFactory {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(musterv1alpha1.AddToScheme(scheme))

	return func(bearerToken string) (ctrlclient.Client, error) {
		cfg := rest.AnonymousClientConfig(base)
		cfg.BearerToken = bearerToken
		return ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	}
}

// TokenFromContext returns the session's dex id_token carried on the request
// context by the OAuth middleware, or "" for unauthenticated transports.
func TokenFromContext(ctx context.Context) string {
	token, _ := server.GetIDTokenFromContext(ctx)
	return token
}

// TokenMissingAudience reports whether token is a JWT whose aud claim
// verifiably lacks want. The payload is decoded without signature
// verification — the apiserver is the verifier; this precheck only exists to
// turn a doomed write into an actionable re-login error. Tokens that don't
// parse as JWTs (e.g. opaque test tokens) are passed through to the apiserver.
func TokenMissingAudience(token, want string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Aud json.RawMessage `json:"aud"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Aud == nil {
		return true
	}
	var single string
	if err := json.Unmarshal(claims.Aud, &single); err == nil {
		return single != want
	}
	var many []string
	if err := json.Unmarshal(claims.Aud, &many); err == nil {
		for _, aud := range many {
			if aud == want {
				return false
			}
		}
		return true
	}
	return true
}

// ReloginError is the actionable error for sessions whose token cannot
// authenticate at the apiserver (missing audience, or rejected outright).
func ReloginError(reason string) string {
	return fmt.Sprintf("%s A re-login is required: sign in again to get a fresh session token, then retry.", reason)
}

// Gate resolves the per-call caller-identity client a session-initiated CR
// mutation must use. The zero value is disabled: adapters keep their
// SA-backed write path (filesystem mode). The app layer enables it whenever
// muster runs in Kubernetes mode.
type Gate struct {
	enabled  bool
	audience string
	factory  ClientFactory

	// subject names what is being written in the no-token error, e.g.
	// "MCP server changes" or "workflow changes".
	subject string
}

// NewGate returns a disabled gate whose error messages name subject as the
// thing being written.
func NewGate(subject string) Gate {
	return Gate{subject: subject}
}

// Enable switches the gate on. factory may be nil when muster has no
// Kubernetes API access; mutations then fail with an explicit configuration
// error instead of silently falling back to the SA path. An empty audience
// selects DefaultKubernetesAudience.
func (g *Gate) Enable(factory ClientFactory, audience string) {
	g.enabled = true
	if audience == "" {
		audience = DefaultKubernetesAudience
	}
	g.audience = audience
	g.factory = factory
}

// Enabled reports whether caller-identity writes are on.
func (g *Gate) Enabled() bool {
	return g.enabled
}

// Resolve returns the caller-identity client for the session carried by ctx,
// or a ready-to-return user-facing error message when the session cannot
// produce a usable bearer. Callers must check Enabled() first: resolving a
// disabled gate is a bug and fails accordingly.
func (g *Gate) Resolve(ctx context.Context) (ctrlclient.Client, string) {
	if !g.enabled {
		return nil, "internal error: caller-identity write gate is disabled"
	}

	token := TokenFromContext(ctx)
	if token == "" {
		return nil, ReloginError(fmt.Sprintf(
			"This installation writes %s with your own identity, but your session carries no token.", g.subject))
	}
	if TokenMissingAudience(token, g.audience) {
		return nil, ReloginError(fmt.Sprintf(
			"Your session token does not carry the %q audience required by the Kubernetes API.", g.audience))
	}
	if g.factory == nil {
		return nil, "writes-as-caller is enabled but muster has no Kubernetes API access to perform the write with — check the muster deployment configuration"
	}

	client, err := g.factory(token)
	if err != nil {
		return nil, fmt.Sprintf("Failed to prepare a Kubernetes client with your identity: %v", err)
	}
	return client, ""
}

// DescribeWriteAuthError maps apiserver authn/authz failures on
// caller-identity writes to actionable messages: 403 names the verb, kind,
// fully-qualified resource, and namespace, and points at the editor role the
// chart ships; 401 asks for a re-login. Returns "" for every other error so
// callers fall through to their existing handling.
func DescribeWriteAuthError(err error, verb, kind, resource, name, namespace, editorRole string) string {
	switch {
	case errors.IsForbidden(err):
		return fmt.Sprintf(
			"Permission denied: your user may not %s %s %q (%s) in namespace %q. "+
				"Ask a platform admin to grant you the %s role. API server response: %v",
			verb, kind, name, resource, namespace, editorRole, err)
	case errors.IsUnauthorized(err):
		return ReloginError("The Kubernetes API rejected your session token.")
	}
	return ""
}
