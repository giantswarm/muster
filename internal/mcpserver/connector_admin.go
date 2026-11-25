package mcpserver

import (
	"context"
	"errors"
	"fmt"

	authorizationv1 "k8s.io/api/authorization/v1"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

var _ api.MCPServerConnectorAdmin = (*Adapter)(nil)

// IsConnectorAdmin reports whether the caller may update MCPServers in the
// namespace of the server with the given key: a connector admin of it, who
// may change its approval settings. In Kubernetes mode it asks the apiserver
// with a SelfSubjectAccessReview, as the caller, exactly the write the core
// tools would make; Kubernetes RBAC is per namespace, so the review names no
// single server. Without writes-as-caller (filesystem mode) every caller
// writes servers through the core tools with muster's own client, so every
// caller is one.
func (a *Adapter) IsConnectorAdmin(ctx context.Context, serverKey string) (bool, error) {
	if !a.gate.Enabled() {
		return true, nil
	}
	callerClient, errMsg := a.gate.Resolve(ctx)
	if errMsg != "" {
		return false, errors.New(errMsg)
	}
	namespace, _ := api.MCPServerObject(serverKey, a.namespace)
	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      "update",
				Group:     musterv1alpha1.GroupVersion.Group,
				Resource:  "mcpservers",
			},
		},
	}
	if err := callerClient.Create(ctx, review); err != nil {
		return false, fmt.Errorf("review update on mcpservers in namespace %s: %w", namespace, err)
	}
	return review.Status.Allowed, nil
}
