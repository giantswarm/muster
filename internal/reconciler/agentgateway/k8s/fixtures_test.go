package k8s_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	agw "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
	"github.com/giantswarm/muster/internal/reconciler/agentgateway/k8s"
)

const (
	ownerNamespace  = "muster"
	gatewayName     = "muster-agw"
	gatewayNS       = "muster"
	ownerName       = "mcp-kubernetes"
	ownerUID        = "u-1234"
	ownerAPIVersion = "muster.giantswarm.io/v1alpha1"
	ownerKind       = "MCPServer"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, gwv1.Install(s))
	s.AddKnownTypes(
		schema.GroupVersion{Group: "agentgateway.dev", Version: "v1alpha1"},
		&agw.AgentgatewayBackend{}, &agw.AgentgatewayBackendList{},
		&agw.AgentgatewayPolicy{}, &agw.AgentgatewayPolicyList{},
	)
	metav1.AddToGroupVersion(s, schema.GroupVersion{Group: "agentgateway.dev", Version: "v1alpha1"})
	return s
}

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build()
}

func ownerRef() metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         ownerAPIVersion,
		Kind:               ownerKind,
		Name:               ownerName,
		UID:                types.UID(ownerUID),
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
}

func newApplier(c client.Client) *k8s.Applier {
	return k8s.NewApplier(c, ownerRef(), ownerNamespace, k8s.Config{
		GatewayName:      gatewayName,
		GatewayNamespace: gatewayNS,
	})
}

func streamableConfig() agentgateway.Config {
	return agentgateway.Config{
		Name:      ownerName,
		Namespace: ownerNamespace,
		Backends: []agentgateway.Backend{{
			Name: ownerName,
			Target: agentgateway.HTTPTarget{
				Protocol: agentgateway.StreamableHTTP,
				Host:     "mcp-kubernetes.mcp-kubernetes.svc.cluster.local",
				Port:     8080,
				Path:     "",
			},
		}},
		Routes: []agentgateway.Route{{
			Name:       ownerName,
			PathMatch:  "/mcp/" + ownerName,
			BackendRef: ownerName,
			PolicyRef:  ownerName,
		}},
		Policies: []agentgateway.Policy{{
			Name: ownerName,
			Authn: agentgateway.Authn{
				Type:              agentgateway.AuthnTypeOAuth,
				ForwardToken:      true,
				RequiredAudiences: []string{"dex-k8s"},
			},
		}},
	}
}

func sseConfig() agentgateway.Config {
	c := streamableConfig()
	c.Backends[0].Target = agentgateway.HTTPTarget{
		Protocol: agentgateway.SSE,
		Host:     "mcp-kubernetes.mcp-kubernetes.svc.cluster.local",
		Port:     8080,
		Path:     "/sse",
	}
	return c
}

func stdioConfig() agentgateway.Config {
	c := streamableConfig()
	c.Backends[0].Target = agentgateway.StdioTarget{
		Command: "/usr/local/bin/mcp-kubernetes",
		Args:    []string{"--in-cluster"},
		Env:     map[string]string{"FOO": "bar"},
	}
	c.Policies[0].Authn = agentgateway.Authn{Type: agentgateway.AuthnTypeNone}
	return c
}

func requireControllerRef(t *testing.T, refs []metav1.OwnerReference) {
	t.Helper()
	require.Len(t, refs, 1)
	require.Equal(t, types.UID(ownerUID), refs[0].UID)
	require.Equal(t, ownerKind, refs[0].Kind)
	require.Equal(t, ownerAPIVersion, refs[0].APIVersion)
	require.NotNil(t, refs[0].Controller)
	require.True(t, *refs[0].Controller)
	require.NotNil(t, refs[0].BlockOwnerDeletion)
	require.True(t, *refs[0].BlockOwnerDeletion)
}

type resourceSnapshot struct {
	backends []agw.AgentgatewayBackend
	routes   []gwv1.HTTPRoute
	policies []agw.AgentgatewayPolicy
}

func snapshotResources(t *testing.T, c client.Client) resourceSnapshot {
	t.Helper()
	ctx := t.Context()
	var snap resourceSnapshot
	bs := &agw.AgentgatewayBackendList{}
	require.NoError(t, c.List(ctx, bs))
	snap.backends = bs.Items
	rs := &gwv1.HTTPRouteList{}
	require.NoError(t, c.List(ctx, rs))
	snap.routes = rs.Items
	ps := &agw.AgentgatewayPolicyList{}
	require.NoError(t, c.List(ctx, ps))
	snap.policies = ps.Items
	return snap
}

type conflictTracker struct {
	fired int
	limit int
}

func (c *conflictTracker) next() bool {
	if c.fired >= c.limit {
		return false
	}
	c.fired++
	return true
}
