package reconciler

import (
	"context"
	"fmt"
	"testing"

	"muster/internal/api"
)

// mockServiceClassManager implements ServiceClassManager for testing.
type mockServiceClassManager struct {
	serviceClasses map[string]*api.ServiceClass
}

func newMockServiceClassManager() *mockServiceClassManager {
	return &mockServiceClassManager{
		serviceClasses: make(map[string]*api.ServiceClass),
	}
}

func (m *mockServiceClassManager) ListServiceClasses() []api.ServiceClass {
	result := make([]api.ServiceClass, 0, len(m.serviceClasses))
	for _, sc := range m.serviceClasses {
		result = append(result, *sc)
	}
	return result
}

func (m *mockServiceClassManager) GetServiceClass(name string) (*api.ServiceClass, error) {
	sc, ok := m.serviceClasses[name]
	if !ok {
		return nil, fmt.Errorf("ServiceClass %s not found", name)
	}
	return sc, nil
}

func (m *mockServiceClassManager) AddServiceClass(sc *api.ServiceClass) {
	m.serviceClasses[sc.Name] = sc
}

func (m *mockServiceClassManager) RemoveServiceClass(name string) {
	delete(m.serviceClasses, name)
}

func TestServiceClassReconciler_GetResourceType(t *testing.T) {
	mgr := newMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	if reconciler.GetResourceType() != ResourceTypeServiceClass {
		t.Errorf("expected ResourceTypeServiceClass, got %s", reconciler.GetResourceType())
	}
}

func TestServiceClassReconciler_ReconcileCreate(t *testing.T) {
	mgr := newMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	// Add a valid ServiceClass
	mgr.AddServiceClass(&api.ServiceClass{
		Name:        "test-class",
		Description: "Test ServiceClass",
		ServiceConfig: api.ServiceConfig{
			ServiceType: "test-type",
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: "start-tool"},
				Stop:  api.ToolCall{Tool: "stop-tool"},
			},
		},
		Available: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeServiceClass,
		Name:    "test-class",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
	if result.Requeue {
		t.Error("expected no requeue for successful reconciliation")
	}
}

func TestServiceClassReconciler_ReconcileDelete(t *testing.T) {
	mgr := newMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	// Do not add the ServiceClass - simulate a delete scenario
	req := ReconcileRequest{
		Type:    ResourceTypeServiceClass,
		Name:    "deleted-class",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error for delete: %v", result.Error)
	}
	if result.Requeue {
		t.Error("expected no requeue for delete")
	}
}

func TestServiceClassReconciler_ReconcileValidationError(t *testing.T) {
	mgr := newMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	// Add an invalid ServiceClass (missing required fields)
	mgr.AddServiceClass(&api.ServiceClass{
		Name:        "invalid-class",
		Description: "Invalid ServiceClass",
		ServiceConfig: api.ServiceConfig{
			// Missing ServiceType
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: "start-tool"},
				Stop:  api.ToolCall{Tool: "stop-tool"},
			},
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeServiceClass,
		Name:    "invalid-class",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected validation error for invalid ServiceClass")
	}
	if !result.Requeue {
		t.Error("expected requeue on validation error")
	}
}

func TestServiceClassReconciler_ValidateServiceClass(t *testing.T) {
	mgr := newMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	tests := []struct {
		name        string
		sc          *api.ServiceClass
		expectError bool
	}{
		{
			name: "valid serviceclass",
			sc: &api.ServiceClass{
				Name: "valid",
				ServiceConfig: api.ServiceConfig{
					ServiceType: "test",
					LifecycleTools: api.LifecycleTools{
						Start: api.ToolCall{Tool: "start"},
						Stop:  api.ToolCall{Tool: "stop"},
					},
				},
			},
			expectError: false,
		},
		{
			name: "missing name",
			sc: &api.ServiceClass{
				Name: "",
				ServiceConfig: api.ServiceConfig{
					ServiceType: "test",
					LifecycleTools: api.LifecycleTools{
						Start: api.ToolCall{Tool: "start"},
						Stop:  api.ToolCall{Tool: "stop"},
					},
				},
			},
			expectError: true,
		},
		{
			name: "missing service type",
			sc: &api.ServiceClass{
				Name: "test",
				ServiceConfig: api.ServiceConfig{
					ServiceType: "",
					LifecycleTools: api.LifecycleTools{
						Start: api.ToolCall{Tool: "start"},
						Stop:  api.ToolCall{Tool: "stop"},
					},
				},
			},
			expectError: true,
		},
		{
			name: "missing start tool",
			sc: &api.ServiceClass{
				Name: "test",
				ServiceConfig: api.ServiceConfig{
					ServiceType: "test",
					LifecycleTools: api.LifecycleTools{
						Start: api.ToolCall{Tool: ""},
						Stop:  api.ToolCall{Tool: "stop"},
					},
				},
			},
			expectError: true,
		},
		{
			name: "missing stop tool",
			sc: &api.ServiceClass{
				Name: "test",
				ServiceConfig: api.ServiceConfig{
					ServiceType: "test",
					LifecycleTools: api.LifecycleTools{
						Start: api.ToolCall{Tool: "start"},
						Stop:  api.ToolCall{Tool: ""},
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := reconciler.validateServiceClass(tt.sc)
			if tt.expectError && err == nil {
				t.Error("expected validation error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected validation error: %v", err)
			}
		})
	}
}
