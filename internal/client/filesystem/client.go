package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// defaultNamespace is the namespace assumed for filesystem-backed resources
// that don't carry an explicit namespace. Filesystem mode has no namespace
// concept; this anchors the in-memory representation.
const defaultNamespace = "default"

// Client is a filesystem-backed implementation of the muster client interface.
//
// Files live directly under per-resource-type folders (no namespace
// subdirectories):
//   - MCPServers:     {basePath}/mcpservers/{name}.yaml
//   - ServiceClasses: {basePath}/serviceclasses/{name}.yaml
//   - Workflows:      {basePath}/workflows/{name}.yaml
//
// Per-domain CRUD methods live in sibling files (mcpserver.go, serviceclass.go,
// workflow.go, events.go). This file keeps the type, the controller-runtime
// Client interface methods, shared helpers, and the sub-resource writer types.
type Client struct {
	basePath string
}

// New returns a filesystem-backed Client rooted at basePath. An empty
// basePath defaults to the current working directory.
func New(basePath string) *Client {
	if basePath == "" {
		basePath = "."
	}
	return &Client{basePath: basePath}
}

var (
	_ client.Client            = (*Client)(nil)
	_ client.StatusWriter      = (*statusWriter)(nil)
	_ client.SubResourceClient = (*subResourceClient)(nil)
)

// Get retrieves a resource by name and namespace (implements client.Client interface).
func (f *Client) Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
	switch v := obj.(type) {
	case *musterv1alpha1.MCPServer:
		server, err := f.GetMCPServer(ctx, key.Name, key.Namespace)
		if err != nil {
			return err
		}
		*v = *server
		return nil
	case *musterv1alpha1.ServiceClass:
		serviceClass, err := f.GetServiceClass(ctx, key.Name, key.Namespace)
		if err != nil {
			return err
		}
		*v = *serviceClass
		return nil
	case *musterv1alpha1.Workflow:
		workflow, err := f.GetWorkflow(ctx, key.Name, key.Namespace)
		if err != nil {
			return err
		}
		*v = *workflow
		return nil
	default:
		return fmt.Errorf("filesystem client does not support type %T", obj)
	}
}

// List retrieves a list of resources (implements client.Client interface).
func (f *Client) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	namespace := defaultNamespace
	for _, opt := range opts {
		if nsOpt, ok := opt.(*client.ListOptions); ok && nsOpt.Namespace != "" {
			namespace = nsOpt.Namespace
		}
	}

	switch v := list.(type) {
	case *musterv1alpha1.MCPServerList:
		servers, err := f.ListMCPServers(ctx, namespace)
		if err != nil {
			return err
		}
		v.Items = servers
		return nil
	case *musterv1alpha1.ServiceClassList:
		serviceClasses, err := f.ListServiceClasses(ctx, namespace)
		if err != nil {
			return err
		}
		v.Items = serviceClasses
		return nil
	case *musterv1alpha1.WorkflowList:
		workflows, err := f.ListWorkflows(ctx, namespace)
		if err != nil {
			return err
		}
		v.Items = workflows
		return nil
	default:
		return fmt.Errorf("filesystem client does not support type %T", list)
	}
}

// Create creates a new resource (implements client.Client interface).
func (f *Client) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	switch v := obj.(type) {
	case *musterv1alpha1.MCPServer:
		return f.CreateMCPServer(ctx, v)
	case *musterv1alpha1.ServiceClass:
		return f.CreateServiceClass(ctx, v)
	case *musterv1alpha1.Workflow:
		return f.CreateWorkflow(ctx, v)
	default:
		return fmt.Errorf("filesystem client does not support type %T", obj)
	}
}

// Update updates an existing resource (implements client.Client interface).
func (f *Client) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	switch v := obj.(type) {
	case *musterv1alpha1.MCPServer:
		return f.UpdateMCPServer(ctx, v)
	case *musterv1alpha1.ServiceClass:
		return f.UpdateServiceClass(ctx, v)
	case *musterv1alpha1.Workflow:
		return f.UpdateWorkflow(ctx, v)
	default:
		return fmt.Errorf("filesystem client does not support type %T", obj)
	}
}

