package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/pkg/logging"
)

// GetWorkflow retrieves a specific Workflow from filesystem.
func (f *Client) GetWorkflow(ctx context.Context, name, namespace string) (*musterv1alpha1.Workflow, error) {
	filePath := f.getWorkflowPath(name)

	data, err := os.ReadFile(filePath) //nolint:gosec
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

	if workflow.Name == "" {
		workflow.Name = name
	}
	if workflow.Namespace == "" {
		workflow.Namespace = defaultNamespace
	}

	return &workflow, nil
}

// ListWorkflows lists all Workflows from filesystem.
func (f *Client) ListWorkflows(ctx context.Context, namespace string) ([]musterv1alpha1.Workflow, error) {
	dirPath := f.getWorkflowDir()

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
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
			logging.Error("fs-client", err, "Failed to load Workflow %s", entry.Name())
			continue
		}
		workflows = append(workflows, *workflow)
	}

	return workflows, nil
}

// CreateWorkflow creates a new Workflow in filesystem.
func (f *Client) CreateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	filePath := f.getWorkflowPath(workflow.Name)
	if _, err := os.Stat(filePath); err == nil {
		return errors.NewAlreadyExists(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "workflows"},
			workflow.Name,
		)
	}

	dirPath := f.getWorkflowDir()
	if err := os.MkdirAll(dirPath, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("failed to create directory %s: %w", dirPath, err)
	}

	if workflow.Namespace == "" {
		workflow.Namespace = defaultNamespace
	}

	data, err := yaml.Marshal(workflow)
	if err != nil {
		return fmt.Errorf("failed to marshal Workflow %s: %w", workflow.Name, err)
	}

	if err := atomicWriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write Workflow file %s: %w", filePath, err)
	}

	return nil
}

// UpdateWorkflow updates an existing Workflow in filesystem.
func (f *Client) UpdateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	filePath := f.getWorkflowPath(workflow.Name)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "workflows"},
			workflow.Name,
		)
	}

	if workflow.Namespace == "" {
		workflow.Namespace = defaultNamespace
	}

	data, err := yaml.Marshal(workflow)
	if err != nil {
		return fmt.Errorf("failed to marshal Workflow %s: %w", workflow.Name, err)
	}

	if err := atomicWriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write Workflow file %s: %w", filePath, err)
	}

	return nil
}

// DeleteWorkflow deletes a Workflow from filesystem.
func (f *Client) DeleteWorkflow(ctx context.Context, name, namespace string) error {
	filePath := f.getWorkflowPath(name)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "workflows"},
			name,
		)
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete Workflow file %s: %w", filePath, err)
	}

	return nil
}

// UpdateWorkflowStatus updates only the status. In filesystem mode the
// whole file is rewritten — status is embedded in the YAML.
func (f *Client) UpdateWorkflowStatus(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	return f.UpdateWorkflow(ctx, workflow)
}

func (f *Client) getWorkflowDir() string {
	return filepath.Join(f.basePath, "workflows")
}

func (f *Client) getWorkflowPath(name string) string {
	return filepath.Join(f.getWorkflowDir(), name+".yaml")
}
