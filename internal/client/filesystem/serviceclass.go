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

// GetServiceClass retrieves a specific ServiceClass from filesystem.
func (f *Client) GetServiceClass(ctx context.Context, name, namespace string) (*musterv1alpha1.ServiceClass, error) {
	filePath := f.getServiceClassPath(name)

	data, err := os.ReadFile(filePath) //nolint:gosec
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

	if serviceClass.Name == "" {
		serviceClass.Name = name
	}
	if serviceClass.Namespace == "" {
		serviceClass.Namespace = defaultNamespace
	}

	return &serviceClass, nil
}

// ListServiceClasses lists all ServiceClasses from filesystem.
func (f *Client) ListServiceClasses(ctx context.Context, namespace string) ([]musterv1alpha1.ServiceClass, error) {
	dirPath := f.getServiceClassDir()

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
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
			logging.Error("fs-client", err, "Failed to load ServiceClass %s", entry.Name())
			continue
		}
		serviceClasses = append(serviceClasses, *serviceClass)
	}

	return serviceClasses, nil
}

// CreateServiceClass creates a new ServiceClass in filesystem.
func (f *Client) CreateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	filePath := f.getServiceClassPath(serviceClass.Name)
	if _, err := os.Stat(filePath); err == nil {
		return errors.NewAlreadyExists(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "serviceclasses"},
			serviceClass.Name,
		)
	}

	dirPath := f.getServiceClassDir()
	if err := os.MkdirAll(dirPath, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("failed to create directory %s: %w", dirPath, err)
	}

	if serviceClass.Namespace == "" {
		serviceClass.Namespace = defaultNamespace
	}

	data, err := yaml.Marshal(serviceClass)
	if err != nil {
		return fmt.Errorf("failed to marshal ServiceClass %s: %w", serviceClass.Name, err)
	}

	if err := atomicWriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write ServiceClass file %s: %w", filePath, err)
	}

	return nil
}

// UpdateServiceClass updates an existing ServiceClass in filesystem.
func (f *Client) UpdateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	filePath := f.getServiceClassPath(serviceClass.Name)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "serviceclasses"},
			serviceClass.Name,
		)
	}

	if serviceClass.Namespace == "" {
		serviceClass.Namespace = defaultNamespace
	}

	data, err := yaml.Marshal(serviceClass)
	if err != nil {
		return fmt.Errorf("failed to marshal ServiceClass %s: %w", serviceClass.Name, err)
	}

	if err := atomicWriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write ServiceClass file %s: %w", filePath, err)
	}

	return nil
}

// DeleteServiceClass deletes a ServiceClass from filesystem.
func (f *Client) DeleteServiceClass(ctx context.Context, name, namespace string) error {
	filePath := f.getServiceClassPath(name)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "serviceclasses"},
			name,
		)
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete ServiceClass file %s: %w", filePath, err)
	}

	return nil
}

// UpdateServiceClassStatus updates only the status. In filesystem mode the
// whole file is rewritten — status is embedded in the YAML.
func (f *Client) UpdateServiceClassStatus(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	return f.UpdateServiceClass(ctx, serviceClass)
}

func (f *Client) getServiceClassDir() string {
	return filepath.Join(f.basePath, "serviceclasses")
}

func (f *Client) getServiceClassPath(name string) string {
	return filepath.Join(f.getServiceClassDir(), name+".yaml")
}
