package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"muster/internal/api"
	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
	"muster/pkg/logging"
)

// filesystemClient implements MusterClient using local filesystem storage.
//
// This implementation provides compatibility with the existing file-based YAML
// storage while implementing the same interface as the Kubernetes client.
// This enables seamless local development and testing.
//
// Files are organized directly in resource type folders without namespace subdirectories:
// - MCPServers: {basePath}/mcpservers/{name}.yaml
// - ServiceClasses: {basePath}/serviceclasses/{name}.yaml
// - Workflows: {basePath}/workflows/{name}.yaml
type filesystemClient struct {
	basePath string
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

	return &filesystemClient{
		basePath: basePath,
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
func (f *filesystemClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	// Extract namespace from list options
	namespace := "default"
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
func (f *filesystemClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
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
func (f *filesystemClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
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
func (f *filesystemClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
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

// Patch patches a resource (implements client.Client interface).
func (f *filesystemClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	// Filesystem client doesn't support patching - fall back to update
	return f.Update(ctx, obj)
}

// Apply applies a resource (implements client.Client interface).
func (f *filesystemClient) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	// Filesystem client doesn't support ApplyConfiguration - return error
	return fmt.Errorf("filesystem client does not support Apply operations with ApplyConfiguration")
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
	case *musterv1alpha1.Workflow:
		return musterv1alpha1.GroupVersion.WithKind("Workflow"), nil
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
	filePath := f.getMCPServerPath(name)

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
	// Set namespace to default if not set
	if server.Namespace == "" {
		server.Namespace = "default"
	}

	return &server, nil
}

// ListMCPServers lists all MCPServers from filesystem.
func (f *filesystemClient) ListMCPServers(ctx context.Context, namespace string) ([]musterv1alpha1.MCPServer, error) {
	dirPath := f.getMCPServerDir()

	// Check if directory exists, if not return empty list (don't create it)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Directory doesn't exist, return empty list
			return []musterv1alpha1.MCPServer{}, nil
		}
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
			// Log error but continue with other files - this prevents one bad file from breaking everything
			logging.Error("fs-client", err, "Failed to load MCPServer %s", entry.Name())
			continue
		}
		servers = append(servers, *server)
	}

	return servers, nil
}

// CreateMCPServer creates a new MCPServer in filesystem.
func (f *filesystemClient) CreateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	// Check if server already exists
	filePath := f.getMCPServerPath(server.Name)
	if _, err := os.Stat(filePath); err == nil {
		return errors.NewAlreadyExists(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
			server.Name,
		)
	}

	// Create directory if it doesn't exist
	dirPath := f.getMCPServerDir()
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dirPath, err)
	}

	// Set namespace to default if not set
	if server.Namespace == "" {
		server.Namespace = "default"
	}

	// Marshal server to YAML
	data, err := yaml.Marshal(server)
	if err != nil {
		return fmt.Errorf("failed to marshal MCPServer %s: %w", server.Name, err)
	}

	// Write file
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write MCPServer file %s: %w", filePath, err)
	}

	return nil
}

// UpdateMCPServer updates an existing MCPServer in filesystem.
func (f *filesystemClient) UpdateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	// Check if server exists
	filePath := f.getMCPServerPath(server.Name)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
			server.Name,
		)
	}

	// Set namespace to default if not set
	if server.Namespace == "" {
		server.Namespace = "default"
	}

	// Marshal server to YAML
	data, err := yaml.Marshal(server)
	if err != nil {
		return fmt.Errorf("failed to marshal MCPServer %s: %w", server.Name, err)
	}

	// Write file
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write MCPServer file %s: %w", filePath, err)
	}

	return nil
}

// DeleteMCPServer deletes an MCPServer from filesystem.
func (f *filesystemClient) DeleteMCPServer(ctx context.Context, name, namespace string) error {
	filePath := f.getMCPServerPath(name)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
			name,
		)
	}

	// Delete file
	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete MCPServer file %s: %w", filePath, err)
	}

	return nil
}

// Helper methods

func (f *filesystemClient) getMCPServerDir() string {
	return filepath.Join(f.basePath, "mcpservers")
}

func (f *filesystemClient) getMCPServerPath(name string) string {
	return filepath.Join(f.getMCPServerDir(), name+".yaml")
}

// GetServiceClass retrieves a specific ServiceClass from filesystem.
func (f *filesystemClient) GetServiceClass(ctx context.Context, name, namespace string) (*musterv1alpha1.ServiceClass, error) {
	filePath := f.getServiceClassPath(name)

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
	// Set namespace to default if not set
	if serviceClass.Namespace == "" {
		serviceClass.Namespace = "default"
	}

	return &serviceClass, nil
}

