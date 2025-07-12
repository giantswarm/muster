package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
)

// filesystemClient implements MusterClient using local filesystem storage.
//
// This implementation provides compatibility with the existing file-based YAML
// storage while implementing the same interface as the Kubernetes client.
// This enables seamless local development and testing.
type filesystemClient struct {
	basePath         string
	defaultNamespace string
}

// NewFilesystemClient creates a new filesystem-based muster client.
//
// This client stores resources as YAML files in the local filesystem,
// providing compatibility with existing file-based configurations.
//
// Args:
//   - cfg: Client configuration (optional)
//
// Returns:
//   - MusterClient: The filesystem-backed client
//   - error: Error if client creation fails
func NewFilesystemClient(cfg *MusterClientConfig) (MusterClient, error) {
	if cfg == nil {
		cfg = &MusterClientConfig{}
	}

	basePath := cfg.FilesystemPath
	if basePath == "" {
		basePath = "."
	}

	namespace := cfg.Namespace
	if namespace == "" {
		namespace = "default"
	}

	return &filesystemClient{
		basePath:         basePath,
		defaultNamespace: namespace,
	}, nil
}

// Get retrieves a resource by name and namespace (implements client.Client interface).
func (f *filesystemClient) Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
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
	default:
		return fmt.Errorf("filesystem client does not support type %T", obj)
	}
}

// List retrieves a list of resources (implements client.Client interface).
func (f *filesystemClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	switch v := list.(type) {
	case *musterv1alpha1.MCPServerList:
		// Extract namespace from list options
		namespace := f.defaultNamespace
		for _, opt := range opts {
			if nsOpt, ok := opt.(*client.ListOptions); ok && nsOpt.Namespace != "" {
				namespace = nsOpt.Namespace
			}
		}

		servers, err := f.ListMCPServers(ctx, namespace)
		if err != nil {
			return err
		}
		v.Items = servers
		return nil
	case *musterv1alpha1.ServiceClassList:
		// Extract namespace from list options
		namespace := f.defaultNamespace
		for _, opt := range opts {
			if nsOpt, ok := opt.(*client.ListOptions); ok && nsOpt.Namespace != "" {
				namespace = nsOpt.Namespace
			}
		}

		serviceClasses, err := f.ListServiceClasses(ctx, namespace)
		if err != nil {
			return err
		}
		v.Items = serviceClasses
		return nil
	default:
		return fmt.Errorf("filesystem client does not support type %T", list)
	}
}

// Create creates a new resource (implements client.Client interface).
func (f *filesystemClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	switch v := obj.(type) {
	case *musterv1alpha1.MCPServer:
		return f.CreateMCPServer(ctx, v)
	case *musterv1alpha1.ServiceClass:
		return f.CreateServiceClass(ctx, v)
	default:
		return fmt.Errorf("filesystem client does not support type %T", obj)
	}
}

// Update updates an existing resource (implements client.Client interface).
func (f *filesystemClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	switch v := obj.(type) {
	case *musterv1alpha1.MCPServer:
		return f.UpdateMCPServer(ctx, v)
	case *musterv1alpha1.ServiceClass:
		return f.UpdateServiceClass(ctx, v)
	default:
		return fmt.Errorf("filesystem client does not support type %T", obj)
	}
}

// Delete deletes a resource (implements client.Client interface).
func (f *filesystemClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	switch v := obj.(type) {
	case *musterv1alpha1.MCPServer:
		return f.DeleteMCPServer(ctx, v.Name, v.Namespace)
	case *musterv1alpha1.ServiceClass:
		return f.DeleteServiceClass(ctx, v.Name, v.Namespace)
	default:
		return fmt.Errorf("filesystem client does not support type %T", obj)
	}
}

// Patch patches a resource (implements client.Client interface).
func (f *filesystemClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	// Filesystem client doesn't support patching - fall back to update
	return f.Update(ctx, obj)
}

