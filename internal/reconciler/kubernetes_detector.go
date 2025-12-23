package reconciler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
	"muster/pkg/logging"
)

// KubernetesDetector implements ChangeDetector using controller-runtime informers.
//
// It watches muster CRDs (MCPServer, ServiceClass, Workflow) via Kubernetes informers
// and generates change events when resources are created, updated, or deleted.
//
// This detector provides native Kubernetes integration with proper event handling,
// caching, and efficient watch-based change detection.
type KubernetesDetector struct {
	mu sync.RWMutex

	// restConfig is the Kubernetes REST configuration
	restConfig *rest.Config

	// namespace is the Kubernetes namespace to watch (empty for all namespaces)
	namespace string

	// cache is the controller-runtime cache for watching resources
	cache cache.Cache

	// scheme is the runtime scheme with registered types
	scheme *runtime.Scheme

	// resourceTypes is the set of resource types being watched
	resourceTypes map[ResourceType]bool

	// changeChan is the channel to send change events to
	changeChan chan<- ChangeEvent

	// ctx is the detector's context
	ctx context.Context

	// cancelFunc cancels the detector's context
	cancelFunc context.CancelFunc

	// running indicates if the detector is active
	running bool

	// informerRegistrations tracks registered event handlers for cleanup
	informerRegistrations []toolscache.ResourceEventHandlerRegistration
}

// NewKubernetesDetector creates a new Kubernetes change detector.
//
// Args:
//   - restConfig: Kubernetes REST configuration for API access
//   - namespace: Namespace to watch (empty string watches all namespaces)
//
// Returns:
//   - *KubernetesDetector: The configured detector
//   - error: Error if scheme registration fails
func NewKubernetesDetector(restConfig *rest.Config, namespace string) (*KubernetesDetector, error) {
	// Create scheme with standard Kubernetes types and muster CRDs
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(musterv1alpha1.AddToScheme(scheme))

	return &KubernetesDetector{
		restConfig:            restConfig,
		namespace:             namespace,
		scheme:                scheme,
		resourceTypes:         make(map[ResourceType]bool),
		informerRegistrations: make([]toolscache.ResourceEventHandlerRegistration, 0),
	}, nil
}

// Start begins watching for Kubernetes resource changes.
func (d *KubernetesDetector) Start(ctx context.Context, changes chan<- ChangeEvent) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return nil
	}

	d.ctx, d.cancelFunc = context.WithCancel(ctx)
	d.changeChan = changes
	d.running = true
	d.mu.Unlock()

	// Create cache options
	cacheOpts := cache.Options{
		Scheme: d.scheme,
	}
	if d.namespace != "" {
		cacheOpts.DefaultNamespaces = map[string]cache.Config{
			d.namespace: {},
		}
	}

	// Create the cache
	c, err := cache.New(d.restConfig, cacheOpts)
	if err != nil {
		d.mu.Lock()
		d.running = false
		d.mu.Unlock()
		return fmt.Errorf("failed to create cache: %w", err)
	}

	d.mu.Lock()
	d.cache = c
	d.mu.Unlock()

	// Setup informers for all registered resource types
	if err := d.setupInformers(); err != nil {
		d.mu.Lock()
		d.running = false
		d.mu.Unlock()
		return fmt.Errorf("failed to setup informers: %w", err)
	}

	// Start the cache in a goroutine
	go func() {
		if err := d.cache.Start(d.ctx); err != nil {
			logging.Error("KubernetesDetector", err, "Cache stopped with error")
		}
	}()

	// Wait for cache to sync
	if !d.cache.WaitForCacheSync(d.ctx) {
		d.mu.Lock()
		d.running = false
		d.mu.Unlock()
		return fmt.Errorf("failed to sync cache")
	}

	logging.Info("KubernetesDetector", "Started watching Kubernetes resources in namespace: %s", d.namespaceDisplay())
	return nil
}

// setupInformers creates informers for all registered resource types.
func (d *KubernetesDetector) setupInformers() error {
	d.mu.RLock()
	types := make([]ResourceType, 0, len(d.resourceTypes))
	for rt := range d.resourceTypes {
		types = append(types, rt)
	}
	d.mu.RUnlock()

	for _, rt := range types {
		if err := d.setupInformerForType(rt); err != nil {
			logging.Warn("KubernetesDetector", "Failed to setup informer for %s: %v", rt, err)
			// Continue with other types
		}
	}

	return nil
}

// setupInformerForType creates an informer for a specific resource type.
func (d *KubernetesDetector) setupInformerForType(resourceType ResourceType) error {
	var obj client.Object
	switch resourceType {
	case ResourceTypeMCPServer:
		obj = &musterv1alpha1.MCPServer{}
	case ResourceTypeServiceClass:
		obj = &musterv1alpha1.ServiceClass{}
	case ResourceTypeWorkflow:
		obj = &musterv1alpha1.Workflow{}
	default:
		return fmt.Errorf("unsupported resource type: %s", resourceType)
	}

	// Get informer for the object type
	informer, err := d.cache.GetInformer(d.ctx, obj)
	if err != nil {
		return fmt.Errorf("failed to get informer for %s: %w", resourceType, err)
	}

	// Create an event handler for this resource type
	handler := d.createEventHandler(resourceType)

	// Register the event handler with the informer
	registration, err := informer.AddEventHandler(handler)
	if err != nil {
		return fmt.Errorf("failed to add event handler for %s: %w", resourceType, err)
	}

	d.mu.Lock()
	d.informerRegistrations = append(d.informerRegistrations, registration)
	d.mu.Unlock()

	logging.Debug("KubernetesDetector", "Setup informer for resource type: %s", resourceType)
	return nil
}