// ListServiceClasses lists all ServiceClasses from filesystem.
func (f *filesystemClient) ListServiceClasses(ctx context.Context, namespace string) ([]musterv1alpha1.ServiceClass, error) {
	dirPath := f.getServiceClassDir()

	// Check if directory exists, if not return empty list (don't create it)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Directory doesn't exist, return empty list
			return []musterv1alpha1.ServiceClass{}, nil
		}
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
			// Log error but continue with other files - this prevents one bad file from breaking everything
			logging.Error("fs-client", err, "Failed to load ServiceClass %s", entry.Name())
			continue
		}
		serviceClasses = append(serviceClasses, *serviceClass)
	}

	return serviceClasses, nil
}

// CreateServiceClass creates a new ServiceClass in filesystem.
func (f *filesystemClient) CreateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	// Check if serviceClass already exists
	filePath := f.getServiceClassPath(serviceClass.Name)
	if _, err := os.Stat(filePath); err == nil {
		return errors.NewAlreadyExists(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "serviceclasses"},
			serviceClass.Name,
		)
	}

	// Create directory if it doesn't exist
	dirPath := f.getServiceClassDir()
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dirPath, err)
	}

	// Set namespace to default if not set
	if serviceClass.Namespace == "" {
		serviceClass.Namespace = "default"
	}

	// Marshal serviceClass to YAML
	data, err := yaml.Marshal(serviceClass)
	if err != nil {
		return fmt.Errorf("failed to marshal ServiceClass %s: %w", serviceClass.Name, err)
	}

	// Write file
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write ServiceClass file %s: %w", filePath, err)
	}

	return nil
}

// UpdateServiceClass updates an existing ServiceClass in filesystem.
func (f *filesystemClient) UpdateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	// Check if serviceClass exists
	filePath := f.getServiceClassPath(serviceClass.Name)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "serviceclasses"},
			serviceClass.Name,
		)
	}

	// Set namespace to default if not set
	if serviceClass.Namespace == "" {
		serviceClass.Namespace = "default"
	}

	// Marshal serviceClass to YAML
	data, err := yaml.Marshal(serviceClass)
	if err != nil {
		return fmt.Errorf("failed to marshal ServiceClass %s: %w", serviceClass.Name, err)
	}

	// Write file
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write ServiceClass file %s: %w", filePath, err)
	}

	return nil
}

// DeleteServiceClass deletes a ServiceClass from filesystem.
func (f *filesystemClient) DeleteServiceClass(ctx context.Context, name, namespace string) error {
	filePath := f.getServiceClassPath(name)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "serviceclasses"},
			name,
		)
	}

	// Delete file
	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete ServiceClass file %s: %w", filePath, err)
	}

	return nil
}

func (f *filesystemClient) getServiceClassDir() string {
	return filepath.Join(f.basePath, "serviceclasses")
}

func (f *filesystemClient) getServiceClassPath(name string) string {
	return filepath.Join(f.getServiceClassDir(), name+".yaml")
}

// GetWorkflow retrieves a specific Workflow from filesystem.
func (f *filesystemClient) GetWorkflow(ctx context.Context, name, namespace string) (*musterv1alpha1.Workflow, error) {
	filePath := f.getWorkflowPath(name)

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.NewNotFound(
				schema.GroupResource{Group: "muster.giantswarm.io", Resource: "workflows"},
				name,
			)
		}
		return nil, fmt.Errorf("failed to read Workflow file %s: %w", filePath, err)
	}

	var workflow musterv1alpha1.Workflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Workflow from %s: %w", filePath, err)
	}

	// Ensure metadata is properly set
	if workflow.Name == "" {
		workflow.Name = name
	}
	// Set namespace to default if not set
	if workflow.Namespace == "" {
		workflow.Namespace = "default"
	}

	return &workflow, nil
}

// ListWorkflows lists all Workflows from filesystem.
func (f *filesystemClient) ListWorkflows(ctx context.Context, namespace string) ([]musterv1alpha1.Workflow, error) {
	dirPath := f.getWorkflowDir()

	// Check if directory exists, if not return empty list (don't create it)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Directory doesn't exist, return empty list
			return []musterv1alpha1.Workflow{}, nil
		}
		return nil, fmt.Errorf("failed to read directory %s: %w", dirPath, err)
	}

	var workflows []musterv1alpha1.Workflow
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}

		name := getNameFromFileName(entry.Name())
		workflow, err := f.GetWorkflow(ctx, name, namespace)
		if err != nil {
			// Log error but continue with other files - this prevents one bad file from breaking everything
			logging.Error("fs-client", err, "Failed to load Workflow %s", entry.Name())
			continue
		}
		workflows = append(workflows, *workflow)
	}

	return workflows, nil
}