// DeleteAllOf deletes all resources matching the given options (implements client.Client interface).
func (f *filesystemClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return fmt.Errorf("filesystem client does not support DeleteAllOf operations")
}

// Status returns a status writer (implements client.Client interface).
func (f *filesystemClient) Status() client.StatusWriter {
	return &filesystemStatusWriter{client: f}
}

// SubResource returns a sub-resource client (implements client.Client interface).
func (f *filesystemClient) SubResource(subResource string) client.SubResourceClient {
	return &filesystemSubResourceClient{client: f, subResource: subResource}
}

// Scheme returns the scheme (implements client.Client interface).
func (f *filesystemClient) Scheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	musterv1alpha1.AddToScheme(scheme)
	return scheme
}

// RESTMapper returns a REST mapper (implements client.Client interface).
func (f *filesystemClient) RESTMapper() meta.RESTMapper {
	// Return a basic REST mapper for filesystem mode
	return nil
}

// GroupVersionKindFor returns the GroupVersionKind for an object (implements client.Client interface).
func (f *filesystemClient) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	switch obj.(type) {
	case *musterv1alpha1.MCPServer:
		return musterv1alpha1.GroupVersion.WithKind("MCPServer"), nil
	case *musterv1alpha1.ServiceClass:
		return musterv1alpha1.GroupVersion.WithKind("ServiceClass"), nil
	default:
		return schema.GroupVersionKind{}, fmt.Errorf("unknown object type %T", obj)
	}
}

// IsObjectNamespaced returns whether the object is namespaced (implements client.Client interface).
func (f *filesystemClient) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	// All muster resources are namespaced
	return true, nil
}

// GetMCPServer retrieves a specific MCPServer from filesystem.
func (f *filesystemClient) GetMCPServer(ctx context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error) {
	if namespace == "" {
		namespace = f.defaultNamespace
	}

	filePath := f.getMCPServerPath(name, namespace)

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.NewNotFound(
				schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
				name,
			)
		}
		return nil, fmt.Errorf("failed to read MCPServer file %s: %w", filePath, err)
	}

	var server musterv1alpha1.MCPServer
	if err := yaml.Unmarshal(data, &server); err != nil {
		return nil, fmt.Errorf("failed to unmarshal MCPServer from %s: %w", filePath, err)
	}

	// Ensure metadata is properly set
	if server.Name == "" {
		server.Name = name
	}
	if server.Namespace == "" {
		server.Namespace = namespace
	}

	return &server, nil
}

// ListMCPServers lists all MCPServers in a namespace from filesystem.
func (f *filesystemClient) ListMCPServers(ctx context.Context, namespace string) ([]musterv1alpha1.MCPServer, error) {
	if namespace == "" {
		namespace = f.defaultNamespace
	}

	dirPath := f.getMCPServerDir(namespace)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", dirPath, err)
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dirPath, err)
	}

	var servers []musterv1alpha1.MCPServer
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}

		name := getNameFromFileName(entry.Name())
		server, err := f.GetMCPServer(ctx, name, namespace)
		if err != nil {
			// Log error but continue with other files
			fmt.Printf("Warning: Failed to load MCPServer %s: %v\n", entry.Name(), err)
			continue
		}
		servers = append(servers, *server)
	}

	return servers, nil
}

// CreateMCPServer creates a new MCPServer in filesystem.
func (f *filesystemClient) CreateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	if server.Namespace == "" {
		server.Namespace = f.defaultNamespace
	}

	filePath := f.getMCPServerPath(server.Name, server.Namespace)

	// Check if file already exists
	if _, err := os.Stat(filePath); err == nil {
		return errors.NewAlreadyExists(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
			server.Name,
		)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Set proper metadata
	if server.APIVersion == "" {
		server.APIVersion = "muster.giantswarm.io/v1alpha1"
	}
	if server.Kind == "" {
		server.Kind = "MCPServer"
	}

	data, err := yaml.Marshal(server)
	if err != nil {
		return fmt.Errorf("failed to marshal MCPServer: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write MCPServer file %s: %w", filePath, err)
	}

	return nil
}

