package credprovider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// fakeCluster answers TokenReviews and LocalSubjectAccessReviews: the token
// "agent-manager" is agent-manager's ServiceAccount, for audience muster,
// allowed to bind toolapprovals; "other" is another ServiceAccount, not
// allowed; "wrong-audience" is valid for another audience only.
func fakeCluster(t *testing.T, reviewErr error) (*fake.Clientset, *authorizationv1.SubjectAccessReviewSpec) {
	t.Helper()
	client := fake.NewClientset()
	var asked authorizationv1.SubjectAccessReviewSpec
	client.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if reviewErr != nil {
			return true, nil, reviewErr
		}
		review := action.(k8stesting.CreateAction).GetObject().(*authenticationv1.TokenReview)
		assert.Equal(t, []string{"muster"}, review.Spec.Audiences)
		switch review.Spec.Token {
		case "agent-manager", "other":
			review.Status = authenticationv1.TokenReviewStatus{Authenticated: true, Audiences: []string{"muster"},
				User: authenticationv1.UserInfo{Username: "system:serviceaccount:agent-platform:" + review.Spec.Token, UID: "u1", Groups: []string{"system:serviceaccounts"}}}
		case "wrong-audience":
			review.Status = authenticationv1.TokenReviewStatus{Authenticated: false, Error: "token audiences do not match"}
		default:
			review.Status = authenticationv1.TokenReviewStatus{Authenticated: false}
		}
		return true, review, nil
	})
	client.PrependReactor("create", "localsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.LocalSubjectAccessReview)
		if action.GetNamespace() != review.Spec.ResourceAttributes.Namespace {
			return true, nil, errors.New("a local review asks in its own namespace")
		}
		asked = review.Spec
		review.Status.Allowed = review.Spec.User == "system:serviceaccount:agent-platform:agent-manager"
		return true, review, nil
	})
	return client, &asked
}

func TestKubernetesSenderReviewer(t *testing.T) {
	client, asked := fakeCluster(t, nil)
	reviewer := KubernetesSenderReviewer{Client: client, Namespace: "muster"}
	ctx := context.Background()

	require.NoError(t, reviewer.Review(ctx, "agent-manager"))
	assert.Equal(t, &authorizationv1.ResourceAttributes{Namespace: "muster", Verb: "bind", Group: "muster.giantswarm.io", Resource: "toolapprovals"}, asked.ResourceAttributes)
	assert.Equal(t, []string{"system:serviceaccounts"}, asked.Groups)
	assert.Equal(t, "u1", asked.UID)

	for _, token := range []string{"other", "wrong-audience", "forged"} {
		require.ErrorIs(t, reviewer.Review(ctx, token), ErrSenderNotAllowed, token)
	}

	broken, _ := fakeCluster(t, errors.New("apiserver unavailable"))
	err := KubernetesSenderReviewer{Client: broken, Namespace: "muster"}.Review(ctx, "agent-manager")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrSenderNotAllowed)
}

func bindWithSender(t *testing.T, p *Provider, token string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, BindingsPath, strings.NewReader(testBindBody))
	if token != "" {
		req.Header.Set(ApprovalSenderHeader, token)
	}
	asPerson("alice", p.HandleBind).ServeHTTP(rec, req)
	return rec
}

// A bind carries approval settings only from a sender that passes the
// review; without a sender token it is taken without them; a failing token
// or review refuses it.
func TestBindReviewsTheApprovalSender(t *testing.T) {
	client, _ := fakeCluster(t, nil)
	p, store := newTestProvider(t, fakeGrants{})
	p.senders = KubernetesSenderReviewer{Client: client, Namespace: "muster"}
	ctx := context.Background()

	require.Equal(t, http.StatusNoContent, bindWithSender(t, p, "").Code)
	b, err := store.Get(ctx, testActor)
	require.NoError(t, err)
	assert.False(t, b.ApprovalSender, "no sender token, no settings")

	require.Equal(t, http.StatusNoContent, bindWithSender(t, p, "agent-manager").Code)
	b, err = store.Get(ctx, testActor)
	require.NoError(t, err)
	assert.True(t, b.ApprovalSender)

	for _, token := range []string{"other", "wrong-audience", "forged"} {
		assert.Equal(t, http.StatusForbidden, bindWithSender(t, p, token).Code, token)
	}

	broken, _ := fakeCluster(t, errors.New("apiserver unavailable"))
	p.senders = KubernetesSenderReviewer{Client: broken, Namespace: "muster"}
	assert.Equal(t, http.StatusServiceUnavailable, bindWithSender(t, p, "agent-manager").Code, "a review error refuses, never strips")

	p.senders = nil
	assert.Equal(t, http.StatusForbidden, bindWithSender(t, p, "agent-manager").Code, "muster that cannot review refuses")
}
