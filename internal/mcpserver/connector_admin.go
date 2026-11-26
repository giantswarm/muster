package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

var _ api.MCPServerConnectorAdmin = (*Adapter)(nil)

// connectorAdminTimeout bounds one review: a slow API server must not hold a
// listing. A review that times out counts as not an admin.
const connectorAdminTimeout = 5 * time.Second

// connectorAdmins returns a lookup of whether the caller is a connector admin
// of a server, reviewing each namespace once (RBAC is per namespace). A
// failed review counts as not an admin.
func (a *Adapter) connectorAdmins(ctx context.Context) func(serverKey string) bool {
	memo := map[string]bool{}
	return func(serverKey string) bool {
		namespace, _ := api.MCPServerObject(serverKey, a.namespace)
		if admin, done := memo[namespace]; done {
			return admin
		}
		admin, err := a.IsConnectorAdmin(ctx, serverKey)
		memo[namespace] = err == nil && admin
		return memo[namespace]
	}
}

// withoutBlockedNames returns the server as a caller who is not its
// connector admin may see it: its Block entries left out, since Block hides
// a tool, its name included. A connector admin sees them all.
func withoutBlockedNames(server api.MCPServerInfo, admin bool) api.MCPServerInfo {
	if admin || server.Approval == nil {
		return server
	}
	approval := &api.MCPServerApproval{Rule: server.Approval.Rule}
	for tool, setting := range server.Approval.Tools {
		if setting == api.ApprovalBlock {
			continue
		}
		if approval.Tools == nil {
			approval.Tools = map[string]string{}
		}
		approval.Tools[tool] = setting
	}
	server.Approval = approval
	return server
}

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
	ctx, cancel := context.WithTimeout(ctx, connectorAdminTimeout)
	defer cancel()
	if err := callerClient.Create(ctx, review); err != nil {
		return false, fmt.Errorf("review update on mcpservers in namespace %s: %w", namespace, err)
	}
	return review.Status.Allowed, nil
}