// CreateWorkflow creates a new Workflow in filesystem.
func (f *filesystemClient) CreateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	// Check if workflow already exists
	filePath := f.getWorkflowPath(workflow.Name)
	if _, err := os.Stat(filePath); err == nil {
		return errors.NewAlreadyExists(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "workflows"},
			workflow.Name,
		)
	}

	// Create directory if it doesn't exist
	dirPath := f.getWorkflowDir()
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dirPath, err)
	}

	// Set namespace to default if not set
	if workflow.Namespace == "" {
		workflow.Namespace = "default"
	}

	// Marshal workflow to YAML
	data, err := yaml.Marshal(workflow)
	if err != nil {
		return fmt.Errorf("failed to marshal Workflow %s: %w", workflow.Name, err)
	}

	// Write file
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write Workflow file %s: %w", filePath, err)
	}

	return nil
}

// UpdateWorkflow updates an existing Workflow in filesystem.
func (f *filesystemClient) UpdateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	// Check if workflow exists
	filePath := f.getWorkflowPath(workflow.Name)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "workflows"},
			workflow.Name,
		)
	}

	// Set namespace to default if not set
	if workflow.Namespace == "" {
		workflow.Namespace = "default"
	}

	// Marshal workflow to YAML
	data, err := yaml.Marshal(workflow)
	if err != nil {
		return fmt.Errorf("failed to marshal Workflow %s: %w", workflow.Name, err)
	}

	// Write file
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write Workflow file %s: %w", filePath, err)
	}

	return nil
}

// DeleteWorkflow deletes a Workflow from filesystem.
func (f *filesystemClient) DeleteWorkflow(ctx context.Context, name, namespace string) error {
	filePath := f.getWorkflowPath(name)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "workflows"},
			name,
		)
	}

	// Delete file
	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete Workflow file %s: %w", filePath, err)
	}

	return nil
}

// getWorkflowDir returns the directory path for workflow files.
func (f *filesystemClient) getWorkflowDir() string {
	return filepath.Join(f.basePath, "workflows")
}

// getWorkflowPath returns the file path for a specific workflow.
func (f *filesystemClient) getWorkflowPath(name string) string {
	return filepath.Join(f.getWorkflowDir(), name+".yaml")
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

// IsKubernetesMode returns false since this is the filesystem implementation.
func (f *filesystemClient) IsKubernetesMode() bool {
	return false
}

// Close performs cleanup for the filesystem client.
func (f *filesystemClient) Close() error {
	// Filesystem client doesn't require cleanup
	return nil
}

// CreateEvent logs an event for the given object in filesystem mode.
func (f *filesystemClient) CreateEvent(ctx context.Context, obj client.Object, reason, message, eventType string) error {
	logging.Info("event", "Event for %s/%s: %s - %s (%s)",
		obj.GetNamespace(), obj.GetName(), reason, message, eventType)

	// Optionally write to events.log file for debugging
	return f.writeEventToFile(obj.GetNamespace(), obj.GetName(), obj.GetObjectKind().GroupVersionKind().Kind, reason, message, eventType)
}

// CreateEventForCRD logs an event for a CRD by type, name, and namespace in filesystem mode.
func (f *filesystemClient) CreateEventForCRD(ctx context.Context, crdType, name, namespace, reason, message, eventType string) error {
	logging.Info("event", "Event for %s %s/%s: %s - %s (%s)",
		crdType, namespace, name, reason, message, eventType)

	// Optionally write to events.log file for debugging
	return f.writeEventToFile(namespace, name, crdType, reason, message, eventType)
}

// QueryEvents retrieves events based on filtering options from filesystem storage.
func (f *filesystemClient) QueryEvents(ctx context.Context, options api.EventQueryOptions) (*api.EventQueryResult, error) {
	eventsDir := filepath.Join(f.basePath, "events")

	// Create events directory if it doesn't exist
	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create events directory: %w", err)
	}

	// Collect events from all daily log files
	var allEvents []api.EventResult

	// Read events from daily JSON files
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read events directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "events-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(eventsDir, entry.Name())
		fileEvents, err := f.readEventsFromFile(filePath)
		if err != nil {
			logging.Debug("fs-client", "Failed to read events from %s: %v", filePath, err)
			continue
		}

		allEvents = append(allEvents, fileEvents...)
	}

	// Also read from legacy events.log if it exists
	legacyEvents, err := f.readLegacyEventsLog(eventsDir)
	if err == nil {
		allEvents = append(allEvents, legacyEvents...)
	}

	// Apply filters
	filteredEvents := f.filterEvents(allEvents, options)

	// Sort by timestamp (newest first)
	sort.Slice(filteredEvents, func(i, j int) bool {
		return filteredEvents[i].Timestamp.After(filteredEvents[j].Timestamp)
	})

	totalCount := len(filteredEvents)

	// Apply limit for initial result
	initialEvents := filteredEvents
	if options.Limit > 0 && len(filteredEvents) > options.Limit {
		initialEvents = filteredEvents[:options.Limit]
	}

	return &api.EventQueryResult{
		Events:     initialEvents,
		TotalCount: totalCount,
	}, nil
}

