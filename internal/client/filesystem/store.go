package filesystem

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/pkg/logging"
)

// resourceMeta is the per-CRD persistence config: the GroupResource used in
// NotFound/AlreadyExists errors and the storage directory under basePath.
type resourceMeta struct {
	gr  schema.GroupResource
	dir string
}

func (m resourceMeta) dirPath(basePath string) string {
	return filepath.Join(basePath, m.dir)
}

func (m resourceMeta) filePath(basePath, name string) string {
	return filepath.Join(basePath, m.dir, name+".yaml")
}

var (
	mcpServerMeta = resourceMeta{
		gr:  schema.GroupResource{Group: musterv1alpha1.GroupVersion.Group, Resource: "mcpservers"},
		dir: "mcpservers",
	}
	workflowMeta = resourceMeta{
		gr:  schema.GroupResource{Group: musterv1alpha1.GroupVersion.Group, Resource: "workflows"},
		dir: "workflows",
	}
)

// getResource reads a YAML file into obj. Caller allocates obj (matches the
// controller-runtime client.Get convention).
func (f *Client) getResource(name string, obj client.Object, m resourceMeta) error {
	filePath := m.filePath(f.basePath, name)

	data, err := os.ReadFile(filePath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return errors.NewNotFound(m.gr, name)
		}
		return fmt.Errorf("failed to read %s file %s: %w", m.gr.Resource, filePath, err)
	}

	if err := yaml.Unmarshal(data, obj); err != nil {
		return fmt.Errorf("failed to unmarshal %s from %s: %w", m.gr.Resource, filePath, err)
	}

	if obj.GetName() == "" {
		obj.SetName(name)
	}
	if obj.GetNamespace() == "" {
		obj.SetNamespace(defaultNamespace)
	}
	return nil
}

// listResources populates list.Items by reading every YAML file under the
// resource directory. factory allocates a fresh typed object per file.
// Bad files are logged and skipped — same behaviour as before the refactor.
func (f *Client) listResources(list client.ObjectList, factory func() client.Object, m resourceMeta) error {
	dirPath := m.dirPath(f.basePath)

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read directory %s: %w", dirPath, err)
	}

	var items []runtime.Object
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}
		name := getNameFromFileName(entry.Name())
		obj := factory()
		if err := f.getResource(name, obj, m); err != nil {
			logging.Error("fs-client", err, "Failed to load %s %s", m.gr.Resource, entry.Name())
			continue
		}
		items = append(items, obj)
	}
	return meta.SetList(list, items)
}

// createResource writes obj to its YAML file. Returns AlreadyExists if the
// file is already present.
func (f *Client) createResource(obj client.Object, m resourceMeta) error {
	filePath := m.filePath(f.basePath, obj.GetName())
	if _, err := os.Stat(filePath); err == nil {
		return errors.NewAlreadyExists(m.gr, obj.GetName())
	}

	dirPath := m.dirPath(f.basePath)
	if err := os.MkdirAll(dirPath, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("failed to create directory %s: %w", dirPath, err)
	}

	if obj.GetNamespace() == "" {
		obj.SetNamespace(defaultNamespace)
	}

	data, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal %s %s: %w", m.gr.Resource, obj.GetName(), err)
	}
	if err := atomicWriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s file %s: %w", m.gr.Resource, filePath, err)
	}
	return nil
}

// updateResource rewrites obj's YAML file. Returns NotFound if the file is
// missing.
func (f *Client) updateResource(obj client.Object, m resourceMeta) error {
	filePath := m.filePath(f.basePath, obj.GetName())
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(m.gr, obj.GetName())
	}

	if obj.GetNamespace() == "" {
		obj.SetNamespace(defaultNamespace)
	}

	data, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal %s %s: %w", m.gr.Resource, obj.GetName(), err)
	}
	if err := atomicWriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s file %s: %w", m.gr.Resource, filePath, err)
	}
	return nil
}

// deleteResource removes the YAML file. Returns NotFound if missing.
func (f *Client) deleteResource(name string, m resourceMeta) error {
	filePath := m.filePath(f.basePath, name)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(m.gr, name)
	}
	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete %s file %s: %w", m.gr.Resource, filePath, err)
	}
	return nil
}
