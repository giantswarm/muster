package credprovider

import (
	"context"
	"errors"
	"fmt"
	"slices"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// A Session binding carries approval settings (the agent's tool list with
// per-entry settings, grant marks) only from a sender Kubernetes allows to
// send them: the bind request carries the sender's own ServiceAccount token
// (agent-manager's, projected with audience muster) in
// ApprovalSenderHeader beside the person's bearer. muster reviews the token
// (TokenReview, audience muster) and asks whether that identity may "bind"
// "toolapprovals" in group muster.giantswarm.io in muster's namespace
// (LocalSubjectAccessReview): a permission on no real resource that only says
// "may send approval settings". The muster chart ships the Role
// (muster-approval-binder); the platform binds it to agent-manager. A bind
// without the token is taken without settings; a token that fails the
// review, or a review that errors, refuses the bind, so an outage never
// strips an agent's Ask or Block.

// ApprovalSenderHeader carries the bind sender's ServiceAccount token.
const ApprovalSenderHeader = "X-Muster-Approval-Sender" //nolint:gosec // G101: a header name, not a credential

// The permission a sender of approval settings needs.
const (
	ApprovalSenderAudience = "muster"
	ApprovalSenderGroup    = "muster.giantswarm.io"
	ApprovalSenderResource = "toolapprovals"
	ApprovalSenderVerb     = "bind"
)

// ErrSenderNotAllowed is a sender whose token does not pass the review.
var ErrSenderNotAllowed = errors.New("the sender may not send approval settings")

// SenderReviewer reviews a bind sender's token.
type SenderReviewer interface {
	// Review returns nil when the token is a valid token for audience muster
	// of an identity allowed to bind toolapprovals, ErrSenderNotAllowed when
	// it is not, and another error when the review could not be made.
	Review(ctx context.Context, token string) error
}

// KubernetesSenderReviewer reviews with the Kubernetes API, as muster's
// ServiceAccount (system:auth-delegator).
type KubernetesSenderReviewer struct {
	Client    kubernetes.Interface
	Namespace string
}

// Review implements SenderReviewer.
func (k KubernetesSenderReviewer) Review(ctx context.Context, token string) error {
	review, err := k.Client.AuthenticationV1().TokenReviews().Create(ctx, &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{Token: token, Audiences: []string{ApprovalSenderAudience}},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("token review: %w", err)
	}
	if review.Status.Error != "" && !review.Status.Authenticated {
		return fmt.Errorf("%w: %s", ErrSenderNotAllowed, review.Status.Error)
	}
	if !review.Status.Authenticated || !slices.Contains(review.Status.Audiences, ApprovalSenderAudience) {
		return ErrSenderNotAllowed
	}
	user := review.Status.User
	extra := make(map[string]authorizationv1.ExtraValue, len(user.Extra))
	for key, values := range user.Extra {
		extra[key] = authorizationv1.ExtraValue(values)
	}
	// A LocalSubjectAccessReview in muster's own namespace: the review is
	// namespaced, so muster needs no cluster-wide right to ask what any
	// identity may do anywhere (M1-MU-S-14).
	access, err := k.Client.AuthorizationV1().LocalSubjectAccessReviews(k.Namespace).Create(ctx, &authorizationv1.LocalSubjectAccessReview{
		ObjectMeta: metav1.ObjectMeta{Namespace: k.Namespace},
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   user.Username,
			Groups: user.Groups,
			UID:    user.UID,
			Extra:  extra,
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: k.Namespace,
				Verb:      ApprovalSenderVerb,
				Group:     ApprovalSenderGroup,
				Resource:  ApprovalSenderResource,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("subject access review: %w", err)
	}
	if !access.Status.Allowed {
		return fmt.Errorf("%w: %s may not %s %s.%s", ErrSenderNotAllowed, user.Username, ApprovalSenderVerb, ApprovalSenderResource, ApprovalSenderGroup)
	}
	return nil
}
