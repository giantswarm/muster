package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	agw "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	"github.com/agentgateway/agentgateway/controller/api/v1alpha1/shared"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
)

func (a *Applier) createOrUpdate(ctx context.Context, obj client.Object, mutate controllerutil.MutateFn) error {
	var lastErr error
	for attempt := 0; attempt <= a.cfg.UpdateConflictRetries; attempt++ {
		_, err := controllerutil.CreateOrUpdate(ctx, a.client, obj, mutate)
		if err == nil {
			return nil
		}
		if !apierrors.IsConflict(err) {
			return err
		}
		lastErr = err
		if err := a.client.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("refresh after conflict: %w", err)
		}
	}
	return fmt.Errorf("conflict retries exhausted: %w", lastErr)
}

func (a *Applier) deleteIfExists(ctx context.Context, obj client.Object) error {
	if err := a.client.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (a *Applier) applyOwner(meta *metav1.ObjectMeta) {
	for i := range meta.OwnerReferences {
		if meta.OwnerReferences[i].UID == a.ownerRef.UID {
			meta.OwnerReferences[i] = a.ownerRef
			return
		}
	}
	meta.OwnerReferences = append(meta.OwnerReferences, a.ownerRef)
}

func portInt32(p int) (int32, error) {
	if p < 1 || p > 65535 {
		return 0, fmt.Errorf("port %d out of range [1, 65535]", p)
	}
	return int32(p), nil
}

func mapProtocol(p agentgateway.HTTPProtocol) agw.MCPProtocol {
	if p == agentgateway.SSE {
		return agw.MCPProtocolSSE
	}
	return agw.MCPProtocolStreamableHTTP
}

// policySpec maps a domain Policy to an AgentgatewayPolicySpec. emit=false
// means the policy should be deleted rather than written: the no-auth /
// no-forward case carries no information that the upstream needs.
func policySpec(p agentgateway.Policy) (agw.AgentgatewayPolicySpec, bool) {
	if p.Authn.Type == agentgateway.AuthnTypeNone && !p.Authn.ForwardToken {
		return agw.AgentgatewayPolicySpec{}, false
	}
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
	return spec, true
}
