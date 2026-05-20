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
			a := k8s.NewApplier(c, ref, ownerNamespace, k8s.Config{GatewayName: gatewayName, GatewayNamespace: gatewayNS})
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

	a := k8s.NewApplier(c, ownerRef(), ownerNamespace, k8s.Config{GatewayName: gatewayName, GatewayNamespace: gatewayNS})
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

	a := k8s.NewApplier(c, ownerRef(), ownerNamespace, k8s.Config{GatewayName: gatewayName, GatewayNamespace: gatewayNS})
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
}

// TestDelete_TearsDownAgentgatewayStack covers the spec.suspended path: the
// reconciler calls Applier.Delete to remove the agentgateway stack while the
// MCPServer CRD persists. OwnerReferences cascade only fires on owner
// deletion, so suspend-without-delete must be handled here.
func TestDelete_TearsDownAgentgatewayStack(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	a := newApplier(c)
	require.NoError(t, a.Apply(t.Context(), streamableConfig()))

	// All three resources exist after Apply.
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, &agw.AgentgatewayBackend{}))
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, &agw.AgentgatewayPolicy{}))
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, &gwv1.HTTPRoute{}))

	require.NoError(t, a.Delete(t.Context(), ownerName))

	// All three are gone.
	err := c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, &agw.AgentgatewayBackend{})
	require.True(t, apierrors.IsNotFound(err), "AgentgatewayBackend should be deleted, got %v", err)
	err = c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, &agw.AgentgatewayPolicy{})
	require.True(t, apierrors.IsNotFound(err), "AgentgatewayPolicy should be deleted, got %v", err)
	err = c.Get(t.Context(), client.ObjectKey{Namespace: ownerNamespace, Name: ownerName}, &gwv1.HTTPRoute{})
	require.True(t, apierrors.IsNotFound(err), "HTTPRoute should be deleted, got %v", err)
}

// TestDelete_NotFound_IsIdempotent verifies that calling Delete after the
// resources are already gone (e.g. a second suspend-reconcile) is a no-op.
func TestDelete_NotFound_IsIdempotent(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	a := newApplier(c)
	require.NoError(t, a.Delete(t.Context(), ownerName))
	require.NoError(t, a.Delete(t.Context(), ownerName))
}
