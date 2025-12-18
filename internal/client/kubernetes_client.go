package client

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"muster/internal/api"
	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
)

// kubernetesClient implements MusterClient using the Kubernetes API and controller-runtime.
//
// This implementation provides native Kubernetes integration with proper scheme registration,
// caching, and event-driven updates through informers and watches.
type kubernetesClient struct {
	client.Client
	scheme *runtime.Scheme
}

// NewKubernetesClient creates a new Kubernetes-based muster client.
//
// This client uses controller-runtime for efficient Kubernetes API access with
// proper caching, informers, and watch functionality.
//
// Args:
//   - config: Kubernetes REST configuration
//
// Returns:
//   - MusterClient: The Kubernetes-backed client
//   - error: Error if client creation fails or CRDs are not available
func NewKubernetesClient(config *rest.Config) (MusterClient, error) {
	// Create scheme with standard Kubernetes types and muster CRDs
	scheme := runtime.NewScheme()

	// Add standard Kubernetes types
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// Add muster CRD types
	utilruntime.Must(musterv1alpha1.AddToScheme(scheme))

	// Create controller-runtime client with the scheme
	k8sClient, err := client.New(config, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Validate that required CRDs are available
	musterClient := &kubernetesClient{
		Client: k8sClient,
		scheme: scheme,
	}

	if err := musterClient.validateCRDs(context.Background()); err != nil {
		return nil, fmt.Errorf("CRD validation failed: %w", err)
	}

	return musterClient, nil
}

// GetWorkflow retrieves a specific Workflow from Kubernetes.
func (k *kubernetesClient) GetWorkflow(ctx context.Context, name, namespace string) (*musterv1alpha1.Workflow, error) {
	workflow := &musterv1alpha1.Workflow{}
	key := client.ObjectKey{Name: name, Namespace: namespace}

	if err := k.Get(ctx, key, workflow); err != nil {
		return nil, err
	}

	return workflow, nil
}

// ListWorkflows lists all Workflows in a namespace from Kubernetes.
func (k *kubernetesClient) ListWorkflows(ctx context.Context, namespace string) ([]musterv1alpha1.Workflow, error) {
	workflowList := &musterv1alpha1.WorkflowList{}
	listOptions := &client.ListOptions{
		Namespace: namespace,
	}

	if err := k.List(ctx, workflowList, listOptions); err != nil {
		return nil, err
	}

	return workflowList.Items, nil
}

// CreateWorkflow creates a new Workflow in Kubernetes.
func (k *kubernetesClient) CreateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	return k.Create(ctx, workflow)
}

// UpdateWorkflow updates an existing Workflow in Kubernetes.
func (k *kubernetesClient) UpdateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	return k.Update(ctx, workflow)
}

// DeleteWorkflow deletes a Workflow from Kubernetes.
func (k *kubernetesClient) DeleteWorkflow(ctx context.Context, name, namespace string) error {
	workflow := &musterv1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	return k.Delete(ctx, workflow)
}

// GetMCPServer retrieves a specific MCPServer resource.
func (k *kubernetesClient) GetMCPServer(ctx context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error) {
	server := &musterv1alpha1.MCPServer{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	err := k.Client.Get(ctx, key, server)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCPServer %s/%s: %w", namespace, name, err)
	}

	return server, nil
}

// ListMCPServers lists all MCPServer resources in a namespace.
func (k *kubernetesClient) ListMCPServers(ctx context.Context, namespace string) ([]musterv1alpha1.MCPServer, error) {
	serverList := &musterv1alpha1.MCPServerList{}
	listOpts := []client.ListOption{}

	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	err := k.Client.List(ctx, serverList, listOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to list MCPServers in namespace %s: %w", namespace, err)
	}

	return serverList.Items, nil
}

// CreateMCPServer creates a new MCPServer resource.
func (k *kubernetesClient) CreateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	err := k.Client.Create(ctx, server)
	if err != nil {
		return fmt.Errorf("failed to create MCPServer %s/%s: %w", server.Namespace, server.Name, err)
	}

	return nil
}

// UpdateMCPServer updates an existing MCPServer resource.
func (k *kubernetesClient) UpdateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	err := k.Client.Update(ctx, server)
	if err != nil {
		return fmt.Errorf("failed to update MCPServer %s/%s: %w", server.Namespace, server.Name, err)
	}

	return nil
}

// DeleteMCPServer deletes an MCPServer resource.
func (k *kubernetesClient) DeleteMCPServer(ctx context.Context, name, namespace string) error {
	server := &musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	err := k.Client.Delete(ctx, server)
	if err != nil {
		return fmt.Errorf("failed to delete MCPServer %s/%s: %w", namespace, name, err)
	}

	return nil
}

