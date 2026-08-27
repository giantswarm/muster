package mcpserver

import (
	"context"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// The caller-identity gate itself (token extraction, audience precheck,
// per-call client construction) is shared across CR-owning adapters and lives
// in internal/callerwrite. This file adapts its resolved client to the
// MCPServer write surface.

// callerWriter adapts a per-call controller-runtime client to the write
// surface the mutation handlers use.
type callerWriter struct {
	client    ctrlclient.Client
	namespace string
}

func (w *callerWriter) CreateMCPServer(ctx context.Context, obj *musterv1alpha1.MCPServer) error {
	return w.client.Create(ctx, obj)
}

func (w *callerWriter) UpdateMCPServer(ctx context.Context, obj *musterv1alpha1.MCPServer) error {
	return w.client.Update(ctx, obj)
}

func (w *callerWriter) DeleteMCPServer(ctx context.Context, name, namespace string) error {
	obj := &musterv1alpha1.MCPServer{}
	obj.Name = name
	obj.Namespace = namespace
	return w.client.Delete(ctx, obj)
}
