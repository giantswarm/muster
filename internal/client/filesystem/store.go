package filesystem

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// statusDir is the directory under basePath that holds the status muster
// records for the definitions, one file per definition at the definition's
// own path: status/mcpservers/<name>.yaml for mcpservers/<name>.yaml.
//
// A definition file is the operator's; the status is muster's. Keeping them
// apart is what makes a status write safe: it never rewrites a definition
// file, so an edit another process makes to the definition -- an operator's
// editor, a GitOps sync, the test harness -- cannot be overwritten by a status
// sync that read the file a moment earlier (#1288), and it never fires the
// change detector that watches the definition directories.
const statusDir = "status"

// resourceMeta is the per-CRD persistence config: the GroupResource used in
// NotFound/AlreadyExists errors and the storage directory under basePath.
type resourceMeta struct {
	gr  schema.GroupResource
	dir string
}

func (m resourceMeta) dirPath(basePath string) string {
	return filepath.Join(basePath, m.dir)
}

func (m resourceMeta) filePath(basePath, name string) (string, error) {
	// api.ValidateResourceName is the single choke point for caller-supplied
	// entity names: filepath.Join collapses ".." segments, so an unchecked name
	// like "../../evil" would read and write *.yaml files outside the config
	// directory.
	if err := api.ValidateResourceName(name); err != nil {
		return "", err
	}
	return filepath.Join(basePath, m.dir, name+".yaml"), nil
}

// statusPath is the file that holds the status recorded for the named
// definition.
func (m resourceMeta) statusPath(basePath, name string) (string, error) {
	if err := api.ValidateResourceName(name); err != nil {
		return "", err
	}
	return filepath.Join(basePath, statusDir, m.dir, name+".yaml"), nil
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

// getResource reads a definition into obj: the spec from its YAML file, the
// status from the status file muster keeps for it (readStatus). Caller
// allocates obj (matches the controller-runtime client.Get convention).
//
// A status block inside the definition file itself is not read. Muster wrote
// one there before status had its own directory; what such a block says is
// whatever the last write before the upgrade recorded, not the state of the
// running service, and muster does not edit the operator's file to remove it.
func (f *Client) getResource(name string, obj client.Object, m resourceMeta) error {
	filePath, err := m.filePath(f.basePath, name)
	if err != nil {
		return err
	}

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
	return f.readStatus(name, obj, m)
}

// readStatus replaces obj's status with the one recorded in its status file;
// a definition without a status file has the empty status.
func (f *Client) readStatus(name string, obj client.Object, m resourceMeta) error {
	statusOf(obj).SetZero()

	statusPath, err := m.statusPath(f.basePath, name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(statusPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read %s status file %s: %w", m.gr.Resource, statusPath, err)
	}
	// The status document carries the status key alone (statusDocument), so
	// unmarshalling it onto obj sets the status and touches nothing else.
	if err := yaml.Unmarshal(data, obj); err != nil {
		return fmt.Errorf("failed to unmarshal %s status from %s: %w", m.gr.Resource, statusPath, err)
	}
	return nil
}

// statusOf is the Status field of a muster CRD object. Every kind the client
// stores has one (Get and List admit only those kinds); the field is reached
// by name so that the store's read and write paths stay one implementation
// per operation instead of one per kind.
func statusOf(obj client.Object) reflect.Value {
	return reflect.ValueOf(obj).Elem().FieldByName("Status")
}

// statusDocument renders obj's status as the content of its status file: a
// YAML document with the status key alone.
func statusDocument(obj client.Object) ([]byte, error) {
	data, err := yaml.Marshal(map[string]any{"status": statusOf(obj).Interface()})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal status of %s: %w", obj.GetName(), err)
	}
	return data, nil
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

// definitionDocument renders obj as the content of its definition file:
// every key but status, which is muster's and has its own file
// (statusDocument). Rendered through a map so that the status key is absent,
// not an empty object.
func definitionDocument(obj client.Object) ([]byte, error) {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %s: %w", obj.GetName(), err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("failed to render %s: %w", obj.GetName(), err)
	}
	delete(doc, "status")
	data, err = yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("failed to render %s: %w", obj.GetName(), err)
	}
	return data, nil
}

// writeResourceLocked atomically writes obj's definition file
// (definitionDocument). Caller must hold f.mu.
func (f *Client) writeResourceLocked(obj client.Object, m resourceMeta) error {
	if obj.GetNamespace() == "" {
		obj.SetNamespace(defaultNamespace)
	}

	data, err := definitionDocument(obj)
	if err != nil {
		return fmt.Errorf("%s: %w", m.gr.Resource, err)
	}
	filePath, err := m.filePath(f.basePath, obj.GetName())
	if err != nil {
		return err
	}
	if err := atomicWriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s file %s: %w", m.gr.Resource, filePath, err)
	}
	return nil
}

// createResource writes obj to its YAML file. Returns AlreadyExists if the
// file is already present.
func (f *Client) createResource(obj client.Object, m resourceMeta) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	filePath, err := m.filePath(f.basePath, obj.GetName())
	if err != nil {
		return err
	}
	if _, err := os.Stat(filePath); err == nil {
		return errors.NewAlreadyExists(m.gr, obj.GetName())
	}

	dirPath := m.dirPath(f.basePath)
	if err := os.MkdirAll(dirPath, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("failed to create directory %s: %w", dirPath, err)
	}

	return f.writeResourceLocked(obj, m)
}

// updateResource rewrites obj's YAML file. Returns NotFound if the file is
// missing.
func (f *Client) updateResource(obj client.Object, m resourceMeta) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	filePath, err := m.filePath(f.basePath, obj.GetName())
	if err != nil {
		return err
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(m.gr, obj.GetName())
	}

	return f.writeResourceLocked(obj, m)
}

// updateResourceStatus records obj's status in the definition's status file.
// The definition file is not read and not written: the status write cannot
// carry a stale spec over an edit made in the meantime, and it cannot bring
// back a definition that was deleted -- a definition whose file is gone has
// no status to record, and the caller's snapshot of it is answered NotFound.
// An unchanged status is not written again.
func (f *Client) updateResourceStatus(obj client.Object, m resourceMeta) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	name := obj.GetName()
	filePath, err := m.filePath(f.basePath, name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(m.gr, name)
	}

	data, err := statusDocument(obj)
	if err != nil {
		return err
	}
	statusPath, err := m.statusPath(f.basePath, name)
	if err != nil {
		return err
	}
	if current, err := os.ReadFile(statusPath); err == nil && bytes.Equal(current, data) { //nolint:gosec
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(statusPath), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("failed to create directory %s: %w", filepath.Dir(statusPath), err)
	}
	if err := atomicWriteFile(statusPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s status file %s: %w", m.gr.Resource, statusPath, err)
	}
	return nil
}

// deleteResource removes the YAML file and the status recorded for it.
// Returns NotFound if the definition is missing.
func (f *Client) deleteResource(name string, m resourceMeta) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	filePath, err := m.filePath(f.basePath, name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(m.gr, name)
	}
	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete %s file %s: %w", m.gr.Resource, filePath, err)
	}
	statusPath, err := m.statusPath(f.basePath, name)
	if err != nil {
		return err
	}
	if err := os.Remove(statusPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete %s status file %s: %w", m.gr.Resource, statusPath, err)
	}
	return nil
}