// GetServiceClass retrieves a specific ServiceClass resource.
func (k *kubernetesClient) GetServiceClass(ctx context.Context, name, namespace string) (*musterv1alpha1.ServiceClass, error) {
	serviceClass := &musterv1alpha1.ServiceClass{}
	key := client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}

	if err := k.Client.Get(ctx, key, serviceClass); err != nil {
		return nil, fmt.Errorf("failed to get ServiceClass %s/%s: %w", namespace, name, err)
	}

	return serviceClass, nil
}

// ListServiceClasses lists all ServiceClass resources in a namespace.
func (k *kubernetesClient) ListServiceClasses(ctx context.Context, namespace string) ([]musterv1alpha1.ServiceClass, error) {
	serviceClassList := &musterv1alpha1.ServiceClassList{}
	opts := []client.ListOption{
		client.InNamespace(namespace),
	}

	if err := k.Client.List(ctx, serviceClassList, opts...); err != nil {
		return nil, fmt.Errorf("failed to list ServiceClasses in namespace %s: %w", namespace, err)
	}

	return serviceClassList.Items, nil
}

// CreateServiceClass creates a new ServiceClass resource.
func (k *kubernetesClient) CreateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	if err := k.Client.Create(ctx, serviceClass); err != nil {
		return fmt.Errorf("failed to create ServiceClass %s/%s: %w", serviceClass.Namespace, serviceClass.Name, err)
	}

	return nil
}

// UpdateServiceClass updates an existing ServiceClass resource.
func (k *kubernetesClient) UpdateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	if err := k.Client.Update(ctx, serviceClass); err != nil {
		return fmt.Errorf("failed to update ServiceClass %s/%s: %w", serviceClass.Namespace, serviceClass.Name, err)
	}

	return nil
}

// DeleteServiceClass deletes a ServiceClass resource.
func (k *kubernetesClient) DeleteServiceClass(ctx context.Context, name, namespace string) error {
	serviceClass := &musterv1alpha1.ServiceClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	if err := k.Client.Delete(ctx, serviceClass); err != nil {
		return fmt.Errorf("failed to delete ServiceClass %s/%s: %w", namespace, name, err)
	}

	return nil
}

// IsKubernetesMode returns true since this is the Kubernetes implementation.
func (k *kubernetesClient) IsKubernetesMode() bool {
	return true
}

// Close performs cleanup for the Kubernetes client.
//
// Currently, controller-runtime clients don't require explicit cleanup,
// but this method is provided for interface compatibility and future extensibility.
func (k *kubernetesClient) Close() error {
	// Controller-runtime clients don't require explicit cleanup
	// This method is provided for interface compatibility
	return nil
}

// Scheme returns the runtime scheme used by this client.
//
// This can be useful for advanced operations or integration with other
// controller-runtime components.
func (k *kubernetesClient) Scheme() *runtime.Scheme {
	return k.scheme
}

// validateCRDs checks if the required muster CRDs are available in the cluster.
//
// This method performs a test API call to verify that the MCPServer CRD is installed
// and available. If the CRDs are not available, it returns an error, which will
// trigger fallback to filesystem mode.
func (k *kubernetesClient) validateCRDs(ctx context.Context) error {
	// Try to list MCPServers in the default namespace
	// This will fail if the CRD is not installed
	_, err := k.ListMCPServers(ctx, "default")
	if err != nil {
		return fmt.Errorf("MCPServer CRD not available: %w", err)
	}

	return nil
}

// CreateEvent creates a Kubernetes Event for the given object.
func (k *kubernetesClient) CreateEvent(ctx context.Context, obj client.Object, reason, message, eventType string) error {
	gvk, err := k.GroupVersionKindFor(obj)
	if err != nil {
		return fmt.Errorf("failed to get GroupVersionKind for object: %w", err)
	}

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: obj.GetName() + "-",
			Namespace:    obj.GetNamespace(),
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: gvk.GroupVersion().String(),
			Kind:       gvk.Kind,
			Name:       obj.GetName(),
			Namespace:  obj.GetNamespace(),
			UID:        obj.GetUID(),
		},
		Reason:         reason,
		Message:        message,
		Type:           eventType,
		Source:         corev1.EventSource{Component: "muster"},
		FirstTimestamp: metav1.NewTime(time.Now()),
		LastTimestamp:  metav1.NewTime(time.Now()),
		Count:          1,
	}

	if err := k.Create(ctx, event); err != nil {
		return fmt.Errorf("failed to create Kubernetes Event: %w", err)
	}

	return nil
}

