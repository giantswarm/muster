package events

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"muster/internal/api"
	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
)

// mockMusterClient implements a mock MusterClient for testing
type mockMusterClient struct {
	isKubernetes     bool
	events           []mockEvent
	eventForCRDCalls []mockEventForCRD
}

type mockEvent struct {
	obj       ctrlclient.Object
	reason    string
	message   string
	eventType string
}

type mockEventForCRD struct {
	crdType   string
	name      string
	namespace string
	reason    string
	message   string
	eventType string
}

func (m *mockMusterClient) CreateEvent(ctx context.Context, obj ctrlclient.Object, reason, message, eventType string) error {
	m.events = append(m.events, mockEvent{
		obj:       obj,
		reason:    reason,
		message:   message,
		eventType: eventType,
	})
	return nil
}

func (m *mockMusterClient) CreateEventForCRD(ctx context.Context, crdType, name, namespace, reason, message, eventType string) error {
	m.eventForCRDCalls = append(m.eventForCRDCalls, mockEventForCRD{
		crdType:   crdType,
		name:      name,
		namespace: namespace,
		reason:    reason,
		message:   message,
		eventType: eventType,
	})
	return nil
}

// QueryEvents implements the new QueryEvents method for the mock
func (m *mockMusterClient) QueryEvents(ctx context.Context, options api.EventQueryOptions) (*api.EventQueryResult, error) {
	// Return empty result for mock
	return &api.EventQueryResult{
		Events:     []api.EventResult{},
		TotalCount: 0,
	}, nil
}

func (m *mockMusterClient) IsKubernetesMode() bool {
	return m.isKubernetes
}

func (m *mockMusterClient) Close() error {
	return nil
}

// Implement the remaining MusterClient interface methods as no-ops for testing
func (m *mockMusterClient) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	return nil
}

func (m *mockMusterClient) List(ctx context.Context, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error {
	return nil
}

func (m *mockMusterClient) Create(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.CreateOption) error {
	return nil
}

func (m *mockMusterClient) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.UpdateOption) error {
	return nil
}

func (m *mockMusterClient) Delete(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.DeleteOption) error {
	return nil
}

func (m *mockMusterClient) Patch(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.PatchOption) error {
	return nil
}

func (m *mockMusterClient) DeleteAllOf(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.DeleteAllOfOption) error {
	return nil
}

func (m *mockMusterClient) Apply(ctx context.Context, applyConfig runtime.ApplyConfiguration, opts ...ctrlclient.ApplyOption) error {
	return nil
}

func (m *mockMusterClient) Status() ctrlclient.StatusWriter {
	return nil
}

func (m *mockMusterClient) SubResource(subResource string) ctrlclient.SubResourceClient {
	return nil
}

func (m *mockMusterClient) Scheme() *runtime.Scheme {
	return nil
}

func (m *mockMusterClient) RESTMapper() meta.RESTMapper {
	return nil
}

func (m *mockMusterClient) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}

func (m *mockMusterClient) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	return true, nil
}

// CRD operation methods - implement as no-ops for testing
func (m *mockMusterClient) GetMCPServer(ctx context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error) {
	return nil, nil
}

func (m *mockMusterClient) ListMCPServers(ctx context.Context, namespace string) ([]musterv1alpha1.MCPServer, error) {
	return nil, nil
}

func (m *mockMusterClient) CreateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	return nil
}

func (m *mockMusterClient) UpdateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	return nil
}

func (m *mockMusterClient) DeleteMCPServer(ctx context.Context, name, namespace string) error {
	return nil
}

func (m *mockMusterClient) GetServiceClass(ctx context.Context, name, namespace string) (*musterv1alpha1.ServiceClass, error) {
	return nil, nil
}

func (m *mockMusterClient) ListServiceClasses(ctx context.Context, namespace string) ([]musterv1alpha1.ServiceClass, error) {
	return nil, nil
}

func (m *mockMusterClient) CreateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	return nil
}

func (m *mockMusterClient) UpdateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	return nil
}

func (m *mockMusterClient) DeleteServiceClass(ctx context.Context, name, namespace string) error {
	return nil
}

func (m *mockMusterClient) GetWorkflow(ctx context.Context, name, namespace string) (*musterv1alpha1.Workflow, error) {
	return nil, nil
}

func (m *mockMusterClient) ListWorkflows(ctx context.Context, namespace string) ([]musterv1alpha1.Workflow, error) {
	return nil, nil
}

func (m *mockMusterClient) CreateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	return nil
}

func (m *mockMusterClient) UpdateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	return nil
}

