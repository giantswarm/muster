package kubernetes

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// getResource reads a typed object by namespace/name. Caller allocates obj
// (matches the controller-runtime client.Get convention).
func (k *Client) getResource(ctx context.Context, name, namespace, kind string, obj client.Object) error {
	if err := k.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
		return fmt.Errorf("failed to get %s %s/%s: %w", kind, namespace, name, err)
	}
	return nil
}

// listResources populates list with all objects in the given namespace. An
// empty namespace lists across all namespaces.
func (k *Client) listResources(ctx context.Context, namespace, kind string, list client.ObjectList) error {
	var opts []client.ListOption
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := k.List(ctx, list, opts...); err != nil {
		return fmt.Errorf("failed to list %s in namespace %s: %w", kind, namespace, err)
	}
	return nil
}

func (k *Client) createResource(ctx context.Context, kind string, obj client.Object) error {
	if err := k.Create(ctx, obj); err != nil {
		return fmt.Errorf("failed to create %s %s/%s: %w", kind, obj.GetNamespace(), obj.GetName(), err)
	}
	return nil
}

func (k *Client) updateResource(ctx context.Context, kind string, obj client.Object) error {
	if err := k.Update(ctx, obj); err != nil {
		return fmt.Errorf("failed to update %s %s/%s: %w", kind, obj.GetNamespace(), obj.GetName(), err)
	}
	return nil
}

// deleteResource removes the resource identified by namespace/name. The
// caller passes a fresh typed object (its TypeMeta tells controller-runtime
// which API to call); name and namespace are set here.
func (k *Client) deleteResource(ctx context.Context, name, namespace, kind string, obj client.Object) error {
	obj.SetName(name)
	obj.SetNamespace(namespace)
	if err := k.Delete(ctx, obj); err != nil {
		return fmt.Errorf("failed to delete %s %s/%s: %w", kind, namespace, name, err)
	}
	return nil
}

func (k *Client) updateResourceStatus(ctx context.Context, kind string, obj client.Object) error {
	if err := k.Client.Status().Update(ctx, obj); err != nil {
		return fmt.Errorf("failed to update %s status %s/%s: %w", kind, obj.GetNamespace(), obj.GetName(), err)
	}
	return nil
}
