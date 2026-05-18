package kubernetes

import (
	"context"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

func (k *Client) GetMCPServer(ctx context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error) {
	var obj musterv1alpha1.MCPServer
	if err := k.getResource(ctx, name, namespace, kindMCPServer, &obj); err != nil {
		return nil, err
	}
	return &obj, nil
}

func (k *Client) ListMCPServers(ctx context.Context, namespace string) ([]musterv1alpha1.MCPServer, error) {
	var list musterv1alpha1.MCPServerList
	err := k.listResources(ctx, namespace, kindMCPServer, &list)
	return list.Items, err
}

func (k *Client) CreateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	return k.createResource(ctx, kindMCPServer, server)
}

func (k *Client) UpdateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	return k.updateResource(ctx, kindMCPServer, server)
}

func (k *Client) DeleteMCPServer(ctx context.Context, name, namespace string) error {
	return k.deleteResource(ctx, name, namespace, kindMCPServer, &musterv1alpha1.MCPServer{})
}

func (k *Client) UpdateMCPServerStatus(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	return k.updateResourceStatus(ctx, kindMCPServer, server)
}