// readEventsFromFile reads events from a daily JSON file.
func (f *filesystemClient) readEventsFromFile(filePath string) ([]api.EventResult, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return []api.EventResult{}, nil
	}

	var events []api.EventResult
	// Parse JSON lines format - each line is a separate JSON event
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event api.EventResult
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			logging.Debug("fs-client", "Failed to parse event JSON: %v", err)
			continue
		}
		events = append(events, event)
	}

	return events, nil
}

// readLegacyEventsLog reads events from the legacy events.log file.
func (f *filesystemClient) readLegacyEventsLog(eventsDir string) ([]api.EventResult, error) {
	legacyFile := filepath.Join(eventsDir, "events.log")
	data, err := os.ReadFile(legacyFile)
	if err != nil {
		return nil, err
	}

	var events []api.EventResult
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse legacy format: [timestamp] Kind namespace/name: Reason - Message (Type)
		event, err := f.parseLegacyEventLine(line)
		if err != nil {
			continue
		}
		events = append(events, event)
	}

	return events, nil
}

// parseLegacyEventLine parses a line from the legacy events.log format.
func (f *filesystemClient) parseLegacyEventLine(line string) (api.EventResult, error) {
	// Format: [2024-01-15T14:30:00Z] MCPServer default/prometheus: MCPServerStarted - MCPServer prometheus started successfully (Normal)
	if !strings.HasPrefix(line, "[") {
		return api.EventResult{}, fmt.Errorf("invalid format")
	}

	parts := strings.SplitN(line, "] ", 2)
	if len(parts) != 2 {
		return api.EventResult{}, fmt.Errorf("invalid timestamp format")
	}

	timestampStr := parts[0][1:] // Remove leading [
	timestamp, err := time.Parse(time.RFC3339, timestampStr)
	if err != nil {
		return api.EventResult{}, fmt.Errorf("invalid timestamp: %w", err)
	}

	remaining := parts[1]

	// Split by ": " to separate object info from event details
	objectParts := strings.SplitN(remaining, ": ", 2)
	if len(objectParts) != 2 {
		return api.EventResult{}, fmt.Errorf("invalid object format")
	}

	// Parse object info: "Kind namespace/name"
	objectInfo := strings.SplitN(objectParts[0], " ", 2)
	if len(objectInfo) != 2 {
		return api.EventResult{}, fmt.Errorf("invalid object info")
	}

	kind := objectInfo[0]
	namespaceAndName := strings.SplitN(objectInfo[1], "/", 2)
	if len(namespaceAndName) != 2 {
		return api.EventResult{}, fmt.Errorf("invalid namespace/name format")
	}

	namespace := namespaceAndName[0]
	name := namespaceAndName[1]

	// Parse event details: "Reason - Message (Type)"
	eventDetails := objectParts[1]

	// Extract type (last part in parentheses)
	var eventType string
	if strings.HasSuffix(eventDetails, ")") {
		lastParen := strings.LastIndex(eventDetails, "(")
		if lastParen > 0 {
			eventType = eventDetails[lastParen+1 : len(eventDetails)-1]
			eventDetails = strings.TrimSpace(eventDetails[:lastParen])
		}
	}

	// Split reason and message by " - "
	reasonAndMessage := strings.SplitN(eventDetails, " - ", 2)
	if len(reasonAndMessage) != 2 {
		return api.EventResult{}, fmt.Errorf("invalid reason/message format")
	}

	reason := reasonAndMessage[0]
	message := reasonAndMessage[1]

	return api.EventResult{
		Timestamp: timestamp,
		Namespace: namespace,
		InvolvedObject: api.ObjectReference{
			Kind:      kind,
			Name:      name,
			Namespace: namespace,
		},
		Reason:  reason,
		Message: message,
		Type:    eventType,
		Source:  "muster",
	}, nil
}

