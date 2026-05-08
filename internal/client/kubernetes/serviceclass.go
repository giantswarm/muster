package kubernetes

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// GetServiceClass retrieves a specific ServiceClass resource.
func (k *Client) GetServiceClass(ctx context.Context, name, namespace string) (*musterv1alpha1.ServiceClass, error) {
	serviceClass := &musterv1alpha1.ServiceClass{}
	key := client.ObjectKey{Name: name, Namespace: namespace}

	if err := k.Get(ctx, key, serviceClass); err != nil {
		return nil, fmt.Errorf("failed to get ServiceClass %s/%s: %w", namespace, name, err)
	}

	return serviceClass, nil
}

// ListServiceClasses lists all ServiceClass resources in a namespace.
func (k *Client) ListServiceClasses(ctx context.Context, namespace string) ([]musterv1alpha1.ServiceClass, error) {
	serviceClassList := &musterv1alpha1.ServiceClassList{}
	opts := []client.ListOption{client.InNamespace(namespace)}

	if err := k.List(ctx, serviceClassList, opts...); err != nil {
		return nil, fmt.Errorf("failed to list ServiceClasses in namespace %s: %w", namespace, err)
	}

	return serviceClassList.Items, nil
}

// CreateServiceClass creates a new ServiceClass resource.
func (k *Client) CreateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	if err := k.Create(ctx, serviceClass); err != nil {
		return fmt.Errorf("failed to create ServiceClass %s/%s: %w", serviceClass.Namespace, serviceClass.Name, err)
	}
	return nil
}

// UpdateServiceClass updates an existing ServiceClass resource.
func (k *Client) UpdateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	if err := k.Update(ctx, serviceClass); err != nil {
		return fmt.Errorf("failed to update ServiceClass %s/%s: %w", serviceClass.Namespace, serviceClass.Name, err)
	}
	return nil
}

// DeleteServiceClass deletes a ServiceClass resource.
func (k *Client) DeleteServiceClass(ctx context.Context, name, namespace string) error {
	serviceClass := &musterv1alpha1.ServiceClass{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}

	if err := k.Delete(ctx, serviceClass); err != nil {
		return fmt.Errorf("failed to delete ServiceClass %s/%s: %w", namespace, name, err)
	}
	return nil
}

// UpdateServiceClassStatus updates only the status subresource of a ServiceClass.
func (k *Client) UpdateServiceClassStatus(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	if err := k.Client.Status().Update(ctx, serviceClass); err != nil {
		return fmt.Errorf("failed to update ServiceClass status %s/%s: %w", serviceClass.Namespace, serviceClass.Name, err)
	}
	return nil
}