// CreateEventForCRD creates a Kubernetes Event for a CRD by type, name, and namespace.
func (k *kubernetesClient) CreateEventForCRD(ctx context.Context, crdType, name, namespace, reason, message, eventType string) error {
	// Determine the GroupVersionKind based on the CRD type
	var gvk schema.GroupVersionKind
	switch crdType {
	case "MCPServer":
		gvk = musterv1alpha1.GroupVersion.WithKind("MCPServer")
	case "ServiceClass":
		gvk = musterv1alpha1.GroupVersion.WithKind("ServiceClass")
	case "Workflow":
		gvk = musterv1alpha1.GroupVersion.WithKind("Workflow")
	default:
		return fmt.Errorf("unsupported CRD type: %s", crdType)
	}

	// Try to get the actual object to retrieve its UID
	var uid types.UID
	switch crdType {
	case "MCPServer":
		obj, err := k.GetMCPServer(ctx, name, namespace)
		if err == nil {
			uid = obj.GetUID()
		}
	case "ServiceClass":
		obj, err := k.GetServiceClass(ctx, name, namespace)
		if err == nil {
			uid = obj.GetUID()
		}
	case "Workflow":
		obj, err := k.GetWorkflow(ctx, name, namespace)
		if err == nil {
			uid = obj.GetUID()
		}
	}

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: name + "-",
			Namespace:    namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: gvk.GroupVersion().String(),
			Kind:       gvk.Kind,
			Name:       name,
			Namespace:  namespace,
			UID:        uid,
		},
		Reason:         reason,
		Message:        message,
		Type:           eventType,
		Source:         corev1.EventSource{Component: "muster"},
		FirstTimestamp: metav1.NewTime(time.Now()),
		LastTimestamp:  metav1.NewTime(time.Now()),
		Count:          1,
	}

	if err := k.Create(ctx, event); err != nil {
		return fmt.Errorf("failed to create Kubernetes Event for %s %s/%s: %w", crdType, namespace, name, err)
	}

	return nil
}

// QueryEvents retrieves events from the Kubernetes Events API with filtering.
func (k *kubernetesClient) QueryEvents(ctx context.Context, options api.EventQueryOptions) (*api.EventQueryResult, error) {
	eventList := &corev1.EventList{}

	// Build list options with field selectors for filtering
	listOptions := &client.ListOptions{}

	// Build field selector for filtering
	var fieldSelectors []string

	if options.ResourceType != "" {
		fieldSelectors = append(fieldSelectors, fmt.Sprintf("involvedObject.kind=%s", options.ResourceType))
	}

	if options.ResourceName != "" {
		fieldSelectors = append(fieldSelectors, fmt.Sprintf("involvedObject.name=%s", options.ResourceName))
	}

	if options.Namespace != "" {
		listOptions.Namespace = options.Namespace
	}

	if options.EventType != "" {
		fieldSelectors = append(fieldSelectors, fmt.Sprintf("type=%s", options.EventType))
	}

	// Note: source.component is not a supported field selector, so we'll filter client-side

	if len(fieldSelectors) > 0 {
		fieldSelector := strings.Join(fieldSelectors, ",")
		listOptions.FieldSelector = fields.ParseSelectorOrDie(fieldSelector)
	}

	// Query the Kubernetes Events API
	if err := k.List(ctx, eventList, listOptions); err != nil {
		return nil, fmt.Errorf("failed to list Kubernetes events: %w", err)
	}

	// Convert Kubernetes events to our event format and filter
	var results []api.EventResult
	for _, event := range eventList.Items {
		// Filter to only include muster-generated events
		if event.Source.Component != "muster" {
			continue
		}

		result := k.convertKubernetesEvent(&event)

		// Apply time-based filtering (Kubernetes field selectors don't support time ranges well)
		if options.Since != nil && result.Timestamp.Before(*options.Since) {
			continue
		}

		if options.Until != nil && result.Timestamp.After(*options.Until) {
			continue
		}

		results = append(results, result)
	}

	// Sort by timestamp (newest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Timestamp.After(results[j].Timestamp)
	})

	totalCount := len(results)

	// Apply limit for initial result
	initialResults := results
	if options.Limit > 0 && len(results) > options.Limit {
		initialResults = results[:options.Limit]
	}

	return &api.EventQueryResult{
		Events:     initialResults,
		TotalCount: totalCount,
	}, nil
}

// convertKubernetesEvent converts a Kubernetes Event to our EventResult format.
func (k *kubernetesClient) convertKubernetesEvent(event *corev1.Event) api.EventResult {
	// Use LastTimestamp if available, otherwise FirstTimestamp
	timestamp := event.LastTimestamp.Time
	if timestamp.IsZero() && !event.FirstTimestamp.Time.IsZero() {
		timestamp = event.FirstTimestamp.Time
	}
	if timestamp.IsZero() {
		timestamp = event.CreationTimestamp.Time
	}

	return api.EventResult{
		Timestamp: timestamp,
		Namespace: event.Namespace,
		InvolvedObject: api.ObjectReference{
			APIVersion: event.InvolvedObject.APIVersion,
			Kind:       event.InvolvedObject.Kind,
			Name:       event.InvolvedObject.Name,
			Namespace:  event.InvolvedObject.Namespace,
			UID:        string(event.InvolvedObject.UID),
		},
		Reason:  event.Reason,
		Message: event.Message,
		Type:    event.Type,
		Source:  event.Source.Component,
		Count:   event.Count,
	}
}
