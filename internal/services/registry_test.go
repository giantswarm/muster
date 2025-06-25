package services

import (
	"context"
	"testing"
)

// testService implements the Service interface for testing
type testService struct {
	name         string
	serviceType  ServiceType
	state        ServiceState
	health       HealthStatus
	lastError    error
	dependencies []string
	callback     StateChangeCallback
}

func (s *testService) Start(ctx context.Context) error {
	return nil
}

func (s *testService) Stop(ctx context.Context) error {
	return nil
}

func (s *testService) Restart(ctx context.Context) error {
	return nil
}

func (s *testService) GetState() ServiceState {
	return s.state
}

func (s *testService) GetHealth() HealthStatus {
	return s.health
}

func (s *testService) GetLastError() error {
	return s.lastError
}

func (s *testService) GetName() string {
	return s.name
}

func (s *testService) GetType() ServiceType {
	return s.serviceType
}

func (s *testService) GetDependencies() []string {
	return s.dependencies
}

func (s *testService) SetStateChangeCallback(callback StateChangeCallback) {
	s.callback = callback
}

func TestNewRegistry(t *testing.T) {
	registry := NewRegistry()
	if registry == nil {
		t.Error("Expected NewRegistry to return non-nil registry")
	}
}

func TestRegister(t *testing.T) {
	registry := NewRegistry()

	// Test registering a valid service
	service := &testService{
		name:        "test-service",
		serviceType: TypeMCPServer,
		state:       StateRunning,
		health:      HealthHealthy,
	}

	err := registry.Register(service)
	if err != nil {
		t.Fatalf("Unexpected error registering service: %v", err)
	}

	// Test registering nil service
	err = registry.Register(nil)
	if err == nil {
		t.Error("Expected error when registering nil service")
	}

	if err.Error() != "cannot register nil service" {
		t.Errorf("Expected specific error message, got: %s", err.Error())
	}

	// Test registering service with empty name
	emptyNameService := &testService{
		name:        "",
		serviceType: TypeMCPServer,
	}

	err = registry.Register(emptyNameService)
	if err == nil {
		t.Error("Expected error when registering service with empty name")
	}

	if err.Error() != "service has empty name" {
		t.Errorf("Expected specific error message, got: %s", err.Error())
	}

	// Test registering duplicate service
	duplicateService := &testService{
		name:        "test-service",
		serviceType: TypeMCPServer,
	}

	err = registry.Register(duplicateService)
	if err == nil {
		t.Error("Expected error when registering duplicate service")
	}

	if err.Error() != "service test-service already registered" {
		t.Errorf("Expected specific error message, got: %s", err.Error())
	}
}

func TestGet(t *testing.T) {
	registry := NewRegistry()

	// Test getting non-existent service
	_, exists := registry.Get("nonexistent")
	if exists {
		t.Error("Expected service to not exist")
	}

	// Register a service
	service := &testService{
		name:        "get-test",
		serviceType: TypeMCPServer,
		state:       StateRunning,
		health:      HealthHealthy,
	}

	registry.Register(service)

	// Test getting existing service
	retrieved, exists := registry.Get("get-test")
	if !exists {
		t.Error("Expected service to exist")
	}

	if retrieved != service {
		t.Error("Expected to get the same service instance")
	}

	if retrieved.GetName() != "get-test" {
		t.Errorf("Expected name 'get-test', got %s", retrieved.GetName())
	}
}

func TestUnregister(t *testing.T) {
	registry := NewRegistry()

	// Test unregistering non-existent service
	err := registry.Unregister("nonexistent")
	if err == nil {
		t.Error("Expected error when unregistering non-existent service")
	}

	if err.Error() != "service nonexistent not found" {
		t.Errorf("Expected specific error message, got: %s", err.Error())
	}

	// Register a service
	service := &testService{
		name:        "unregister-test",
		serviceType: TypeMCPServer,
	}

	registry.Register(service)

	// Verify service exists
	_, exists := registry.Get("unregister-test")
	if !exists {
		t.Error("Expected service to exist before unregistering")
	}

	// Unregister the service
	err = registry.Unregister("unregister-test")
	if err != nil {
		t.Fatalf("Unexpected error unregistering service: %v", err)
	}

	// Verify service no longer exists
	_, exists = registry.Get("unregister-test")
	if exists {
		t.Error("Expected service to not exist after unregistering")
	}
}

func TestGetAll(t *testing.T) {
	registry := NewRegistry()

	// Test getting all from empty registry
	services := registry.GetAll()
	if len(services) != 0 {
		t.Errorf("Expected 0 services, got %d", len(services))
	}

	// Register multiple services
	service1 := &testService{
		name:        "service-1",
		serviceType: TypeMCPServer,
	}

	service2 := &testService{
		name:        "service-2",
		serviceType: TypeMCPServer,
	}

	service3 := &testService{
		name:        "service-3",
		serviceType: TypeMCPServer,
	}

	registry.Register(service1)
	registry.Register(service2)
	registry.Register(service3)

	// Test getting all services
	services = registry.GetAll()
	if len(services) != 3 {
		t.Errorf("Expected 3 services, got %d", len(services))
	}

	// Verify all services are present
	names := make(map[string]bool)
	for _, service := range services {
		names[service.GetName()] = true
	}

	expectedNames := []string{"service-1", "service-2", "service-3"}
	for _, name := range expectedNames {
		if !names[name] {
			t.Errorf("Expected service %s to be in GetAll result", name)
		}
	}
}

func TestGetByType(t *testing.T) {
	registry := NewRegistry()

	// Register services of different types
	mcpService := &testService{
		name:        "mcp-1",
		serviceType: TypeMCPServer,
	}

	registry.Register(mcpService)

	// Test getting MCP services
	mcpServices := registry.GetByType(TypeMCPServer)
	if len(mcpServices) != 1 {
		t.Errorf("Expected 1 MCP service, got %d", len(mcpServices))
	}

	if mcpServices[0].GetName() != "mcp-1" {
		t.Errorf("Expected MCP service 'mcp-1', got %s", mcpServices[0].GetName())
	}

	// Test getting non-existent type (this should return empty slice)
	unknownServices := registry.GetByType(ServiceType("unknown"))
	if len(unknownServices) != 0 {
		t.Errorf("Expected 0 services for unknown type, got %d", len(unknownServices))
	}
}

func TestRegistryConcurrency(t *testing.T) {
	registry := NewRegistry()

	// Test concurrent registration and retrieval
	done := make(chan bool, 2)

	// Goroutine 1: Register services
	go func() {
		for i := 0; i < 10; i++ {
			service := &testService{
				name:        "concurrent-" + string(rune('0'+i)),
				serviceType: TypeMCPServer,
			}
			registry.Register(service)
		}
		done <- true
	}()

	// Goroutine 2: Get services
	go func() {
		for i := 0; i < 10; i++ {
			registry.GetAll()
			registry.GetByType(TypeMCPServer)
		}
		done <- true
	}()

	// Wait for both goroutines to complete
	<-done
	<-done

	// Verify final state
	services := registry.GetAll()
	if len(services) != 10 {
		t.Errorf("Expected 10 services after concurrent operations, got %d", len(services))
	}
}
