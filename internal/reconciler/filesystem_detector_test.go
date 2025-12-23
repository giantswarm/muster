package reconciler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilesystemDetector_ParseFilePath(t *testing.T) {
	detector := NewFilesystemDetector("/tmp/muster", 100*time.Millisecond)

	tests := []struct {
		name             string
		path             string
		expectedType     ResourceType
		expectedName     string
		shouldBeEmpty    bool
	}{
		{
			name:         "MCPServer YAML",
			path:         "/tmp/muster/mcpservers/kubernetes.yaml",
			expectedType: ResourceTypeMCPServer,
			expectedName: "kubernetes",
		},
		{
			name:         "ServiceClass YAML",
			path:         "/tmp/muster/serviceclasses/postgres-db.yaml",
			expectedType: ResourceTypeServiceClass,
			expectedName: "postgres-db",
		},
		{
			name:         "Workflow YAML",
			path:         "/tmp/muster/workflows/deploy-app.yaml",
			expectedType: ResourceTypeWorkflow,
			expectedName: "deploy-app",
		},
		{
			name:         "YML extension",
			path:         "/tmp/muster/mcpservers/test.yml",
			expectedType: ResourceTypeMCPServer,
			expectedName: "test",
		},
		{
			name:          "Unknown directory",
			path:          "/tmp/muster/unknown/test.yaml",
			shouldBeEmpty: true,
		},
		{
			name:          "Wrong base path",
			path:          "/other/mcpservers/test.yaml",
			shouldBeEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceType, name := detector.parseFilePath(tt.path)

			if tt.shouldBeEmpty {
				if resourceType != "" || name != "" {
					t.Errorf("expected empty result, got type=%s name=%s", resourceType, name)
				}
				return
			}

			if resourceType != tt.expectedType {
				t.Errorf("expected type %s, got %s", tt.expectedType, resourceType)
			}
			if name != tt.expectedName {
				t.Errorf("expected name %s, got %s", tt.expectedName, name)
			}
		})
	}
}

func TestFilesystemDetector_IsYAMLFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/path/to/file.yaml", true},
		{"/path/to/file.yml", true},
		{"/path/to/file.YAML", true},
		{"/path/to/file.YML", true},
		{"/path/to/file.json", false},
		{"/path/to/file.txt", false},
		{"/path/to/file", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isYAMLFile(tt.path); got != tt.expected {
				t.Errorf("isYAMLFile(%s) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

func TestFilesystemDetector_MergeOperations(t *testing.T) {
	tests := []struct {
		old      ChangeOperation
		new      ChangeOperation
		expected ChangeOperation
	}{
		{OperationCreate, OperationUpdate, OperationCreate},
		{OperationCreate, OperationDelete, OperationDelete},
		{OperationUpdate, OperationUpdate, OperationUpdate},
		{OperationUpdate, OperationDelete, OperationDelete},
		{OperationDelete, OperationCreate, OperationCreate},
	}

	for _, tt := range tests {
		t.Run(string(tt.old)+"_"+string(tt.new), func(t *testing.T) {
			if got := mergeOperations(tt.old, tt.new); got != tt.expected {
				t.Errorf("mergeOperations(%s, %s) = %s, want %s", tt.old, tt.new, got, tt.expected)
			}
		})
	}
}

func TestFilesystemDetector_AddResourceType(t *testing.T) {
	detector := NewFilesystemDetector("/tmp/test", 100*time.Millisecond)

	err := detector.AddResourceType(ResourceTypeMCPServer)
	if err != nil {
		t.Fatalf("failed to add resource type: %v", err)
	}

	if !detector.resourceTypes[ResourceTypeMCPServer] {
		t.Error("expected MCPServer to be in resource types")
	}

	err = detector.RemoveResourceType(ResourceTypeMCPServer)
	if err != nil {
		t.Fatalf("failed to remove resource type: %v", err)
	}

	if detector.resourceTypes[ResourceTypeMCPServer] {
		t.Error("expected MCPServer to be removed from resource types")
	}
}

func TestFilesystemDetector_StartStop(t *testing.T) {
	tempDir := t.TempDir()

	// Create the mcpservers directory
	mcpDir := filepath.Join(tempDir, "mcpservers")
	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	detector := NewFilesystemDetector(tempDir, 100*time.Millisecond)
	detector.AddResourceType(ResourceTypeMCPServer)

	ctx := context.Background()
	changes := make(chan ChangeEvent, 10)

	err := detector.Start(ctx, changes)
	if err != nil {
		t.Fatalf("failed to start detector: %v", err)
	}

	if detector.GetSource() != SourceFilesystem {
		t.Errorf("expected source Filesystem, got %s", detector.GetSource())
	}

	err = detector.Stop()
	if err != nil {
		t.Fatalf("failed to stop detector: %v", err)
	}
}

func TestFilesystemDetector_DetectFileChange(t *testing.T) {
	tempDir := t.TempDir()

	// Create the mcpservers directory
	mcpDir := filepath.Join(tempDir, "mcpservers")
	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	detector := NewFilesystemDetector(tempDir, 50*time.Millisecond)
	detector.AddResourceType(ResourceTypeMCPServer)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	changes := make(chan ChangeEvent, 10)

	err := detector.Start(ctx, changes)
	if err != nil {
		t.Fatalf("failed to start detector: %v", err)
	}
	defer detector.Stop()

	// Create a new file
	testFile := filepath.Join(mcpDir, "test-server.yaml")
	if err := os.WriteFile(testFile, []byte("name: test-server"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Wait for the change event
	select {
	case event := <-changes:
		if event.Type != ResourceTypeMCPServer {
			t.Errorf("expected type MCPServer, got %s", event.Type)
		}
		if event.Name != "test-server" {
			t.Errorf("expected name test-server, got %s", event.Name)
		}
		if event.Operation != OperationCreate {
			t.Errorf("expected operation Create, got %s", event.Operation)
		}
	case <-ctx.Done():
		t.Error("timeout waiting for change event")
	}
}

func TestFilesystemDetector_Debouncing(t *testing.T) {
	tempDir := t.TempDir()

	// Create the mcpservers directory
	mcpDir := filepath.Join(tempDir, "mcpservers")
	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	// Use a longer debounce for this test
	detector := NewFilesystemDetector(tempDir, 200*time.Millisecond)
	detector.AddResourceType(ResourceTypeMCPServer)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	changes := make(chan ChangeEvent, 10)

	err := detector.Start(ctx, changes)
	if err != nil {
		t.Fatalf("failed to start detector: %v", err)
	}
	defer detector.Stop()

	// Create a file
	testFile := filepath.Join(mcpDir, "debounce-test.yaml")
	if err := os.WriteFile(testFile, []byte("v1"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Rapidly update the file multiple times
	for i := 0; i < 5; i++ {
		time.Sleep(10 * time.Millisecond)
		if err := os.WriteFile(testFile, []byte("v"+string(rune('2'+i))), 0644); err != nil {
			t.Fatalf("failed to update test file: %v", err)
		}
	}

	// Wait for debounced event
	eventCount := 0
	timeout := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case <-changes:
			eventCount++
		case <-timeout:
			break loop
		}
	}

	// Should have received only 1 debounced event (or possibly 2 if timing is tight)
	if eventCount > 2 {
		t.Errorf("expected 1-2 debounced events, got %d", eventCount)
	}
}

