package filesystem

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

func (f *Client) GetMCPServer(_ context.Context, name, _ string) (*musterv1alpha1.MCPServer, error) {
	var obj musterv1alpha1.MCPServer
	if err := f.getResource(name, &obj, mcpServerMeta); err != nil {
		return nil, err
	}
	return &obj, nil
}

func (f *Client) ListMCPServers(_ context.Context, _ string) ([]musterv1alpha1.MCPServer, error) {
	var list musterv1alpha1.MCPServerList
	err := f.listResources(&list, func() client.Object { return &musterv1alpha1.MCPServer{} }, mcpServerMeta)
	return list.Items, err
}

func (f *Client) CreateMCPServer(_ context.Context, server *musterv1alpha1.MCPServer) error {
	return f.createResource(server, mcpServerMeta)
}

func (f *Client) UpdateMCPServer(_ context.Context, server *musterv1alpha1.MCPServer) error {
	return f.updateResource(server, mcpServerMeta)
}

func (f *Client) DeleteMCPServer(_ context.Context, name, _ string) error {
	return f.deleteResource(name, mcpServerMeta)
}

// UpdateMCPServerStatus records the server's status in its status file. The
// definition file is left as it is (updateResourceStatus).
func (f *Client) UpdateMCPServerStatus(_ context.Context, server *musterv1alpha1.MCPServer) error {
	return f.updateResourceStatus(server, mcpServerMeta)
}
