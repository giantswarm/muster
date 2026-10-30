package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/client/filesystem"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

func TestCreateStoresTags(t *testing.T) {
	ctx := api.WithSubject(t.Context(), "alice")

	t.Run("filesystem", func(t *testing.T) {
		dir := t.TempDir()
		adapter := NewAdapterWithClient(filesystem.New(dir), "default")

		executeOK(t, adapter, ctx, "mcpserver_create", remoteServerArgs(map[string]interface{}{"tags": []interface{}{"triage", "kubernetes"}}))

		assert.Equal(t, map[string]string{
			api.TagsAnnotation:         "triage,kubernetes",
			api.RegisteredByAnnotation: "alice",
		}, readDefinitionAnnotations(t, dir, "kagi-search"))
	})

	t.Run("kubernetes", func(t *testing.T) {
		c := &recordingClient{}
		adapter := NewAdapterWithClient(c, "test-ns")

		executeOK(t, adapter, ctx, "mcpserver_create", remoteServerArgs(map[string]interface{}{"tags": []interface{}{"triage"}}))

		require.Len(t, c.createdObjs, 1)
		assert.Equal(t, map[string]string{
			api.TagsAnnotation:         "triage",
			api.RegisteredByAnnotation: "alice",
		}, c.createdObjs[0].Annotations)
	})

	t.Run("an empty list sets no annotation", func(t *testing.T) {
		c := &recordingClient{}
		adapter := NewAdapterWithClient(c, "test-ns")

		executeOK(t, adapter, t.Context(), "mcpserver_create", remoteServerArgs(map[string]interface{}{"tags": []interface{}{}}))

		require.Len(t, c.createdObjs, 1)
		assert.Empty(t, c.createdObjs[0].Annotations)
	})
}

func TestMutationsRejectInvalidTags(t *testing.T) {
	for _, tool := range []string{"mcpserver_create", "mcpserver_update", "mcpserver_validate"} {
		for _, tags := range [][]interface{}{{"Triage"}, {"triage", "triage"}, {"bad_tag"}} {
			t.Run(tool, func(t *testing.T) {
				c := &recordingClient{stubMusterClient: stubMusterClient{existing: existingServer()}}
				adapter := NewAdapterWithClient(c, "test-ns")

				result, err := adapter.ExecuteTool(t.Context(), tool, remoteServerArgs(map[string]interface{}{"tags": tags}))

				require.NoError(t, err)
				require.True(t, result.IsError)
				assert.Contains(t, resultText(t, result), "invalid tags")
				assert.Empty(t, c.createdObjs)
				assert.Empty(t, c.updatedObjs)
			})
		}
	}
}

func TestUpdateTags(t *testing.T) {
	existingAnnotations := map[string]string{
		api.TagsAnnotation:         "old",
		api.RegisteredByAnnotation: "alice",
	}

	tests := []struct {
		name string
		tags interface{} // nil leaves the argument out
		want map[string]string
	}{
		{
			name: "omitted keeps the tags",
			want: existingAnnotations,
		},
		{
			name: "a list replaces them",
			tags: []interface{}{"triage", "kubernetes"},
			want: map[string]string{api.TagsAnnotation: "triage,kubernetes", api.RegisteredByAnnotation: "alice"},
		},
		{
			name: "an empty list removes them",
			tags: []interface{}{},
			want: map[string]string{api.RegisteredByAnnotation: "alice"},
		},
	}

	for _, tt := range tests {
		args := map[string]interface{}{"name": "kagi-search", "description": "web search"}
		if tt.tags != nil {
			args["tags"] = tt.tags
		}
		existing := func() *musterv1alpha1.MCPServer {
			obj := existingServer()
			obj.Name = "kagi-search"
			obj.Annotations = map[string]string{}
			for k, v := range existingAnnotations {
				obj.Annotations[k] = v
			}
			return obj
		}

		t.Run(tt.name+"/filesystem", func(t *testing.T) {
			dir := t.TempDir()
			fs := filesystem.New(dir)
			require.NoError(t, fs.CreateMCPServer(t.Context(), existing()))
			adapter := NewAdapterWithClient(fs, "default")

			executeOK(t, adapter, t.Context(), "mcpserver_update", args)

			assert.Equal(t, tt.want, readDefinitionAnnotations(t, dir, "kagi-search"))
		})

		t.Run(tt.name+"/kubernetes", func(t *testing.T) {
			c := &recordingClient{stubMusterClient: stubMusterClient{existing: existing()}}
			adapter := NewAdapterWithClient(c, "test-ns")

			executeOK(t, adapter, t.Context(), "mcpserver_update", args)

			require.Len(t, c.updatedObjs, 1)
			assert.Equal(t, tt.want, c.updatedObjs[0].Annotations)
			assert.Equal(t, "web search", c.updatedObjs[0].Spec.Description)
		})
	}
}

func TestGetAndListSurfaceTags(t *testing.T) {
	dir := t.TempDir()
	fs := filesystem.New(dir)
	tagged := existingServer()
	tagged.Name = "kagi-search"
	tagged.Annotations = map[string]string{api.TagsAnnotation: " Triage ,bad_tag,kubernetes,triage"}
	require.NoError(t, fs.CreateMCPServer(t.Context(), tagged))
	untagged := existingServer()
	untagged.Name = "plain"
	require.NoError(t, fs.CreateMCPServer(t.Context(), untagged))
	adapter := NewAdapterWithClient(fs, "default")

	info, err := adapter.GetMCPServer(t.Context(), "kagi-search")
	require.NoError(t, err)
	assert.Equal(t, []string{"triage", "kubernetes"}, info.Tags)

	servers, err := adapter.ListMCPServers(t.Context())
	require.NoError(t, err)
	byName := map[string][]string{}
	for _, s := range servers {
		byName[s.Name] = s.Tags
	}
	assert.Equal(t, map[string][]string{"kagi-search": {"triage", "kubernetes"}, "plain": nil}, byName)
}

func TestMutationToolsDeclareTags(t *testing.T) {
	declared := map[string]bool{}
	for _, tool := range NewAdapterWithClient(&recordingClient{}, "test-ns").GetTools() {
		for _, arg := range tool.Args {
			if arg.Name == "tags" {
				assert.Equal(t, api.ArgTypeArray, arg.Type)
				assert.False(t, arg.Required)
				declared[tool.Name] = true
			}
		}
	}
	assert.Equal(t, map[string]bool{"mcpserver_create": true, "mcpserver_update": true, "mcpserver_validate": true}, declared)
}