// Delete deletes a resource (implements client.Client interface).
func (f *Client) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	switch v := obj.(type) {
	case *musterv1alpha1.MCPServer:
		return f.DeleteMCPServer(ctx, v.Name, v.Namespace)
	case *musterv1alpha1.ServiceClass:
		return f.DeleteServiceClass(ctx, v.Name, v.Namespace)
	case *musterv1alpha1.Workflow:
		return f.DeleteWorkflow(ctx, v.Name, v.Namespace)
	default:
		return fmt.Errorf("filesystem client does not support type %T", obj)
	}
}

// Patch falls back to Update — filesystem can't compute a partial patch.
func (f *Client) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return f.Update(ctx, obj)
}

// Apply is unsupported (filesystem has no server-side apply).
func (f *Client) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	return fmt.Errorf("filesystem client does not support Apply operations with ApplyConfiguration")
}

// DeleteAllOf is unsupported.
func (f *Client) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return fmt.Errorf("filesystem client does not support DeleteAllOf operations")
}

// Status returns a status writer (implements client.Client interface).
func (f *Client) Status() client.StatusWriter {
	return &statusWriter{client: f}
}

// SubResource returns a sub-resource client (implements client.Client interface).
func (f *Client) SubResource(subResource string) client.SubResourceClient {
	return &subResourceClient{client: f}
}

// Scheme returns the scheme (implements client.Client interface).
func (f *Client) Scheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = musterv1alpha1.AddToScheme(scheme)
	return scheme
}

// RESTMapper returns nil — filesystem mode doesn't need REST mapping.
func (f *Client) RESTMapper() meta.RESTMapper {
	return nil
}

// GroupVersionKindFor returns the GroupVersionKind for an object.
func (f *Client) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	switch obj.(type) {
	case *musterv1alpha1.MCPServer:
		return musterv1alpha1.GroupVersion.WithKind("MCPServer"), nil
	case *musterv1alpha1.ServiceClass:
		return musterv1alpha1.GroupVersion.WithKind("ServiceClass"), nil
	case *musterv1alpha1.Workflow:
		return musterv1alpha1.GroupVersion.WithKind("Workflow"), nil
	default:
		return schema.GroupVersionKind{}, fmt.Errorf("unknown object type %T", obj)
	}
}

// IsObjectNamespaced — all muster resources are namespaced.
func (f *Client) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	return true, nil
}

// IsKubernetesMode returns false since this is the filesystem implementation.
func (f *Client) IsKubernetesMode() bool {
	return false
}

// Close performs cleanup for the filesystem client.
func (f *Client) Close() error {
	return nil
}

// isYAMLFile returns true for filenames with a YAML extension.
func isYAMLFile(filename string) bool {
	ext := filepath.Ext(filename)
	return ext == ".yaml" || ext == ".yml"
}

// getNameFromFileName strips the extension from a YAML filename.
func getNameFromFileName(filename string) string {
	ext := filepath.Ext(filename)
	return filename[:len(filename)-len(ext)]
}

// statusWriter implements client.StatusWriter for filesystem client.
// In filesystem mode the YAML file holds both spec and status, so writes go
// through the regular CRUD methods.
type statusWriter struct {
	client *Client
}

func (w *statusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return w.client.Create(ctx, obj)
}

func (w *statusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return w.client.Update(ctx, obj)
}

func (w *statusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return w.client.Patch(ctx, obj, patch)
}

func (w *statusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return fmt.Errorf("filesystem client does not support Apply operations with ApplyConfiguration")
}

// subResourceClient implements client.SubResourceClient.
// Filesystem mode treats sub-resource ops the same as the parent ops.
type subResourceClient struct {
	client *Client
}

func (s *subResourceClient) Get(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceGetOption) error {
	return s.client.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, obj)
}

func (s *subResourceClient) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return s.client.Create(ctx, obj)
}

func (s *subResourceClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return s.client.Update(ctx, obj)
}

func (s *subResourceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return s.client.Patch(ctx, obj, patch)
}

func (s *subResourceClient) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return fmt.Errorf("filesystem client does not support Apply operations with ApplyConfiguration")
}

// atomicWriteFile writes data atomically by writing to a temp file in the
// same directory and renaming. POSIX-atomic rename ensures readers and
// filesystem watchers never see a partially written file.
func atomicWriteFile(filePath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(filePath)
	tempFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	success := false
	defer func() {
		if !success {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Chmod(tempPath, perm); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	if err := os.Rename(tempPath, filePath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	success = true
	return nil
}
