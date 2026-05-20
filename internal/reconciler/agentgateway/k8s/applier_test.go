package k8s_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	agw "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
	"github.com/giantswarm/muster/internal/reconciler/agentgateway/k8s"
)

func TestApply_Streamable_CreatesAllObjects(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	require.NoError(t, newApplier(c).Apply(t.Context(), streamableConfig()))

	got := &agw.AgentgatewayBackend{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, got))
	require.NotNil(t, got.Spec.MCP)
	require.Len(t, got.Spec.MCP.Targets, 1)
	require.NotNil(t, got.Spec.MCP.Targets[0].Static)
	require.NotNil(t, got.Spec.MCP.Targets[0].Static.Host)
	require.Equal(t, "mcp-kubernetes.mcp-kubernetes.svc.cluster.local", string(*got.Spec.MCP.Targets[0].Static.Host))
	require.Equal(t, int32(8080), got.Spec.MCP.Targets[0].Static.Port)
	require.NotNil(t, got.Spec.MCP.Targets[0].Static.Protocol)
	require.Equal(t, agw.MCPProtocolStreamableHTTP, *got.Spec.MCP.Targets[0].Static.Protocol)
	requireControllerRef(t, got.OwnerReferences)

	route := &gwv1.HTTPRoute{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, route))
	require.Len(t, route.Spec.ParentRefs, 1)
	require.Equal(t, gwv1.ObjectName(gatewayName), route.Spec.ParentRefs[0].Name)
	require.NotNil(t, route.Spec.ParentRefs[0].Namespace)
	require.Equal(t, gwv1.Namespace(gatewayNS), *route.Spec.ParentRefs[0].Namespace)
	require.Len(t, route.Spec.Rules, 1)
	require.Len(t, route.Spec.Rules[0].Matches, 1)
	require.NotNil(t, route.Spec.Rules[0].Matches[0].Path)
	require.NotNil(t, route.Spec.Rules[0].Matches[0].Path.Value)
	require.Equal(t, "/mcp/"+ownerName, *route.Spec.Rules[0].Matches[0].Path.Value)
	require.Len(t, route.Spec.Rules[0].BackendRefs, 1)
	require.NotNil(t, route.Spec.Rules[0].BackendRefs[0].Kind)
	require.Equal(t, gwv1.Kind("AgentgatewayBackend"), *route.Spec.Rules[0].BackendRefs[0].Kind)
	requireControllerRef(t, route.OwnerReferences)

	policy := &agw.AgentgatewayPolicy{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, policy))
	require.Len(t, policy.Spec.TargetRefs, 1)
	require.Equal(t, gwv1.Kind("HTTPRoute"), policy.Spec.TargetRefs[0].Kind)
	require.Equal(t, gwv1.ObjectName(ownerName), policy.Spec.TargetRefs[0].Name)
	require.NotNil(t, policy.Spec.Backend)
	require.NotNil(t, policy.Spec.Backend.Auth)
	require.NotNil(t, policy.Spec.Backend.Auth.Passthrough)
	requireControllerRef(t, policy.OwnerReferences)
}

func TestApply_Stdio_Rejected_NoObjectsCreated(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	err := newApplier(c).Apply(t.Context(), stdioConfig())
	require.Error(t, err)
	require.True(t, errors.Is(err, k8s.ErrStdioNotSupportedInCluster), "expected ErrStdioNotSupportedInCluster, got %v", err)

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

func TestApply_Idempotent_NoChangeOnRepeat(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	a := newApplier(c)
	require.NoError(t, a.Apply(t.Context(), streamableConfig()))

	first := snapshotResources(t, c)
	require.NoError(t, a.Apply(t.Context(), streamableConfig()))
	require.Equal(t, first, snapshotResources(t, c))
}

func TestApply_OAuthNoForward_SkipsPolicy(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	config := streamableConfig()
	config.Policies[0].Authn.ForwardToken = false
	config.Policies[0].Authn.Type = agentgateway.AuthnTypeNone

	require.NoError(t, newApplier(c).Apply(t.Context(), config))

	err := c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, &agw.AgentgatewayPolicy{})
	require.True(t, apierrors.IsNotFound(err), "policy must be skipped for AuthnType=none/no-forward, got err=%v", err)
}

func TestApply_SSEBackend_SetsSSEProtocol(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	require.NoError(t, newApplier(c).Apply(t.Context(), sseConfig()))

	got := &agw.AgentgatewayBackend{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, got))
	require.NotNil(t, got.Spec.MCP.Targets[0].Static.Protocol)
	require.Equal(t, agw.MCPProtocolSSE, *got.Spec.MCP.Targets[0].Static.Protocol)
}

func TestDelete_NoOp(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	a := newApplier(c)
	require.NoError(t, a.Apply(t.Context(), streamableConfig()))

	before := snapshotResources(t, c)
	require.NoError(t, a.Delete(t.Context(), "any-name"))
	require.Equal(t, before, snapshotResources(t, c), "Delete must leave OwnerReference-managed objects untouched")
}

func TestApply_UnknownProtocol_ReturnsError(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	config := streamableConfig()
	target := config.Backends[0].Target.(agentgateway.HTTPTarget)
	target.Protocol = agentgateway.HTTPProtocol("websocket")
	config.Backends[0].Target = target

	err := newApplier(c).Apply(t.Context(), config)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown HTTPProtocol")
	require.Contains(t, err.Error(), "websocket")

	backends := &agw.AgentgatewayBackendList{}
	require.NoError(t, c.List(t.Context(), backends))
	require.Empty(t, backends.Items, "no backend must be persisted when mapProtocol rejects the input")
}

func TestApply_DeferredAuthnFields_NotEmittedAtGateway(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	config := streamableConfig()
	config.Policies[0].Authn.RequiredAudiences = []string{"dex-k8s", "kube-apiserver"}
	config.Policies[0].Authn.TokenExchange = &agentgateway.TokenExchange{
		Enabled:          true,
		DexTokenEndpoint: "https://dex.example.com/token",
		ExpectedIssuer:   "https://dex.example.com",
	}
	config.Policies[0].Authn.AuthorizationServer = &agentgateway.AuthorizationServer{
		Issuer: "https://atlassian.example.com",
		Scopes: "read:mcp",
	}

	require.NoError(t, newApplier(c).Apply(t.Context(), config))

	policy := &agw.AgentgatewayPolicy{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, policy))
	require.NotNil(t, policy.Spec.Backend, "ForwardToken Passthrough must still be emitted")
	require.NotNil(t, policy.Spec.Backend.Auth.Passthrough)
	require.Nil(t, policy.Spec.Traffic, "RequiredAudiences/AuthorizationServer must not become gateway JWTAuthentication/Authorization — muster validates")
	require.Nil(t, policy.Spec.Frontend, "Authn must not produce frontend policy")
}

func TestApply_EmptyConfig_NoObjectsAndNoError(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	require.NoError(t, newApplier(c).Apply(t.Context(), agentgateway.Config{Name: ownerName, Namespace: ownerNamespace}))

	backends := &agw.AgentgatewayBackendList{}
	require.NoError(t, c.List(t.Context(), backends))
	require.Empty(t, backends.Items)

	routes := &gwv1.HTTPRouteList{}
	require.NoError(t, c.List(t.Context(), routes))
	require.Empty(t, routes.Items)
}
