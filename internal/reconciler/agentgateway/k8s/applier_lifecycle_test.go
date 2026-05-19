package k8s_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	agw "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
	"github.com/giantswarm/muster/internal/reconciler/agentgateway/k8s"
)

func TestApply_DropForwardToken_DeletesPreviouslyEmittedPolicy(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	a := newApplier(c)
	require.NoError(t, a.Apply(t.Context(), streamableConfig()))
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, &agw.AgentgatewayPolicy{}))

	config := streamableConfig()
	config.Policies[0].Authn.ForwardToken = false
	config.Policies[0].Authn.Type = agentgateway.AuthnTypeNone
	require.NoError(t, a.Apply(t.Context(), config))

	err := c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, &agw.AgentgatewayPolicy{})
	require.True(t, apierrors.IsNotFound(err))
}

func TestApply_MissingNamespace_ReturnsError(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	config := streamableConfig()
	config.Namespace = ""
	err := newApplier(c).Apply(t.Context(), config)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Config.Namespace")
}

func TestApply_MissingOwnerFields_ReturnsError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		field  string
		mutate func(*metav1.OwnerReference)
	}{
		{"Name", func(r *metav1.OwnerReference) { r.Name = "" }},
		{"UID", func(r *metav1.OwnerReference) { r.UID = "" }},
		{"APIVersion", func(r *metav1.OwnerReference) { r.APIVersion = "" }},
		{"Kind", func(r *metav1.OwnerReference) { r.Kind = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			c := newClient(t)
			ref := ownerRef()
			tc.mutate(&ref)
			a := k8s.NewApplier(c, ref, k8s.Config{GatewayName: gatewayName, GatewayNamespace: gatewayNS})
			err := a.Apply(t.Context(), streamableConfig())
			require.Error(t, err)
			require.Contains(t, err.Error(), "ownerRef."+tc.field)
		})
	}
}

func TestApply_ContextCancelled_ReturnsCtxErr(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := newApplier(c).Apply(ctx, streamableConfig())
	require.ErrorIs(t, err, context.Canceled)
}

func TestApply_ConflictOnUpdate_Retries(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	backoff := &conflictTracker{limit: 1}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if _, ok := obj.(*agw.AgentgatewayBackend); ok && backoff.next() {
					return apierrors.NewConflict(schema.GroupResource{Group: "agentgateway.dev", Resource: "agentgatewaybackends"}, obj.GetName(), errors.New("synthetic conflict"))
				}
				return c.Update(ctx, obj, opts...)
			},
		}).
		Build()

	a := k8s.NewApplier(c, ownerRef(), k8s.Config{GatewayName: gatewayName, GatewayNamespace: gatewayNS})
	require.NoError(t, a.Apply(t.Context(), streamableConfig()))

	mutated := streamableConfig()
	target := mutated.Backends[0].Target.(agentgateway.HTTPTarget)
	target.Host = "alt-host.example.com"
	mutated.Backends[0].Target = target
	require.NoError(t, a.Apply(t.Context(), mutated))
	require.Equal(t, 1, backoff.fired, "the interceptor should have injected exactly one conflict before the retry succeeded")

	got := &agw.AgentgatewayBackend{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, got))
	require.NotNil(t, got.Spec.MCP.Targets[0].Static.Host)
	require.Equal(t, "alt-host.example.com", string(*got.Spec.MCP.Targets[0].Static.Host))
}

func TestApply_PersistentConflict_FailsWithRetryError(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if _, ok := obj.(*agw.AgentgatewayBackend); ok {
					return apierrors.NewConflict(schema.GroupResource{Group: "agentgateway.dev", Resource: "agentgatewaybackends"}, obj.GetName(), errors.New("synthetic"))
				}
				return c.Update(ctx, obj, opts...)
			},
		}).
		Build()

	a := k8s.NewApplier(c, ownerRef(), k8s.Config{GatewayName: gatewayName, GatewayNamespace: gatewayNS})
	require.NoError(t, a.Apply(t.Context(), streamableConfig()))

	mutated := streamableConfig()
	target := mutated.Backends[0].Target.(agentgateway.HTTPTarget)
	target.Host = "alt-host.example.com"
	mutated.Backends[0].Target = target
	err := a.Apply(t.Context(), mutated)
	require.Error(t, err)
	require.Contains(t, err.Error(), "conflict retries exhausted")
}