// UpdateMCPServer updates an existing MCPServer in filesystem.
func (f *filesystemClient) UpdateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	if server.Namespace == "" {
		server.Namespace = f.defaultNamespace
	}

	filePath := f.getMCPServerPath(server.Name, server.Namespace)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
			server.Name,
		)
	}

	// Set proper metadata
	if server.APIVersion == "" {
		server.APIVersion = "muster.giantswarm.io/v1alpha1"
	}
	if server.Kind == "" {
		server.Kind = "MCPServer"
	}

	data, err := yaml.Marshal(server)
	if err != nil {
		return fmt.Errorf("failed to marshal MCPServer: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write MCPServer file %s: %w", filePath, err)
	}

	return nil
}

// DeleteMCPServer deletes an MCPServer from filesystem.
func (f *filesystemClient) DeleteMCPServer(ctx context.Context, name, namespace string) error {
	if namespace == "" {
		namespace = f.defaultNamespace
	}

	filePath := f.getMCPServerPath(name, namespace)

	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return errors.NewNotFound(
				schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
				name,
			)
		}
		return fmt.Errorf("failed to delete MCPServer file %s: %w", filePath, err)
	}

	return nil
}

// IsKubernetesMode returns false since this is the filesystem implementation.
func (f *filesystemClient) IsKubernetesMode() bool {
	return false
}

// Close performs cleanup for the filesystem client.
func (f *filesystemClient) Close() error {
	// Filesystem client doesn't require cleanup
	return nil
}

// Helper methods

func (f *filesystemClient) getMCPServerDir(namespace string) string {
	return filepath.Join(f.basePath, "mcpservers", namespace)
}

func (f *filesystemClient) getMCPServerPath(name, namespace string) string {
	return filepath.Join(f.getMCPServerDir(namespace), name+".yaml")
}

// GetServiceClass retrieves a specific ServiceClass from filesystem.
func (f *filesystemClient) GetServiceClass(ctx context.Context, name, namespace string) (*musterv1alpha1.ServiceClass, error) {
	if namespace == "" {
		namespace = f.defaultNamespace
	}

	filePath := f.getServiceClassPath(name, namespace)

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.NewNotFound(
				schema.GroupResource{Group: "muster.giantswarm.io", Resource: "serviceclasses"},
				name,
			)
		}
		return nil, fmt.Errorf("failed to read ServiceClass file %s: %w", filePath, err)
	}

	var serviceClass musterv1alpha1.ServiceClass
	if err := yaml.Unmarshal(data, &serviceClass); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ServiceClass from %s: %w", filePath, err)
	}

	// Ensure metadata is properly set
	if serviceClass.Name == "" {
		serviceClass.Name = name
	}
	if serviceClass.Namespace == "" {
		serviceClass.Namespace = namespace
	}

	return &serviceClass, nil
}

// ListServiceClasses lists all ServiceClasses in a namespace from filesystem.
func (f *filesystemClient) ListServiceClasses(ctx context.Context, namespace string) ([]musterv1alpha1.ServiceClass, error) {
	if namespace == "" {
		namespace = f.defaultNamespace
	}

	dirPath := f.getServiceClassDir(namespace)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", dirPath, err)
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dirPath, err)
	}

	var serviceClasses []musterv1alpha1.ServiceClass
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}

		name := getNameFromFileName(entry.Name())
		serviceClass, err := f.GetServiceClass(ctx, name, namespace)
		if err != nil {
			// Log error but continue with other files
			fmt.Printf("Warning: Failed to load ServiceClass %s: %v\n", entry.Name(), err)
			continue
		}
		serviceClasses = append(serviceClasses, *serviceClass)
	}

	return serviceClasses, nil
}

