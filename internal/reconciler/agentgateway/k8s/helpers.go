package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	agw "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	"github.com/agentgateway/agentgateway/controller/api/v1alpha1/shared"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
)

func (a *Applier) createOrUpdate(ctx context.Context, obj client.Object, mutate controllerutil.MutateFn) error {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		_, err := controllerutil.CreateOrUpdate(ctx, a.client, obj, mutate)
		return err
	})
	if apierrors.IsConflict(err) {
		return fmt.Errorf("conflict retries exhausted: %w", err)
	}
	return err
}

func (a *Applier) deleteIfExists(ctx context.Context, obj client.Object) error {
	if err := a.client.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (a *Applier) applyOwner(meta *metav1.ObjectMeta) {
	ref := a.ownerRef
	// Defensive defaults so a caller that forgot to set Controller or
	// BlockOwnerDeletion still gets cascade-blocking semantics consistent
	// with the test fixture. ownerRef passed by future callers may not
	// carry these.
	if ref.Controller == nil {
		t := true
		ref.Controller = &t
	}
	if ref.BlockOwnerDeletion == nil {
		t := true
		ref.BlockOwnerDeletion = &t
	}
	for i := range meta.OwnerReferences {
		existing := &meta.OwnerReferences[i]
		if existing.Name == ref.Name && existing.Kind == ref.Kind && existing.APIVersion == ref.APIVersion {
			meta.OwnerReferences[i] = ref
			return
		}
	}
	meta.OwnerReferences = append(meta.OwnerReferences, ref)
}

func portInt32(p int) (int32, error) {
	if p < 1 || p > 65535 {
		return 0, fmt.Errorf("port %d out of range [1, 65535]", p)
	}
	return int32(p), nil
}

func mapProtocol(p agentgateway.HTTPProtocol) (agw.MCPProtocol, error) {
	switch p {
	case agentgateway.StreamableHTTP:
		return agw.MCPProtocolStreamableHTTP, nil
	case agentgateway.SSE:
		return agw.MCPProtocolSSE, nil
	default:
		return "", fmt.Errorf("unknown HTTPProtocol %q", p)
	}
}

func policySpec(p agentgateway.Policy) agw.AgentgatewayPolicySpec {
	httpRouteKind := gwv1.Kind(kindHTTPRoute)
	httpRouteGroup := gwv1.Group(gatewayAPIGroupName)
	spec := agw.AgentgatewayPolicySpec{
		TargetRefs: []shared.LocalPolicyTargetReferenceWithSectionName{
			{
				LocalPolicyTargetReference: shared.LocalPolicyTargetReference{
					Group: httpRouteGroup,
					Kind:  httpRouteKind,
					Name:  gwv1.ObjectName(p.Name),
				},
			},
		},
	}
	if p.Authn.ForwardToken {
		spec.Backend = &agw.BackendFull{
			BackendSimple: agw.BackendSimple{
				Auth: &agw.BackendAuth{Passthrough: &agw.BackendAuthPassthrough{}},
			},
		}
	}
	return spec
}

// deferredAuthnFields returns the names of Authn fields the gateway adapter
// does not translate to AgentgatewayPolicy primitives — muster's aggregator
// handles them in front of the gateway today.
func deferredAuthnFields(a agentgateway.Authn) []string {
	var deferred []string
	if len(a.RequiredAudiences) > 0 {
		deferred = append(deferred, "requiredAudiences")
	}
	if a.TokenExchange != nil && a.TokenExchange.Enabled {
		deferred = append(deferred, "tokenExchange")
	}
	if a.AuthorizationServer != nil {
		deferred = append(deferred, "authorizationServer")
	}
	return deferred
}