func TestApply_DeletionCascade_OwnerRefsAreControllerBlocking(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	require.NoError(t, newApplier(c).Apply(t.Context(), streamableConfig()))

	checkRef := func(refs []metav1.OwnerReference) {
		require.Len(t, refs, 1)
		require.True(t, refs[0].Controller != nil && *refs[0].Controller)
		require.True(t, refs[0].BlockOwnerDeletion != nil && *refs[0].BlockOwnerDeletion)
		require.Equal(t, types.UID(ownerUID), refs[0].UID)
		require.Equal(t, ownerKind, refs[0].Kind)
	}

	backend := &agw.AgentgatewayBackend{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, backend))
	checkRef(backend.OwnerReferences)

	route := &gwv1.HTTPRoute{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, route))
	checkRef(route.OwnerReferences)

	policy := &agw.AgentgatewayPolicy{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, policy))
	checkRef(policy.OwnerReferences)
}

func TestApply_DefaultsControllerAndBlockOwnerDeletion_WhenOwnerRefOmitsThem(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	bareOwner := metav1.OwnerReference{
		APIVersion: ownerAPIVersion,
		Kind:       ownerKind,
		Name:       ownerName,
		UID:        types.UID(ownerUID),
		// Controller and BlockOwnerDeletion intentionally left nil.
	}
	a := k8s.NewApplier(c, bareOwner, k8s.Config{GatewayName: gatewayName, GatewayNamespace: gatewayNS})
	require.NoError(t, a.Apply(t.Context(), streamableConfig()))

	backend := &agw.AgentgatewayBackend{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, backend))
	require.Len(t, backend.OwnerReferences, 1)
	require.NotNil(t, backend.OwnerReferences[0].Controller, "Controller pointer must be defaulted to true")
	require.True(t, *backend.OwnerReferences[0].Controller)
	require.NotNil(t, backend.OwnerReferences[0].BlockOwnerDeletion, "BlockOwnerDeletion must be defaulted to true")
	require.True(t, *backend.OwnerReferences[0].BlockOwnerDeletion)
}

func TestApply_HTTPRouteSpec_FullOwnership_RevertsExternalEdits(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	a := newApplier(c)
	require.NoError(t, a.Apply(t.Context(), streamableConfig()))

	route := &gwv1.HTTPRoute{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, route))

	requestMirror := gwv1.HTTPRouteFilterRequestMirror
	route.Spec.Rules[0].Filters = []gwv1.HTTPRouteFilter{{Type: requestMirror}}
	extraParent := gwv1.ParentReference{Name: gwv1.ObjectName("other-gateway")}
	route.Spec.ParentRefs = append(route.Spec.ParentRefs, extraParent)
	require.NoError(t, c.Update(t.Context(), route))

	require.NoError(t, a.Apply(t.Context(), streamableConfig()))

	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, route))
	require.Len(t, route.Spec.ParentRefs, 1, "external ParentRef must be reverted (full-ownership contract)")
	require.Equal(t, gwv1.ObjectName(gatewayName), route.Spec.ParentRefs[0].Name)
	require.Len(t, route.Spec.Rules, 1)
	require.Empty(t, route.Spec.Rules[0].Filters, "external Filters must be reverted (full-ownership contract)")
}

func TestApply_RecreatedMCPServer_ReplacesStaleOwnerRefInPlace(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	require.NoError(t, newApplier(c).Apply(t.Context(), streamableConfig()))

	backend := &agw.AgentgatewayBackend{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, backend))
	require.Len(t, backend.OwnerReferences, 1)
	require.Equal(t, types.UID(ownerUID), backend.OwnerReferences[0].UID)

	const recreatedUID = "u-5678"
	recreatedRef := ownerRef()
	recreatedRef.UID = types.UID(recreatedUID)
	recreated := k8s.NewApplier(c, recreatedRef, k8s.Config{GatewayName: gatewayName, GatewayNamespace: gatewayNS})
	require.NoError(t, recreated.Apply(t.Context(), streamableConfig()))

	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, backend))
	require.Len(t, backend.OwnerReferences, 1, "stale ownerRef from previous MCPServer incarnation must be replaced, not appended")
	require.Equal(t, types.UID(recreatedUID), backend.OwnerReferences[0].UID)
	require.Equal(t, ownerName, backend.OwnerReferences[0].Name)
	require.Equal(t, ownerKind, backend.OwnerReferences[0].Kind)
	require.Equal(t, ownerAPIVersion, backend.OwnerReferences[0].APIVersion)
	require.NotNil(t, backend.OwnerReferences[0].Controller)
	require.True(t, *backend.OwnerReferences[0].Controller)
	require.NotNil(t, backend.OwnerReferences[0].BlockOwnerDeletion)
	require.True(t, *backend.OwnerReferences[0].BlockOwnerDeletion)
}
