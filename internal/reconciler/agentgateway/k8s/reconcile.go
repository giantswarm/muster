package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	agw "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
)

const (
	groupAgentgateway   = "agentgateway.dev"
	kindAgentgatewayBE  = "AgentgatewayBackend"
	kindHTTPRoute       = "HTTPRoute"
	gatewayAPIGroupName = gwv1.GroupName
)

func (a *Applier) reconcileBackend(ctx context.Context, namespace string, b agentgateway.Backend) error {
	target, ok := b.Target.(agentgateway.HTTPTarget)
	if !ok {
		return fmt.Errorf("backend %q: expected HTTPTarget, got %T", b.Name, b.Target)
	}
	port, err := portInt32(target.Port)
	if err != nil {
		return err
	}
	host := agw.ShortString(target.Host)
	path := agw.LongString(target.Path)
	protocol := mapProtocol(target.Protocol)

	obj := &agw.AgentgatewayBackend{
		ObjectMeta: metav1.ObjectMeta{Name: b.Name, Namespace: namespace},
	}
	mutate := func() error {
		a.applyOwner(&obj.ObjectMeta)
		obj.Spec.MCP = &agw.MCPBackend{
			Targets: []agw.McpTargetSelector{
				{
					Name: gwv1.SectionName(b.Name),
					Static: &agw.McpTarget{
						Host:     &host,
						Port:     port,
						Path:     &path,
						Protocol: &protocol,
					},
				},
			},
		}
		return nil
	}
	return a.createOrUpdate(ctx, obj, mutate)
}

func (a *Applier) reconcileRoute(ctx context.Context, namespace string, r agentgateway.Route) error {
	obj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: r.Name, Namespace: namespace},
	}
	parentRef := gwv1.ParentReference{Name: gwv1.ObjectName(a.cfg.GatewayName)}
	if ns := a.cfg.GatewayNamespace; ns != "" {
		parentNS := gwv1.Namespace(ns)
		parentRef.Namespace = &parentNS
	}
	pathType := gwv1.PathMatchPathPrefix
	pathValue := r.PathMatch
	backendGroup := gwv1.Group(groupAgentgateway)
	backendKind := gwv1.Kind(kindAgentgatewayBE)
	mutate := func() error {
		a.applyOwner(&obj.ObjectMeta)
		obj.Spec = gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{
				ParentRefs: []gwv1.ParentReference{parentRef},
			},
			Rules: []gwv1.HTTPRouteRule{
				{
					Matches: []gwv1.HTTPRouteMatch{
						{Path: &gwv1.HTTPPathMatch{Type: &pathType, Value: &pathValue}},
					},
					BackendRefs: []gwv1.HTTPBackendRef{
						{
							BackendRef: gwv1.BackendRef{
								BackendObjectReference: gwv1.BackendObjectReference{
									Group: &backendGroup,
									Kind:  &backendKind,
									Name:  gwv1.ObjectName(r.BackendRef),
								},
							},
						},
					},
				},
			},
		}
		return nil
	}
	return a.createOrUpdate(ctx, obj, mutate)
}

func (a *Applier) reconcilePolicy(ctx context.Context, namespace string, p agentgateway.Policy) error {
	obj := &agw.AgentgatewayPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: namespace},
	}
	if p.Authn.Type == agentgateway.AuthnTypeNone && !p.Authn.ForwardToken {
		return a.deleteIfExists(ctx, obj)
	}
	mutate := func() error {
		a.applyOwner(&obj.ObjectMeta)
		obj.Spec = policySpec(p)
		return nil
	}
	return a.createOrUpdate(ctx, obj, mutate)
}
