package workflow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/client/filesystem"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

func newFilesystemAdapter(t *testing.T) (*Adapter, string) {
	t.Helper()
	dir := t.TempDir()
	adapter := NewAdapterWithClient(filesystem.New(dir), "default", nil, nil, dir)
	t.Cleanup(adapter.stopGC)
	return adapter, dir
}

func triageArgs(extra map[string]interface{}) map[string]interface{} {
	args := map[string]interface{}{
		"name":  "triage",
		"steps": []interface{}{map[string]interface{}{"id": "pods", "tool": "x_kubernetes_list"}},
	}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

func executeOK(t *testing.T, adapter *Adapter, tool string, args map[string]interface{}) *api.CallToolResult {
	t.Helper()
	result, err := adapter.ExecuteTool(t.Context(), tool, args)
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	return result
}

// readWorkflowAnnotations reads the annotations of a workflow's definition
// file as it is on disk.
func readWorkflowAnnotations(t *testing.T, dir, name string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "workflows", name+".yaml"))
	require.NoError(t, err)
	var obj musterv1alpha1.Workflow
	require.NoError(t, yaml.Unmarshal(data, &obj))
	return obj.Annotations
}

func TestCreateStoresTags(t *testing.T) {
	adapter, dir := newFilesystemAdapter(t)

	executeOK(t, adapter, "workflow_create", triageArgs(map[string]interface{}{"tags": []interface{}{"triage", "kubernetes"}}))

	assert.Equal(t, map[string]string{api.TagsAnnotation: "triage,kubernetes"}, readWorkflowAnnotations(t, dir, "triage"))
}

func TestCreateWithEmptyTagsSetsNoAnnotation(t *testing.T) {
	adapter, dir := newFilesystemAdapter(t)

	executeOK(t, adapter, "workflow_create", triageArgs(map[string]interface{}{"tags": []interface{}{}}))

	assert.Empty(t, readWorkflowAnnotations(t, dir, "triage"))
}

func TestMutationsRejectInvalidTags(t *testing.T) {
	for _, tool := range []string{"workflow_create", "workflow_update", "workflow_validate"} {
		for _, tags := range []interface{}{
			[]interface{}{"Triage"},
			[]interface{}{"triage", "triage"},
			[]interface{}{"bad_tag"},
			[]interface{}{42},
			"triage",
		} {
			t.Run(tool, func(t *testing.T) {
				adapter, dir := newFilesystemAdapter(t)
				if tool == "workflow_update" {
					executeOK(t, adapter, "workflow_create", triageArgs(map[string]interface{}{"tags": []interface{}{"old"}}))
				}

				result, err := adapter.ExecuteTool(t.Context(), tool, triageArgs(map[string]interface{}{"tags": tags}))

				require.NoError(t, err, "bad tags are a refused call, not a protocol error")
				require.True(t, result.IsError)
				assert.Contains(t, result.Content[0], "tags")
				if tool == "workflow_update" {
					assert.Equal(t, map[string]string{api.TagsAnnotation: "old"}, readWorkflowAnnotations(t, dir, "triage"))
				} else {
					_, statErr := os.Stat(filepath.Join(dir, "workflows", "triage.yaml"))
					assert.True(t, os.IsNotExist(statErr), "nothing is written")
				}
			})
		}
	}
}

func TestUpdateTags(t *testing.T) {
	tests := []struct {
		name string
		tags interface{} // nil leaves the argument out
		want map[string]string
	}{
		{name: "omitted keeps the tags", want: map[string]string{api.TagsAnnotation: "old"}},
		{name: "a list replaces them", tags: []interface{}{"triage", "kubernetes"}, want: map[string]string{api.TagsAnnotation: "triage,kubernetes"}},
		{name: "an empty list removes them", tags: []interface{}{}, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, dir := newFilesystemAdapter(t)
			executeOK(t, adapter, "workflow_create", triageArgs(map[string]interface{}{"tags": []interface{}{"old"}}))

			extra := map[string]interface{}{"description": "updated"}
			if tt.tags != nil {
				extra["tags"] = tt.tags
			}
			executeOK(t, adapter, "workflow_update", triageArgs(extra))

			got := readWorkflowAnnotations(t, dir, "triage")
			if tt.want == nil {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tt.want, got)
			}
			wf, err := adapter.GetWorkflow("triage")
			require.NoError(t, err)
			assert.Equal(t, "updated", wf.Description, "the spec update still applies")
		})
	}
}

func TestGetAndListSurfaceTags(t *testing.T) {
	adapter, dir := newFilesystemAdapter(t)
	fs := filesystem.New(dir)
	tagged := &musterv1alpha1.Workflow{}
	tagged.Name = "triage"
	tagged.Annotations = map[string]string{api.TagsAnnotation: " Triage ,bad_tag,kubernetes,triage"}
	tagged.Spec.Steps = []musterv1alpha1.WorkflowStep{{ID: "pods", Tool: "x_kubernetes_list"}}
	require.NoError(t, fs.CreateWorkflow(t.Context(), tagged))
	untagged := tagged.DeepCopy()
	untagged.Name = "plain"
	untagged.Annotations = nil
	require.NoError(t, fs.CreateWorkflow(t.Context(), untagged))

	get := executeOK(t, adapter, "workflow_get", map[string]interface{}{"name": "triage"})
	got := get.Content[0].(map[string]interface{})
	assert.Equal(t, []string{"triage", "kubernetes"}, got["workflow"].(*api.Workflow).Tags)
	assert.Contains(t, got["yaml"], "tags:\n    - triage\n    - kubernetes\n")

	list := executeOK(t, adapter, "workflow_list", map[string]interface{}{})
	byName := map[string]interface{}{}
	for _, wf := range list.Content[0].(map[string]interface{})["workflows"].([]map[string]interface{}) {
		byName[wf["name"].(string)] = wf["tags"]
	}
	assert.Equal(t, map[string]interface{}{"triage": []string{"triage", "kubernetes"}, "plain": nil}, byName)
}

func TestMutationToolsDeclareTags(t *testing.T) {
	adapter, _ := newFilesystemAdapter(t)
	declared := map[string]bool{}
	for _, tool := range adapter.GetTools() {
		for _, arg := range tool.Args {
			if arg.Name == "tags" {
				assert.Equal(t, api.ArgTypeArray, arg.Type)
				assert.False(t, arg.Required)
				declared[tool.Name] = true
			}
		}
	}
	assert.Equal(t, map[string]bool{"workflow_create": true, "workflow_update": true, "workflow_validate": true}, declared)
}