// createEventHandler creates a ResourceEventHandler for a specific resource type.
func (d *KubernetesDetector) createEventHandler(resourceType ResourceType) toolscache.ResourceEventHandler {
	return toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			d.handleAdd(resourceType, obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			d.handleUpdate(resourceType, oldObj, newObj)
		},
		DeleteFunc: func(obj interface{}) {
			d.handleDelete(resourceType, obj)
		},
	}
}

// handleAdd processes an add event from the informer.
func (d *KubernetesDetector) handleAdd(resourceType ResourceType, obj interface{}) {
	meta, ok := extractObjectMeta(obj)
	if !ok {
		logging.Warn("KubernetesDetector", "Failed to extract metadata from add event")
		return
	}

	changeEvent := ChangeEvent{
		Type:      resourceType,
		Name:      meta.name,
		Namespace: meta.namespace,
		Operation: OperationCreate,
		Timestamp: time.Now(),
		Source:    SourceKubernetes,
	}

	d.sendChangeEvent(changeEvent)
}

// handleUpdate processes an update event from the informer.
func (d *KubernetesDetector) handleUpdate(resourceType ResourceType, oldObj, newObj interface{}) {
	meta, ok := extractObjectMeta(newObj)
	if !ok {
		logging.Warn("KubernetesDetector", "Failed to extract metadata from update event")
		return
	}

	changeEvent := ChangeEvent{
		Type:      resourceType,
		Name:      meta.name,
		Namespace: meta.namespace,
		Operation: OperationUpdate,
		Timestamp: time.Now(),
		Source:    SourceKubernetes,
	}

	d.sendChangeEvent(changeEvent)
}

// handleDelete processes a delete event from the informer.
func (d *KubernetesDetector) handleDelete(resourceType ResourceType, obj interface{}) {
	// Handle DeletedFinalStateUnknown for objects deleted while controller was down
	if deletedState, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = deletedState.Obj
	}

	meta, ok := extractObjectMeta(obj)
	if !ok {
		logging.Warn("KubernetesDetector", "Failed to extract metadata from delete event")
		return
	}

	changeEvent := ChangeEvent{
		Type:      resourceType,
		Name:      meta.name,
		Namespace: meta.namespace,
		Operation: OperationDelete,
		Timestamp: time.Now(),
		Source:    SourceKubernetes,
	}

	d.sendChangeEvent(changeEvent)
}

// objectMeta holds extracted metadata from a Kubernetes object.
type objectMeta struct {
	name      string
	namespace string
}

// extractObjectMeta extracts name and namespace from a Kubernetes object.
func extractObjectMeta(obj interface{}) (objectMeta, bool) {
	if clientObj, ok := obj.(client.Object); ok {
		return objectMeta{
			name:      clientObj.GetName(),
			namespace: clientObj.GetNamespace(),
		}, true
	}
	return objectMeta{}, false
}

// sendChangeEvent sends a change event to the output channel.
func (d *KubernetesDetector) sendChangeEvent(event ChangeEvent) {
	d.mu.RLock()
	changeChan := d.changeChan
	running := d.running
	d.mu.RUnlock()

	if !running || changeChan == nil {
		return
	}

	select {
	case changeChan <- event:
		logging.Debug("KubernetesDetector", "Emitted change event: %s %s/%s/%s",
			event.Operation, event.Type, event.Namespace, event.Name)
	default:
		logging.Warn("KubernetesDetector", "Change event channel full, dropping event for %s/%s/%s",
			event.Type, event.Namespace, event.Name)
	}
}

// Stop gracefully stops the Kubernetes detector.
func (d *KubernetesDetector) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.running {
		return nil
	}

	d.running = false

	// Cancel the context to stop the cache and informers
	if d.cancelFunc != nil {
		d.cancelFunc()
	}

	// Clear registrations (they are automatically removed when cache stops)
	d.informerRegistrations = nil

	logging.Info("KubernetesDetector", "Stopped Kubernetes detector")
	return nil
}

// GetSource returns the change source type.
func (d *KubernetesDetector) GetSource() ChangeSource {
	return SourceKubernetes
}

// AddResourceType adds a resource type to watch.
func (d *KubernetesDetector) AddResourceType(resourceType ResourceType) error {
	d.mu.Lock()
	d.resourceTypes[resourceType] = true
	running := d.running
	c := d.cache
	d.mu.Unlock()

	// If already running, add the informer immediately
	if running && c != nil {
		return d.setupInformerForType(resourceType)
	}

	return nil
}

// RemoveResourceType removes a resource type from watching.
func (d *KubernetesDetector) RemoveResourceType(resourceType ResourceType) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.resourceTypes, resourceType)

	// Note: controller-runtime cache doesn't support removing individual informers,
	// but since we track resourceTypes, we won't process events for removed types

	return nil
}

// namespaceDisplay returns a display string for the namespace.
func (d *KubernetesDetector) namespaceDisplay() string {
	if d.namespace == "" {
		return "all namespaces"
	}
	return d.namespace
}

// GetRestConfig returns the REST config for creating a Kubernetes detector.
// This is a convenience function that uses controller-runtime's config detection.
func GetRestConfig() (*rest.Config, error) {
	return ctrl.GetConfig()
}

// IsKubernetesAvailable checks if Kubernetes cluster access is available.
func IsKubernetesAvailable() bool {
	config, err := ctrl.GetConfig()
	if err != nil {
		return false
	}

	// Try to create a simple client to verify connectivity
	_, err = client.New(config, client.Options{})
	return err == nil
}
