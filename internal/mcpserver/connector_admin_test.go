package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/giantswarm/muster/v5/internal/callerwrite"
	"github.com/giantswarm/muster/v5/internal/server"
)

// reviewHarness enables writes-as-caller with a client whose
// SelfSubjectAccessReviews are answered by allow, recording each review.
func reviewHarness(t *testing.T, allow func(authorizationv1.ResourceAttributes) bool, reviewErr error) (*Adapter, *[]authorizationv1.ResourceAttributes, *[]string) {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	var reviews []authorizationv1.ResourceAttributes
	var tokens []string
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(_ context.Context, _ ctrlclient.WithWatch, obj ctrlclient.Object, _ ...ctrlclient.CreateOption) error {
			review, ok := obj.(*authorizationv1.SelfSubjectAccessReview)
			if !ok {
				t.Fatalf("unexpected create of %T", obj)
			}
			if reviewErr != nil {
				return reviewErr
			}
			reviews = append(reviews, *review.Spec.ResourceAttributes)
			review.Status.Allowed = allow(*review.Spec.ResourceAttributes)
			return nil
		},
	}).Build()

	adapter := NewAdapterWithClient(&stubMusterClient{}, "muster")
	adapter.EnableWritesAsCaller(func(token string) (ctrlclient.Client, error) {
		tokens = append(tokens, token)
		return fakeClient, nil
	}, "")
	return adapter, &reviews, &tokens
}

func TestIsConnectorAdmin(t *testing.T) {
	token := testJWT(t, callerwrite.DefaultKubernetesAudience)
	ctx := server.ContextWithIDToken(context.Background(), token)

	t.Run("filesystem mode: every caller writes through the core tools", func(t *testing.T) {
		adapter := NewAdapterWithClient(&stubMusterClient{filesystem: true}, "muster")
		admin, err := adapter.IsConnectorAdmin(context.Background(), "github")
		require.NoError(t, err)
		assert.True(t, admin)
	})

	t.Run("asks Kubernetes, as the caller, for update on mcpservers in the server's namespace", func(t *testing.T) {
		adapter, reviews, tokens := reviewHarness(t, func(a authorizationv1.ResourceAttributes) bool {
			return a.Namespace == "muster"
		}, nil)

		admin, err := adapter.IsConnectorAdmin(ctx, "github")
		require.NoError(t, err)
		assert.True(t, admin)

		admin, err = adapter.IsConnectorAdmin(ctx, "team-a/github")
		require.NoError(t, err)
		assert.False(t, admin, "RBAC refuses the tenant namespace")

		require.Equal(t, []string{token, token}, *tokens, "the review runs with the caller's own token")
		require.Len(t, *reviews, 2)
		assert.Equal(t, authorizationv1.ResourceAttributes{
			Namespace: "muster", Verb: "update", Group: "muster.giantswarm.io", Resource: "mcpservers",
		}, (*reviews)[0], "per namespace: the review names no single server")
		assert.Equal(t, "team-a", (*reviews)[1].Namespace)
	})

	t.Run("a caller without a token is no connector admin", func(t *testing.T) {
		adapter, reviews, _ := reviewHarness(t, func(authorizationv1.ResourceAttributes) bool { return true }, nil)
		admin, err := adapter.IsConnectorAdmin(context.Background(), "github")
		require.Error(t, err)
		assert.False(t, admin)
		assert.Empty(t, *reviews)
	})

	t.Run("a failed review is no connector admin", func(t *testing.T) {
		adapter, _, _ := reviewHarness(t, nil, errors.New("apiserver unavailable"))
		admin, err := adapter.IsConnectorAdmin(ctx, "github")
		require.ErrorContains(t, err, "apiserver unavailable")
		assert.False(t, admin)
	})
}
