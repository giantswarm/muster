package k8s_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	agw "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway/k8s"
)

const (
	musterSvcName = "muster"
	musterSvcNS   = "muster"
	musterSvcPort = 8090
	musterSvcPath = "/mcp"
)

func newApplierWithBaseline(c client.Client) *k8s.Applier {
	return k8s.NewApplier(c, ownerRef(), k8s.Config{
		GatewayName:      gatewayName,
		GatewayNamespace: gatewayNS,
		MusterBackend: &k8s.MusterBackendConfig{
			ServiceName:      musterSvcName,
			ServiceNamespace: musterSvcNS,
			ServicePort:      musterSvcPort,
			Path:             musterSvcPath,
		},
	})
}

func TestEnsureMusterBaseline_NoOpWhenUnset(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	require.NoError(t, newApplier(c).EnsureMusterBaseline(t.Context()))

	backends := &agw.AgentgatewayBackendList{}
	require.NoError(t, c.List(t.Context(), backends))
	require.Empty(t, backends.Items)

	routes := &gwv1.HTTPRouteList{}
	require.NoError(t, c.List(t.Context(), routes))
	require.Empty(t, routes.Items)

	policies := &agw.AgentgatewayPolicyList{}
	require.NoError(t, c.List(t.Context(), policies))
	require.Empty(t, policies.Items)
}

func TestEnsureMusterBaseline_CreatesAllThreeObjects(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	require.NoError(t, newApplierWithBaseline(c).EnsureMusterBaseline(t.Context()))

	be := &agw.AgentgatewayBackend{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: musterSvcNS, Name: k8s.MusterBaselineName}, be))
	require.Empty(t, be.OwnerReferences, "baseline objects must not carry MCPServer owner refs")
	require.Equal(t, k8s.ManagedByValue, be.Labels[k8s.ManagedByLabel])
	require.NotNil(t, be.Spec.MCP)
	require.Len(t, be.Spec.MCP.Targets, 1)
	require.NotNil(t, be.Spec.MCP.Targets[0].Static)
	require.NotNil(t, be.Spec.MCP.Targets[0].Static.Host)
	require.Equal(t, "muster.muster.svc.cluster.local", string(*be.Spec.MCP.Targets[0].Static.Host))
	require.Equal(t, int32(musterSvcPort), be.Spec.MCP.Targets[0].Static.Port)
	require.NotNil(t, be.Spec.MCP.Targets[0].Static.Protocol)
	require.Equal(t, agw.MCPProtocolStreamableHTTP, *be.Spec.MCP.Targets[0].Static.Protocol)

	rt := &gwv1.HTTPRoute{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: musterSvcNS, Name: k8s.MusterBaselineName}, rt))
	require.Empty(t, rt.OwnerReferences)
	require.Equal(t, k8s.ManagedByValue, rt.Labels[k8s.ManagedByLabel])
	require.Len(t, rt.Spec.ParentRefs, 1)
	require.Equal(t, gwv1.ObjectName(gatewayName), rt.Spec.ParentRefs[0].Name)
	require.NotNil(t, rt.Spec.Rules[0].Matches[0].Path.Value)
	require.Equal(t, "/mcp/muster", *rt.Spec.Rules[0].Matches[0].Path.Value)
	require.NotNil(t, rt.Spec.Rules[0].BackendRefs[0].Kind)
	require.Equal(t, gwv1.Kind("AgentgatewayBackend"), *rt.Spec.Rules[0].BackendRefs[0].Kind)
	require.Equal(t, gwv1.ObjectName(k8s.MusterBaselineName), rt.Spec.Rules[0].BackendRefs[0].Name)

	pol := &agw.AgentgatewayPolicy{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: musterSvcNS, Name: k8s.MusterBaselineName}, pol))
	require.Empty(t, pol.OwnerReferences)
	require.Equal(t, k8s.ManagedByValue, pol.Labels[k8s.ManagedByLabel])
	require.Len(t, pol.Spec.TargetRefs, 1)
	require.Equal(t, gwv1.Kind("HTTPRoute"), pol.Spec.TargetRefs[0].Kind)
	require.Equal(t, gwv1.ObjectName(k8s.MusterBaselineName), pol.Spec.TargetRefs[0].Name)
	require.NotNil(t, pol.Spec.Backend)
	require.NotNil(t, pol.Spec.Backend.Auth)
	require.NotNil(t, pol.Spec.Backend.Auth.Passthrough)
}

func TestEnsureMusterBaseline_Idempotent(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	a := newApplierWithBaseline(c)
	require.NoError(t, a.EnsureMusterBaseline(t.Context()))

	first := snapshotResources(t, c)
	require.NoError(t, a.EnsureMusterBaseline(t.Context()))
	require.Equal(t, first, snapshotResources(t, c))
}

func TestEnsureMusterBaseline_CoexistsWithPerMCPServerApply(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	a := newApplierWithBaseline(c)
	require.NoError(t, a.EnsureMusterBaseline(t.Context()))
	require.NoError(t, a.Apply(t.Context(), streamableConfig()))

	baselineBE := &agw.AgentgatewayBackend{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: musterSvcNS, Name: k8s.MusterBaselineName}, baselineBE))
	require.Empty(t, baselineBE.OwnerReferences)

	mcpserverBE := &agw.AgentgatewayBackend{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, mcpserverBE))
	requireControllerRef(t, mcpserverBE.OwnerReferences)
}

func TestEnsureMusterBaseline_RejectsIncompleteConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mb   k8s.MusterBackendConfig
	}{
		{"empty service name", k8s.MusterBackendConfig{ServiceName: "", ServiceNamespace: musterSvcNS, ServicePort: musterSvcPort, Path: musterSvcPath}},
		{"empty namespace", k8s.MusterBackendConfig{ServiceName: musterSvcName, ServiceNamespace: "", ServicePort: musterSvcPort, Path: musterSvcPath}},
		{"zero port", k8s.MusterBackendConfig{ServiceName: musterSvcName, ServiceNamespace: musterSvcNS, ServicePort: 0, Path: musterSvcPath}},
		{"out-of-range port", k8s.MusterBackendConfig{ServiceName: musterSvcName, ServiceNamespace: musterSvcNS, ServicePort: 70000, Path: musterSvcPath}},
		{"empty path", k8s.MusterBackendConfig{ServiceName: musterSvcName, ServiceNamespace: musterSvcNS, ServicePort: musterSvcPort, Path: ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(t)
			mb := tc.mb
			a := k8s.NewApplier(c, ownerRef(), k8s.Config{
				GatewayName:      gatewayName,
				GatewayNamespace: gatewayNS,
				MusterBackend:    &mb,
			})
			require.Error(t, a.EnsureMusterBaseline(t.Context()))

			be := &agw.AgentgatewayBackend{}
			err := c.Get(t.Context(), client.ObjectKey{Namespace: musterSvcNS, Name: k8s.MusterBaselineName}, be)
			require.True(t, apierrors.IsNotFound(err), "rejected baseline must not write objects")
		})
	}
}
