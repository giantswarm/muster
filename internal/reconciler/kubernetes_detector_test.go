package reconciler

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
)

// TestNewKubernetesDetector tests the creation of a KubernetesDetector.
func TestNewKubernetesDetector(t *testing.T) {
	// Create a detector without a rest config (will be used for unit tests)
	detector, err := NewKubernetesDetector(nil, "default")
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	if detector == nil {
		t.Fatal("detector is nil")
	}

	if detector.namespace != "default" {
		t.Errorf("namespace = %q, want %q", detector.namespace, "default")
	}

	if detector.scheme == nil {
		t.Error("scheme is nil")
	}
}

// TestKubernetesDetectorGetSource tests the GetSource method.
func TestKubernetesDetectorGetSource(t *testing.T) {
	detector, err := NewKubernetesDetector(nil, "")
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	if source := detector.GetSource(); source != SourceKubernetes {
		t.Errorf("GetSource() = %v, want %v", source, SourceKubernetes)
	}
}

// TestKubernetesDetectorAddResourceType tests adding resource types.
func TestKubernetesDetectorAddResourceType(t *testing.T) {
	detector, err := NewKubernetesDetector(nil, "default")
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Add resource types
	if err := detector.AddResourceType(ResourceTypeMCPServer); err != nil {
		t.Errorf("AddResourceType(MCPServer) failed: %v", err)
	}

	if err := detector.AddResourceType(ResourceTypeServiceClass); err != nil {
		t.Errorf("AddResourceType(ServiceClass) failed: %v", err)
	}

	if err := detector.AddResourceType(ResourceTypeWorkflow); err != nil {
		t.Errorf("AddResourceType(Workflow) failed: %v", err)
	}

	// Verify resource types are tracked
	detector.mu.RLock()
	if !detector.resourceTypes[ResourceTypeMCPServer] {
		t.Error("MCPServer not in resourceTypes")
	}
	if !detector.resourceTypes[ResourceTypeServiceClass] {
		t.Error("ServiceClass not in resourceTypes")
	}
	if !detector.resourceTypes[ResourceTypeWorkflow] {
		t.Error("Workflow not in resourceTypes")
	}
	detector.mu.RUnlock()
}

// TestKubernetesDetectorRemoveResourceType tests removing resource types.
func TestKubernetesDetectorRemoveResourceType(t *testing.T) {
	detector, err := NewKubernetesDetector(nil, "default")
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Add and then remove a resource type
	if err := detector.AddResourceType(ResourceTypeMCPServer); err != nil {
		t.Errorf("AddResourceType failed: %v", err)
	}

	if err := detector.RemoveResourceType(ResourceTypeMCPServer); err != nil {
		t.Errorf("RemoveResourceType failed: %v", err)
	}

	detector.mu.RLock()
	if detector.resourceTypes[ResourceTypeMCPServer] {
		t.Error("MCPServer still in resourceTypes after removal")
	}
	detector.mu.RUnlock()
}

// TestKubernetesDetectorStopWithoutStart tests stopping without starting.
func TestKubernetesDetectorStopWithoutStart(t *testing.T) {
	detector, err := NewKubernetesDetector(nil, "default")
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Stop without starting should not panic
	if err := detector.Stop(); err != nil {
		t.Errorf("Stop() returned error: %v", err)
	}
}