// CreateServiceClass creates a new ServiceClass in filesystem.
func (f *filesystemClient) CreateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	if serviceClass.Namespace == "" {
		serviceClass.Namespace = f.defaultNamespace
	}

	filePath := f.getServiceClassPath(serviceClass.Name, serviceClass.Namespace)

	// Check if file already exists
	if _, err := os.Stat(filePath); err == nil {
		return errors.NewAlreadyExists(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "serviceclasses"},
			serviceClass.Name,
		)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Set proper metadata
	if serviceClass.APIVersion == "" {
		serviceClass.APIVersion = "muster.giantswarm.io/v1alpha1"
	}
	if serviceClass.Kind == "" {
		serviceClass.Kind = "ServiceClass"
	}

	data, err := yaml.Marshal(serviceClass)
	if err != nil {
		return fmt.Errorf("failed to marshal ServiceClass: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write ServiceClass file %s: %w", filePath, err)
	}

	return nil
}

// UpdateServiceClass updates an existing ServiceClass in filesystem.
func (f *filesystemClient) UpdateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	if serviceClass.Namespace == "" {
		serviceClass.Namespace = f.defaultNamespace
	}

	filePath := f.getServiceClassPath(serviceClass.Name, serviceClass.Namespace)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "serviceclasses"},
			serviceClass.Name,
		)
	}

	// Set proper metadata
	if serviceClass.APIVersion == "" {
		serviceClass.APIVersion = "muster.giantswarm.io/v1alpha1"
	}
	if serviceClass.Kind == "" {
		serviceClass.Kind = "ServiceClass"
	}

	data, err := yaml.Marshal(serviceClass)
	if err != nil {
		return fmt.Errorf("failed to marshal ServiceClass: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write ServiceClass file %s: %w", filePath, err)
	}

	return nil
}

// DeleteServiceClass deletes a ServiceClass from filesystem.
func (f *filesystemClient) DeleteServiceClass(ctx context.Context, name, namespace string) error {
	if namespace == "" {
		namespace = f.defaultNamespace
	}

	filePath := f.getServiceClassPath(name, namespace)

	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return errors.NewNotFound(
				schema.GroupResource{Group: "muster.giantswarm.io", Resource: "serviceclasses"},
				name,
			)
		}
		return fmt.Errorf("failed to delete ServiceClass file %s: %w", filePath, err)
	}

	return nil
}

func (f *filesystemClient) getServiceClassDir(namespace string) string {
	return filepath.Join(f.basePath, "serviceclasses", namespace)
}

func (f *filesystemClient) getServiceClassPath(name, namespace string) string {
	return filepath.Join(f.getServiceClassDir(namespace), name+".yaml")
}

func isYAMLFile(filename string) bool {
	ext := filepath.Ext(filename)
	return ext == ".yaml" || ext == ".yml"
}

func getNameFromFileName(filename string) string {
	ext := filepath.Ext(filename)
	return filename[:len(filename)-len(ext)]
}

// filesystemStatusWriter implements client.StatusWriter for filesystem client.
type filesystemStatusWriter struct {
	client *filesystemClient
}

func (w *filesystemStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	// For filesystem client, status create is the same as regular create
	return w.client.Create(ctx, obj)
}

func (w *filesystemStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	// For filesystem client, status updates are the same as regular updates
	return w.client.Update(ctx, obj)
}

func (w *filesystemStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	// For filesystem client, status patches are the same as regular patches
	return w.client.Patch(ctx, obj, patch)
}

// filesystemSubResourceClient implements client.SubResourceClient for filesystem client.
type filesystemSubResourceClient struct {
	client      *filesystemClient
	subResource string
}

func (s *filesystemSubResourceClient) Get(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceGetOption) error {
	// For filesystem client, sub-resource gets are the same as regular gets
	return s.client.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, obj)
}

func (s *filesystemSubResourceClient) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	// For filesystem client, sub-resource creates are the same as regular creates
	return s.client.Create(ctx, obj)
}

func (s *filesystemSubResourceClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	// For filesystem client, sub-resource updates are the same as regular updates
	return s.client.Update(ctx, obj)
}

func (s *filesystemSubResourceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	// For filesystem client, sub-resource patches are the same as regular patches
	return s.client.Patch(ctx, obj, patch)
}