func (m *mockMusterClient) DeleteWorkflow(ctx context.Context, name, namespace string) error {
	return nil
}

// Status update methods - implement as no-ops for testing
func (m *mockMusterClient) UpdateMCPServerStatus(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	return nil
}

func (m *mockMusterClient) UpdateServiceClassStatus(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	return nil
}

func (m *mockMusterClient) UpdateWorkflowStatus(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	return nil
}

func TestEventGenerator_MCPServerEvent(t *testing.T) {
	mockClient := &mockMusterClient{isKubernetes: true}
	generator := NewEventGenerator(mockClient)

	server := &musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-server",
			Namespace: "default",
		},
	}

	data := EventData{
		Operation: "create",
	}

	err := generator.MCPServerEvent(server, ReasonMCPServerCreated, data)
	if err != nil {
		t.Fatalf("MCPServerEvent failed: %v", err)
	}

	if len(mockClient.events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(mockClient.events))
	}

	event := mockClient.events[0]
	if event.reason != string(ReasonMCPServerCreated) {
		t.Errorf("Expected reason %s, got %s", ReasonMCPServerCreated, event.reason)
	}

	if event.eventType != string(EventTypeNormal) {
		t.Errorf("Expected event type %s, got %s", EventTypeNormal, event.eventType)
	}

	expectedMessage := "MCPServer test-server successfully created in namespace default"
	if event.message != expectedMessage {
		t.Errorf("Expected message %s, got %s", expectedMessage, event.message)
	}
}

func TestEventGenerator_CRDEvent(t *testing.T) {
	mockClient := &mockMusterClient{isKubernetes: false}
	generator := NewEventGenerator(mockClient)

	data := EventData{
		Error: "connection failed",
	}

	err := generator.CRDEvent("MCPServer", "test-server", "default", ReasonMCPServerFailed, data)
	if err != nil {
		t.Fatalf("CRDEvent failed: %v", err)
	}

	if len(mockClient.eventForCRDCalls) != 1 {
		t.Fatalf("Expected 1 CRD event, got %d", len(mockClient.eventForCRDCalls))
	}

	event := mockClient.eventForCRDCalls[0]
	if event.crdType != "MCPServer" {
		t.Errorf("Expected CRD type MCPServer, got %s", event.crdType)
	}

	if event.reason != string(ReasonMCPServerFailed) {
		t.Errorf("Expected reason %s, got %s", ReasonMCPServerFailed, event.reason)
	}

	if event.eventType != string(EventTypeWarning) {
		t.Errorf("Expected event type %s, got %s", EventTypeWarning, event.eventType)
	}

	expectedMessage := "MCPServer test-server operation failed: connection failed"
	if event.message != expectedMessage {
		t.Errorf("Expected message %s, got %s", expectedMessage, event.message)
	}
}

func TestEventGenerator_ServiceInstanceEvent(t *testing.T) {
	mockClient := &mockMusterClient{isKubernetes: true}
	generator := NewEventGenerator(mockClient)

	data := EventData{
		Duration: 2 * time.Second,
	}

	err := generator.ServiceInstanceEvent("my-service", "web-app", "default", ReasonServiceInstanceStarted, data)
	if err != nil {
		t.Fatalf("ServiceInstanceEvent failed: %v", err)
	}

	if len(mockClient.eventForCRDCalls) != 1 {
		t.Fatalf("Expected 1 CRD event, got %d", len(mockClient.eventForCRDCalls))
	}

	event := mockClient.eventForCRDCalls[0]
	if event.crdType != "ServiceInstance" {
		t.Errorf("Expected CRD type ServiceInstance, got %s", event.crdType)
	}

	if event.name != "my-service" {
		t.Errorf("Expected name my-service, got %s", event.name)
	}

	expectedMessage := "Service instance my-service started successfully and is running"
	if event.message != expectedMessage {
		t.Errorf("Expected message %s, got %s", expectedMessage, event.message)
	}
}

func TestEventGenerator_IsKubernetesMode(t *testing.T) {
	// Test Kubernetes mode
	mockClientK8s := &mockMusterClient{isKubernetes: true}
	generatorK8s := NewEventGenerator(mockClientK8s)

	if !generatorK8s.IsKubernetesMode() {
		t.Error("Expected Kubernetes mode to be true")
	}

	// Test filesystem mode
	mockClientFS := &mockMusterClient{isKubernetes: false}
	generatorFS := NewEventGenerator(mockClientFS)

	if generatorFS.IsKubernetesMode() {
		t.Error("Expected Kubernetes mode to be false")
	}
}