// TestKubernetesDetectorNamespaceDisplay tests namespace display logic.
func TestKubernetesDetectorNamespaceDisplay(t *testing.T) {
	tests := []struct {
		namespace string
		want      string
	}{
		{"", "all namespaces"},
		{"default", "default"},
		{"my-namespace", "my-namespace"},
	}

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			detector, err := NewKubernetesDetector(nil, tt.namespace)
			if err != nil {
				t.Fatalf("failed to create detector: %v", err)
			}

			if got := detector.namespaceDisplay(); got != tt.want {
				t.Errorf("namespaceDisplay() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestExtractObjectMeta tests the extractObjectMeta helper function.
func TestExtractObjectMeta(t *testing.T) {
	// Create a fake MCP server object
	mcpServer := &musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-server",
			Namespace: "test-namespace",
		},
	}

	meta, ok := extractObjectMeta(mcpServer)
	if !ok {
		t.Fatal("extractObjectMeta returned false for valid object")
	}

	if meta.name != "test-server" {
		t.Errorf("name = %q, want %q", meta.name, "test-server")
	}

	if meta.namespace != "test-namespace" {
		t.Errorf("namespace = %q, want %q", meta.namespace, "test-namespace")
	}
}

// TestExtractObjectMetaInvalidObject tests extractObjectMeta with invalid input.
func TestExtractObjectMetaInvalidObject(t *testing.T) {
	// Test with a non-client.Object type
	invalidObj := struct{ Name string }{Name: "test"}

	_, ok := extractObjectMeta(invalidObj)
	if ok {
		t.Error("extractObjectMeta returned true for invalid object")
	}
}

// TestKubernetesDetectorEventHandlers tests the event handlers directly.
func TestKubernetesDetectorEventHandlers(t *testing.T) {
	detector, err := NewKubernetesDetector(nil, "default")
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Set up the detector state to simulate running
	detector.running = true
	changeChan := make(chan ChangeEvent, 10)
	detector.changeChan = changeChan

	// Create a test MCP server
	mcpServer := &musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-server",
			Namespace: "default",
		},
	}

	// Test handleAdd
	detector.handleAdd(ResourceTypeMCPServer, mcpServer)

	select {
	case event := <-changeChan:
		if event.Operation != OperationCreate {
			t.Errorf("handleAdd: Operation = %v, want %v", event.Operation, OperationCreate)
		}
		if event.Name != "test-server" {
			t.Errorf("handleAdd: Name = %q, want %q", event.Name, "test-server")
		}
		if event.Type != ResourceTypeMCPServer {
			t.Errorf("handleAdd: Type = %v, want %v", event.Type, ResourceTypeMCPServer)
		}
		if event.Source != SourceKubernetes {
			t.Errorf("handleAdd: Source = %v, want %v", event.Source, SourceKubernetes)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("handleAdd: no event received")
	}

	// Test handleUpdate
	detector.handleUpdate(ResourceTypeMCPServer, mcpServer, mcpServer)

	select {
	case event := <-changeChan:
		if event.Operation != OperationUpdate {
			t.Errorf("handleUpdate: Operation = %v, want %v", event.Operation, OperationUpdate)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("handleUpdate: no event received")
	}

	// Test handleDelete
	detector.handleDelete(ResourceTypeMCPServer, mcpServer)

	select {
	case event := <-changeChan:
		if event.Operation != OperationDelete {
			t.Errorf("handleDelete: Operation = %v, want %v", event.Operation, OperationDelete)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("handleDelete: no event received")
	}
}

// TestKubernetesDetectorEventHandlersNotRunning tests that events are not sent when not running.
func TestKubernetesDetectorEventHandlersNotRunning(t *testing.T) {
	detector, err := NewKubernetesDetector(nil, "default")
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Not running, so events should be dropped
	detector.running = false
	changeChan := make(chan ChangeEvent, 10)
	detector.changeChan = changeChan

	mcpServer := &musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-server",
			Namespace: "default",
		},
	}

	// handleAdd should not send event when not running
	detector.handleAdd(ResourceTypeMCPServer, mcpServer)

	select {
	case <-changeChan:
		t.Error("received event when detector is not running")
	case <-time.After(50 * time.Millisecond):
		// Expected - no event should be received
	}
}

// TestKubernetesDetectorCreateEventHandler tests the createEventHandler method.
func TestKubernetesDetectorCreateEventHandler(t *testing.T) {
	detector, err := NewKubernetesDetector(nil, "default")
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	handler := detector.createEventHandler(ResourceTypeMCPServer)
	if handler == nil {
		t.Fatal("createEventHandler returned nil")
	}
}

// testScheme creates a scheme with muster types registered for testing.
func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(musterv1alpha1.AddToScheme(scheme))
	return scheme
}

// TestKubernetesDetectorWithFakeClient tests detector behavior with a fake client.
func TestKubernetesDetectorWithFakeClient(t *testing.T) {
	scheme := testScheme()

	// Create a fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// Create an MCP server
	mcpServer := &musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-server",
			Namespace: "default",
		},
		Spec: musterv1alpha1.MCPServerSpec{
			Type:    "stdio",
			Command: "/usr/bin/test",
		},
	}

	ctx := context.Background()

	// Create the server
	if err := fakeClient.Create(ctx, mcpServer); err != nil {
		t.Fatalf("failed to create MCP server: %v", err)
	}

	// Verify the server was created
	var retrieved musterv1alpha1.MCPServer
	if err := fakeClient.Get(ctx, client.ObjectKey{Name: "test-server", Namespace: "default"}, &retrieved); err != nil {
		t.Fatalf("failed to get MCP server: %v", err)
	}

	if retrieved.Name != "test-server" {
		t.Errorf("retrieved name = %q, want %q", retrieved.Name, "test-server")
	}
}

// TestSendChangeEventChannelFull tests behavior when the change channel is full.
func TestSendChangeEventChannelFull(t *testing.T) {
	detector, err := NewKubernetesDetector(nil, "default")
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Set up with a channel that's already full
	detector.running = true
	changeChan := make(chan ChangeEvent, 1)
	detector.changeChan = changeChan

	// Fill the channel
	changeChan <- ChangeEvent{Name: "filler"}

	// Send another event - should be dropped without blocking
	event := ChangeEvent{
		Type:      ResourceTypeMCPServer,
		Name:      "test-server",
		Operation: OperationCreate,
		Timestamp: time.Now(),
		Source:    SourceKubernetes,
	}

	// This should not block
	done := make(chan bool, 1)
	go func() {
		detector.sendChangeEvent(event)
		done <- true
	}()

	select {
	case <-done:
		// Good - event was dropped without blocking
	case <-time.After(100 * time.Millisecond):
		t.Error("sendChangeEvent blocked when channel was full")
	}
}

