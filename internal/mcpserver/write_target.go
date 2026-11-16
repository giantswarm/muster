package mcpserver

import (
	"context"
	"errors"
	"fmt"

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

// validateWriteTarget refuses a key that names no object muster may write
// with verb: an unsafe name, a namespace the caller may not verb MCPServers
// in, or a namespace muster does not serve, checked in that order.
func (a *Adapter) validateWriteTarget(ctx context.Context, key, verb string) *api.CallToolResult {
	if a.client == nil || !a.client.IsKubernetesMode() {
		if err := api.ValidateResourceName(key); err != nil {
			result, _ := simpleError(err.Error())
			return result
		}
		return nil
	}
	namespace, name := api.SplitMCPServerKey(key)
	if !api.IsTenantMCPServerKey(key) {
		namespace = a.namespace
	}
	for _, value := range []string{namespace, name} {
		if err := api.ValidateResourceName(value); err != nil {
			result, _ := simpleError(err.Error())
			return result
		}
	}
	if refused := a.authorizeCaller(ctx, key, verb); refused != nil {
		return refused
	}
	_, _, served, err := a.object(ctx, key)
	if err != nil {
		result, _ := simpleError(fmt.Sprintf("Cannot check MCP server namespace: %v", err))
		return result
	}
	if !served {
		result, _ := simpleError(fmt.Sprintf("MCP server namespace %q is not served by muster", namespace))
		return result
	}
	return nil
}

// authorizeCaller asks the API server, as the caller, whether the caller may
// verb the MCPServer a key outside muster's namespace names, with a
// SelfSubjectAccessReview (what `kubectl auth can-i` sends). It runs before
// muster reads that namespace or its servers as itself, so a refused caller
// gets the same answer whether or not the namespace or the server exists.
func (a *Adapter) authorizeCaller(ctx context.Context, key, verb string) *api.CallToolResult {
	if !api.IsTenantMCPServerKey(key) || !a.gate.Enabled() {
		return nil
	}
	caller, errMsg := a.gate.Resolve(ctx)
	if errMsg != "" {
		result, _ := simpleError(errMsg)
		return result
	}
	namespace, name := api.SplitMCPServerKey(key)
	attributes := &authorizationv1.ResourceAttributes{
		Namespace: namespace,
		Verb:      verb,
		Group:     musterv1alpha1.GroupVersion.Group,
		Resource:  "mcpservers",
	}
	// RBAC never matches resourceNames on create: the object has no name yet.
	if verb != "create" {
		attributes.Name = name
	}
	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: attributes},
	}
	if err := caller.Create(ctx, review); err != nil {
		if msg := a.describeWriteAuthError(err, verb, key); msg != "" {
			result, _ := simpleError(msg)
			return result
		}
		result, _ := simpleError(fmt.Sprintf("Cannot check your permission to %s MCP server '%s': %v", verb, key, err))
		return result
	}
	if review.Status.Allowed {
		return nil
	}
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: attributes.Group, Resource: attributes.Resource}, name, errors.New(review.Status.Reason))
	result, _ := simpleError(a.describeWriteAuthError(forbidden, verb, key))
	return result
}

// refuseShadowed refuses, before anything is written, a server outside
// muster's namespace whose exposed names a server of muster's namespace takes:
// the reconciler would refuse to serve it (api.ValidateNotShadowed).
func (a *Adapter) refuseShadowed(ctx context.Context, server, namespace string) *api.CallToolResult {
	if a.client == nil || !a.client.IsKubernetesMode() {
		return nil
	}
	if err := api.ValidateNotShadowed(ctx, namespace, a.namespace, a.listOwnNamespace); err != nil {
		result, _ := simpleError(fmt.Sprintf("Invalid MCP server definition %q: %v", server, err))
		return result
	}
	return nil
}

// listOwnNamespace lists the served servers of muster's namespace, the only
// ones that can shadow a server of another namespace.
func (a *Adapter) listOwnNamespace(ctx context.Context) ([]api.MCPServerInfo, error) {
	servers, err := a.client.ListMCPServers(ctx, a.namespace)
	if err != nil {
		return nil, err
	}
	result := make([]api.MCPServerInfo, 0, len(servers))
	for i := range servers {
		if a.scope.servesObject(&servers[i]) {
			result = append(result, convertCRDToInfo(&servers[i]))
		}
	}
	return result, nil
}