// filterEvents applies the given options to filter events.
func (f *filesystemClient) filterEvents(events []api.EventResult, options api.EventQueryOptions) []api.EventResult {
	var filtered []api.EventResult

	for _, event := range events {
		// Filter by resource type
		if options.ResourceType != "" && event.InvolvedObject.Kind != options.ResourceType {
			continue
		}

		// Filter by resource name
		if options.ResourceName != "" && event.InvolvedObject.Name != options.ResourceName {
			continue
		}

		// Filter by namespace
		if options.Namespace != "" && event.Namespace != options.Namespace {
			continue
		}

		// Filter by event type
		if options.EventType != "" && event.Type != options.EventType {
			continue
		}

		// Filter by time range
		if options.Since != nil && event.Timestamp.Before(*options.Since) {
			continue
		}

		if options.Until != nil && event.Timestamp.After(*options.Until) {
			continue
		}

		filtered = append(filtered, event)
	}

	return filtered
}

// Enhanced event storage methods

// writeEventToFile writes event information to both legacy and JSON formats.
func (f *filesystemClient) writeEventToFile(namespace, name, kind, reason, message, eventType string) error {
	eventsDir := filepath.Join(f.basePath, "events")
	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		logging.Debug("fs-client", "Failed to create events directory: %v", err)
		return nil
	}

	timestamp := time.Now()

	// Write to legacy format for backward compatibility
	if err := f.writeLegacyEvent(eventsDir, timestamp, namespace, name, kind, reason, message, eventType); err != nil {
		logging.Debug("fs-client", "Failed to write legacy event: %v", err)
	}

	// Write to JSON format
	if err := f.writeJSONEvent(eventsDir, timestamp, namespace, name, kind, reason, message, eventType); err != nil {
		logging.Debug("fs-client", "Failed to write JSON event: %v", err)
	}

	// Cleanup old event files (keep last 30 days)
	f.cleanupOldEventFiles(eventsDir)

	return nil
}

// writeLegacyEvent writes to the legacy events.log format.
func (f *filesystemClient) writeLegacyEvent(eventsDir string, timestamp time.Time, namespace, name, kind, reason, message, eventType string) error {
	eventsFile := filepath.Join(eventsDir, "events.log")
	eventLine := fmt.Sprintf("[%s] %s %s/%s: %s - %s (%s)\n",
		timestamp.Format(time.RFC3339), kind, namespace, name, reason, message, eventType)

	file, err := os.OpenFile(eventsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(eventLine)
	return err
}

// writeJSONEvent writes to daily JSON files.
func (f *filesystemClient) writeJSONEvent(eventsDir string, timestamp time.Time, namespace, name, kind, reason, message, eventType string) error {
	// Create daily file name
	dateStr := timestamp.Format("2006-01-02")
	jsonFile := filepath.Join(eventsDir, fmt.Sprintf("events-%s.json", dateStr))

	event := api.EventResult{
		Timestamp: timestamp,
		Namespace: namespace,
		InvolvedObject: api.ObjectReference{
			Kind:      kind,
			Name:      name,
			Namespace: namespace,
		},
		Reason:  reason,
		Message: message,
		Type:    eventType,
		Source:  "muster",
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return err
	}

	// Append to daily JSON file (one JSON object per line)
	file, err := os.OpenFile(jsonFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(string(eventJSON) + "\n")
	return err
}

// cleanupOldEventFiles removes event files older than 30 days.
func (f *filesystemClient) cleanupOldEventFiles(eventsDir string) {
	cutoffDate := time.Now().AddDate(0, 0, -30)
	cutoffDateStr := cutoffDate.Format("2006-01-02")

	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "events-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Extract date from filename: events-2024-01-15.json
		filename := entry.Name()
		if len(filename) < 21 { // "events-YYYY-MM-DD.json" = 21 chars
			continue
		}

		dateStr := filename[7:17] // Extract "YYYY-MM-DD"
		if dateStr < cutoffDateStr {
			filePath := filepath.Join(eventsDir, filename)
			if err := os.Remove(filePath); err != nil {
				logging.Debug("fs-client", "Failed to remove old event file %s: %v", filePath, err)
			} else {
				logging.Debug("fs-client", "Removed old event file: %s", filename)
			}
		}
	}
}
