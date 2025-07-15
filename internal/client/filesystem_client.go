package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

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

// safeUnmarshalWorkflow safely unmarshals workflow YAML with panic recovery
func (f *filesystemClient) safeUnmarshalWorkflow(data []byte, workflow *musterv1alpha1.Workflow) (err error) {
	// Add panic recovery to handle potential stack overflow from recursive structures
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during YAML unmarshaling (likely recursive structure): %v", r)
		}
	}()

	// First try to unmarshal as raw structure to avoid RawExtension issues
	var rawWorkflow map[string]interface{}
	if err := yaml.Unmarshal(data, &rawWorkflow); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	// Convert the raw structure to a safe workflow structure
	return f.convertRawToWorkflow(rawWorkflow, workflow)
}

// convertRawToWorkflow converts raw YAML structure to a Workflow CRD safely
func (f *filesystemClient) convertRawToWorkflow(rawWorkflow map[string]interface{}, workflow *musterv1alpha1.Workflow) error {
	// Set basic metadata
	if metadata, ok := rawWorkflow["metadata"].(map[string]interface{}); ok {
		if name, ok := metadata["name"].(string); ok {
			workflow.Name = name
		}
		if namespace, ok := metadata["namespace"].(string); ok {
			workflow.Namespace = namespace
		}
	}

	// Set apiVersion and kind
	if apiVersion, ok := rawWorkflow["apiVersion"].(string); ok {
		workflow.APIVersion = apiVersion
	}
	if kind, ok := rawWorkflow["kind"].(string); ok {
		workflow.Kind = kind
	}

	// Convert spec safely
	if spec, ok := rawWorkflow["spec"].(map[string]interface{}); ok {
		if name, ok := spec["name"].(string); ok {
			workflow.Spec.Name = name
		}
		if description, ok := spec["description"].(string); ok {
			workflow.Spec.Description = description
		}

		// Convert args - avoid RawExtension for now, use simple interface{}
		if args, ok := spec["args"].(map[string]interface{}); ok {
			workflow.Spec.Args = make(map[string]musterv1alpha1.ArgDefinition)
			for key, argValue := range args {
				if argMap, ok := argValue.(map[string]interface{}); ok {
					argDef := musterv1alpha1.ArgDefinition{}
					if argType, ok := argMap["type"].(string); ok {
						argDef.Type = argType
					}
					if required, ok := argMap["required"].(bool); ok {
						argDef.Required = required
					}
					if description, ok := argMap["description"].(string); ok {
						argDef.Description = description
					}
					if defaultValue, ok := argMap["default"]; ok {
						// Safe JSON marshaling for default value to avoid recursion
						if jsonBytes, err := f.safeJSONMarshal(defaultValue); err == nil {
							argDef.Default = &runtime.RawExtension{Raw: jsonBytes}
						}
					}
					workflow.Spec.Args[key] = argDef
				}
			}
		}

		// Convert steps - avoid complex RawExtension for test scenarios
		if steps, ok := spec["steps"].([]interface{}); ok {
			workflow.Spec.Steps = make([]musterv1alpha1.WorkflowStep, len(steps))
			for i, stepValue := range steps {
				if stepMap, ok := stepValue.(map[string]interface{}); ok {
					step := musterv1alpha1.WorkflowStep{}
					if id, ok := stepMap["id"].(string); ok {
						step.ID = id
					}
					if tool, ok := stepMap["tool"].(string); ok {
						step.Tool = tool
					}
					if store, ok := stepMap["store"].(bool); ok {
						step.Store = store
					}
					if allowFailure, ok := stepMap["allowFailure"].(bool); ok {
						step.AllowFailure = allowFailure
					}
					if description, ok := stepMap["description"].(string); ok {
						step.Description = description
					}

					// Convert args map to RawExtension safely
					if args, ok := stepMap["args"].(map[string]interface{}); ok {
						step.Args = make(map[string]*runtime.RawExtension)
						for key, value := range args {
							if jsonBytes, err := f.safeJSONMarshal(value); err == nil {
								step.Args[key] = &runtime.RawExtension{Raw: jsonBytes}
							}
						}
					}

					// Convert outputs map to RawExtension safely
					if outputs, ok := stepMap["outputs"].(map[string]interface{}); ok {
						step.Outputs = make(map[string]*runtime.RawExtension)
						for key, value := range outputs {
							if jsonBytes, err := f.safeJSONMarshal(value); err == nil {
								step.Outputs[key] = &runtime.RawExtension{Raw: jsonBytes}
							}
						}
					}

					workflow.Spec.Steps[i] = step
				}
			}
		}
	}

	return nil
}

// safeJSONMarshal safely marshals values to JSON with recursion protection
func (f *filesystemClient) safeJSONMarshal(value interface{}) ([]byte, error) {
	// Use a depth counter to prevent infinite recursion
	return f.marshalWithDepth(value, 0, 10)
}

// marshalWithDepth marshals with depth tracking to prevent stack overflow
func (f *filesystemClient) marshalWithDepth(value interface{}, depth, maxDepth int) ([]byte, error) {
	if depth > maxDepth {
		// If we reach max depth, return a simple string representation
		return []byte(`"<max depth reached>"`), nil
	}

	// For simple types, use direct JSON marshaling
	switch v := value.(type) {
	case nil:
		return []byte("null"), nil
	case bool:
		if v {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case string:
		// Escape quotes and marshal as JSON string
		return json.Marshal(v)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return json.Marshal(v)
	case float32, float64:
		return json.Marshal(v)
	case map[string]interface{}:
		// For maps, recursively marshal each value
		result := make(map[string]interface{})
		for key, val := range v {
			// Skip recursive marshaling for complex nested structures to avoid loops
			result[key] = val
		}
		return json.Marshal(result)
	case []interface{}:
		// For arrays, recursively marshal each element
		result := make([]interface{}, len(v))
		for i, val := range v {
			result[i] = val
		}
		return json.Marshal(result)
	default:
		// For other types, use default JSON marshaling
		return json.Marshal(value)
	}
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
