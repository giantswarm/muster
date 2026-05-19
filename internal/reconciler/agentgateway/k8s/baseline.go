package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	agw "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	"github.com/agentgateway/agentgateway/controller/api/v1alpha1/shared"
)

const baselinePathPrefix = "/mcp/" + MusterBaselineName

// EnsureMusterBaseline upserts the AgentgatewayBackend, HTTPRoute and
// AgentgatewayPolicy that federate muster's own /mcp endpoint through the
// gateway. It is a no-op when Config.MusterBackend is nil and is safe to call
// repeatedly; objects are owned by the operator deployment (managed-by label,
// no OwnerReferences) so their lifetime is independent of any MCPServer CR.
func (a *Applier) EnsureMusterBaseline(ctx context.Context) error {
	if a.cfg.MusterBackend == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	mb := a.cfg.MusterBackend
	if mb.ServiceName == "" || mb.ServiceNamespace == "" || mb.ServicePort <= 0 || mb.ServicePort > 65535 || mb.Path == "" {
		return fmt.Errorf("k8s applier: muster baseline requires non-empty ServiceName, ServiceNamespace, Path and port in [1,65535] (got %+v)", *mb)
	}

	if err := a.ensureBaselineBackend(ctx, mb); err != nil {
		return fmt.Errorf("k8s applier: baseline backend: %w", err)
	}
	if err := a.ensureBaselineRoute(ctx, mb.ServiceNamespace); err != nil {
		return fmt.Errorf("k8s applier: baseline route: %w", err)
	}
	if err := a.ensureBaselinePolicy(ctx, mb.ServiceNamespace); err != nil {
		return fmt.Errorf("k8s applier: baseline policy: %w", err)
	}
	return nil
}

func (a *Applier) ensureBaselineBackend(ctx context.Context, mb *MusterBackendConfig) error {
	host := agw.ShortString(fmt.Sprintf("%s.%s.svc.cluster.local", mb.ServiceName, mb.ServiceNamespace))
	path := agw.LongString(mb.Path)
	protocol := agw.MCPProtocolStreamableHTTP
	port, err := portInt32(mb.ServicePort)
	if err != nil {
		return err
	}

	obj := &agw.AgentgatewayBackend{
		ObjectMeta: metav1.ObjectMeta{Name: MusterBaselineName, Namespace: mb.ServiceNamespace},
	}
	mutate := func() error {
		applyManagedByLabel(&obj.ObjectMeta)
		obj.Spec.MCP = &agw.MCPBackend{
			Targets: []agw.McpTargetSelector{{
				Name: gwv1.SectionName(MusterBaselineName),
				Static: &agw.McpTarget{
					Host:     &host,
					Port:     port,
					Path:     &path,
					Protocol: &protocol,
				},
			}},
		}
		return nil
	}
	return a.createOrUpdate(ctx, obj, mutate)
}

func (a *Applier) ensureBaselineRoute(ctx context.Context, namespace string) error {
	parentRef := gwv1.ParentReference{Name: gwv1.ObjectName(a.cfg.GatewayName)}
	if ns := a.cfg.GatewayNamespace; ns != "" {
		parentNS := gwv1.Namespace(ns)
		parentRef.Namespace = &parentNS
	}
	pathType := gwv1.PathMatchPathPrefix
	pathValue := baselinePathPrefix
	backendGroup := gwv1.Group(groupAgentgateway)
	backendKind := gwv1.Kind(kindAgentgatewayBE)

	obj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: MusterBaselineName, Namespace: namespace},
	}
	mutate := func() error {
		applyManagedByLabel(&obj.ObjectMeta)
		obj.Spec = gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{
				ParentRefs: []gwv1.ParentReference{parentRef},
			},
			Rules: []gwv1.HTTPRouteRule{{
				Matches: []gwv1.HTTPRouteMatch{
					{Path: &gwv1.HTTPPathMatch{Type: &pathType, Value: &pathValue}},
				},
				BackendRefs: []gwv1.HTTPBackendRef{{
					BackendRef: gwv1.BackendRef{
						BackendObjectReference: gwv1.BackendObjectReference{
							Group: &backendGroup,
							Kind:  &backendKind,
							Name:  gwv1.ObjectName(MusterBaselineName),
						},
					},
				}},
			}},
		}
		return nil
	}
	return a.createOrUpdate(ctx, obj, mutate)
}

func (a *Applier) ensureBaselinePolicy(ctx context.Context, namespace string) error {
	httpRouteKind := gwv1.Kind(kindHTTPRoute)
	httpRouteGroup := gwv1.Group(gatewayAPIGroupName)

	obj := &agw.AgentgatewayPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: MusterBaselineName, Namespace: namespace},
	}
	mutate := func() error {
		applyManagedByLabel(&obj.ObjectMeta)
		obj.Spec = agw.AgentgatewayPolicySpec{
			TargetRefs: []shared.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: shared.LocalPolicyTargetReference{
					Group: httpRouteGroup,
					Kind:  httpRouteKind,
					Name:  gwv1.ObjectName(MusterBaselineName),
				},
			}},
			Backend: &agw.BackendFull{
				BackendSimple: agw.BackendSimple{
					Auth: &agw.BackendAuth{Passthrough: &agw.BackendAuthPassthrough{}},
				},
			},
		}
		return nil
	}
	return a.createOrUpdate(ctx, obj, mutate)
}

func applyManagedByLabel(meta *metav1.ObjectMeta) {
	if meta.Labels == nil {
		meta.Labels = map[string]string{}
	}
	meta.Labels[ManagedByLabel] = ManagedByValue
}

// Compile-time check that the *client.Object types used in baseline helpers
// satisfy client.Object — keeps callers honest if upstream signatures shift.
var _ client.Object = (*agw.AgentgatewayBackend)(nil)
var _ client.Object = (*gwv1.HTTPRoute)(nil)
var _ client.Object = (*agw.AgentgatewayPolicy)(nil)
