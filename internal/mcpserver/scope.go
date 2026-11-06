package mcpserver

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// Scope selects the MCPServer objects muster serves in Kubernetes mode, as
// prometheus-operator's serviceMonitorNamespaceSelector and
// serviceMonitorSelector do for ServiceMonitors.
type Scope struct {
	// Namespaces selects the namespaces besides muster's whose servers are
	// served: nil, none; labels.Everything(), every one; otherwise the
	// namespaces whose labels match.
	Namespaces labels.Selector

	// Servers selects the served objects by their labels, in muster's
	// namespace too: nil, every one.
	Servers labels.Selector
}

// servesObject reports whether the object's labels match Servers.
func (s Scope) servesObject(server *musterv1alpha1.MCPServer) bool {
	return s.Servers == nil || s.Servers.Matches(labels.Set(server.Labels))
}

// servedNamespace reports whether the servers of namespace are served: those
// of musterNamespace always, those of another namespace when Namespaces
// selects it. A namespace that does not exist is not served.
func (s Scope) servedNamespace(ctx context.Context, c ctrlclient.Reader, namespace, musterNamespace string) (bool, error) {
	if namespace == musterNamespace {
		return true, nil
	}
	if s.Namespaces == nil {
		return false, nil
	}
	if s.Namespaces.Empty() {
		return true, nil
	}
	var ns corev1.Namespace
	if err := c.Get(ctx, ctrlclient.ObjectKey{Name: namespace}, &ns); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("read namespace %s: %w", namespace, err)
	}
	return s.Namespaces.Matches(labels.Set(ns.Labels)), nil
}

// servedNamespaces returns the test servedNamespace answers, with one list of
// the namespaces Namespaces selects.
func (s Scope) servedNamespaces(ctx context.Context, c ctrlclient.Reader, musterNamespace string) (func(string) bool, error) {
	if s.Namespaces == nil {
		return func(namespace string) bool { return namespace == musterNamespace }, nil
	}
	if s.Namespaces.Empty() {
		return func(string) bool { return true }, nil
	}
	var list corev1.NamespaceList
	if err := c.List(ctx, &list, ctrlclient.MatchingLabelsSelector{Selector: s.Namespaces}); err != nil {
		return nil, fmt.Errorf("list the namespaces of mcpServerNamespaceSelector: %w", err)
	}
	selected := make(map[string]bool, len(list.Items)+1)
	selected[musterNamespace] = true
	for i := range list.Items {
		selected[list.Items[i].Name] = true
	}
	return func(namespace string) bool { return selected[namespace] }, nil
}
