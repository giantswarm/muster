package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

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

// CallerClientFactory builds a Kubernetes client authenticated with the
// caller's own bearer token. Each session-initiated MCPServer spec mutation
// gets a per-call client so the apiserver authenticates the real user and
// k8s RBAC decides — never muster's ServiceAccount, never impersonation
// headers (dex-only ruling).
type CallerClientFactory func(bearerToken string) (ctrlclient.Client, error)

// NewKubernetesCallerClientFactory returns a factory deriving per-call clients
// from the given base REST config: same apiserver host and CA, all ambient
// credentials stripped, the caller's bearer as the only authentication.
func NewKubernetesCallerClientFactory(base *rest.Config) CallerClientFactory {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(musterv1alpha1.AddToScheme(scheme))

	return func(bearerToken string) (ctrlclient.Client, error) {
		cfg := rest.AnonymousClientConfig(base)
		cfg.BearerToken = bearerToken
		return ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	}
}

// callerTokenFromContext returns the session's dex id_token carried on the
// request context by the OAuth middleware, or "" for unauthenticated
// transports.
func callerTokenFromContext(ctx context.Context) string {
	token, _ := server.GetIDTokenFromContext(ctx)
	return token
}

// tokenMissingAudience reports whether token is a JWT whose aud claim
// verifiably lacks want. The payload is decoded without signature
// verification — the apiserver is the verifier; this precheck only exists to
// turn a doomed write into an actionable re-login error. Tokens that don't
// parse as JWTs (e.g. opaque test tokens) are passed through to the apiserver.
func tokenMissingAudience(token, want string) bool {
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

// callerWriter adapts a per-call controller-runtime client to the write
// surface the mutation handlers use.
type callerWriter struct {
	client    ctrlclient.Client
	namespace string
}

func (w *callerWriter) CreateMCPServer(ctx context.Context, obj *musterv1alpha1.MCPServer) error {
	return w.client.Create(ctx, obj)
}

func (w *callerWriter) UpdateMCPServer(ctx context.Context, obj *musterv1alpha1.MCPServer) error {
	return w.client.Update(ctx, obj)
}

func (w *callerWriter) DeleteMCPServer(ctx context.Context, name, namespace string) error {
	obj := &musterv1alpha1.MCPServer{}
	obj.Name = name
	obj.Namespace = namespace
	return w.client.Delete(ctx, obj)
}

// reloginError is the actionable error for sessions whose token cannot
// authenticate at the apiserver (missing audience, or rejected outright).
func reloginError(reason string) (msg string) {
	return fmt.Sprintf("%s A re-login is required: sign in again to get a fresh session token, then retry.", reason)
}
