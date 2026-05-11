package kubernetes

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// GetMCPServer retrieves a specific MCPServer resource.
func (k *Client) GetMCPServer(ctx context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error) {
	server := &musterv1alpha1.MCPServer{}
	key := types.NamespacedName{Name: name, Namespace: namespace}

	if err := k.Get(ctx, key, server); err != nil {
		return nil, fmt.Errorf("failed to get MCPServer %s/%s: %w", namespace, name, err)
	}

	return server, nil
}

// ListMCPServers lists all MCPServer resources in a namespace.
func (k *Client) ListMCPServers(ctx context.Context, namespace string) ([]musterv1alpha1.MCPServer, error) {
	serverList := &musterv1alpha1.MCPServerList{}
	listOpts := []client.ListOption{}

	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	if err := k.List(ctx, serverList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list MCPServers in namespace %s: %w", namespace, err)
	}

	return serverList.Items, nil
}

// CreateMCPServer creates a new MCPServer resource.
func (k *Client) CreateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	if err := k.Create(ctx, server); err != nil {
		return fmt.Errorf("failed to create MCPServer %s/%s: %w", server.Namespace, server.Name, err)
	}
	return nil
}

// UpdateMCPServer updates an existing MCPServer resource.
func (k *Client) UpdateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	if err := k.Update(ctx, server); err != nil {
		return fmt.Errorf("failed to update MCPServer %s/%s: %w", server.Namespace, server.Name, err)
	}
	return nil
}

// DeleteMCPServer deletes an MCPServer resource.
func (k *Client) DeleteMCPServer(ctx context.Context, name, namespace string) error {
	server := &musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}

	if err := k.Delete(ctx, server); err != nil {
		return fmt.Errorf("failed to delete MCPServer %s/%s: %w", namespace, name, err)
	}
	return nil
}

// UpdateMCPServerStatus updates only the status subresource of an MCPServer.
func (k *Client) UpdateMCPServerStatus(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	if err := k.Client.Status().Update(ctx, server); err != nil {
		return fmt.Errorf("failed to update MCPServer status %s/%s: %w", server.Namespace, server.Name, err)
	}
	return nil
}
