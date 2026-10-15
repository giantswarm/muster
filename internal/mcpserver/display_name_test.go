package mcpserver

import (
	"context"
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

// recordingClient is a Kubernetes-mode client that keeps the objects the
// adapter writes.
type recordingClient struct {
	stubMusterClient
	createdObjs []*musterv1alpha1.MCPServer
	updatedObjs []*musterv1alpha1.MCPServer
}

func (r *recordingClient) CreateMCPServer(_ context.Context, obj *musterv1alpha1.MCPServer) error {
	r.createdObjs = append(r.createdObjs, obj.DeepCopy())
	return nil
}

func (r *recordingClient) UpdateMCPServer(_ context.Context, obj *musterv1alpha1.MCPServer) error {
	r.updatedObjs = append(r.updatedObjs, obj.DeepCopy())
	return nil
}

func remoteServerArgs(extra map[string]interface{}) map[string]interface{} {
	args := map[string]interface{}{"name": "kagi-search", "type": "streamable-http", "url": "http://example.com/mcp"}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

func executeOK(t *testing.T, adapter *Adapter, ctx context.Context, tool string, args map[string]interface{}) {
	t.Helper()
	result, err := adapter.ExecuteTool(ctx, tool, args)
	require.NoError(t, err)
	require.False(t, result.IsError, resultText(t, result))
}

// readDefinitionAnnotations reads the annotations of a server's definition
// file as it is on disk.
func readDefinitionAnnotations(t *testing.T, dir, name string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "mcpservers", name+".yaml"))
	require.NoError(t, err)
	var obj musterv1alpha1.MCPServer
	require.NoError(t, yaml.Unmarshal(data, &obj))
	return obj.Annotations
}

func TestCreateMergesDisplayNameWithRegisteredBy(t *testing.T) {
	ctx := api.WithSubject(context.Background(), "alice")

	t.Run("filesystem", func(t *testing.T) {
		dir := t.TempDir()
		adapter := NewAdapterWithClient(filesystem.New(dir), "default")

		executeOK(t, adapter, ctx, "mcpserver_create", remoteServerArgs(map[string]interface{}{"displayName": "Kagi Search"}))

		assert.Equal(t, map[string]string{
			api.DisplayNameAnnotation:  "Kagi Search",
			api.RegisteredByAnnotation: "alice",
		}, readDefinitionAnnotations(t, dir, "kagi-search"))
	})

	t.Run("kubernetes", func(t *testing.T) {
		c := &recordingClient{}
		adapter := NewAdapterWithClient(c, "test-ns")

		executeOK(t, adapter, ctx, "mcpserver_create", remoteServerArgs(map[string]interface{}{"displayName": "Kagi Search"}))

		require.Len(t, c.createdObjs, 1)
		assert.Equal(t, map[string]string{
			api.DisplayNameAnnotation:  "Kagi Search",
			api.RegisteredByAnnotation: "alice",
		}, c.createdObjs[0].Annotations)
	})
}

func TestCreateTrimsDisplayName(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		want        map[string]string
	}{
		{name: "empty sets no annotation", displayName: ""},
		{name: "blank sets no annotation", displayName: " \t "},
		{name: "a padded value is stored trimmed", displayName: "  Kagi Search\n", want: map[string]string{api.DisplayNameAnnotation: "Kagi Search"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &recordingClient{}
			adapter := NewAdapterWithClient(c, "test-ns")

			executeOK(t, adapter, context.Background(), "mcpserver_create", remoteServerArgs(map[string]interface{}{"displayName": tt.displayName}))

			require.Len(t, c.createdObjs, 1)
			if tt.want == nil {
				assert.Empty(t, c.createdObjs[0].Annotations)
			} else {
				assert.Equal(t, tt.want, c.createdObjs[0].Annotations)
			}
		})
	}
}

func TestUpdateDisplayName(t *testing.T) {
	existingAnnotations := map[string]string{
		api.DisplayNameAnnotation:  "Kagi",
		api.RegisteredByAnnotation: "alice",
	}

	tests := []struct {
		name        string
		existing    map[string]string
		displayName interface{} // nil leaves the argument out
		want        map[string]string
	}{
		{
			name:     "omitted keeps the annotation",
			existing: existingAnnotations,
			want:     existingAnnotations,
		},
		{
			name:        "a value sets it",
			existing:    existingAnnotations,
			displayName: "Kagi Search",
			want:        map[string]string{api.DisplayNameAnnotation: "Kagi Search", api.RegisteredByAnnotation: "alice"},
		},
		{
			name:        "a value sets it on a server without annotations",
			displayName: "Kagi Search",
			want:        map[string]string{api.DisplayNameAnnotation: "Kagi Search"},
		},
		{
			name:        "an empty string removes it",
			existing:    existingAnnotations,
			displayName: "",
			want:        map[string]string{api.RegisteredByAnnotation: "alice"},
		},
		{
			name:        "a blank value removes it",
			existing:    existingAnnotations,
			displayName: "  ",
			want:        map[string]string{api.RegisteredByAnnotation: "alice"},
		},
		{
			name:        "a padded value is stored trimmed",
			existing:    existingAnnotations,
			displayName: " Kagi Search ",
			want:        map[string]string{api.DisplayNameAnnotation: "Kagi Search", api.RegisteredByAnnotation: "alice"},
		},
	}

	for _, tt := range tests {
		args := map[string]interface{}{"name": "kagi-search", "description": "web search"}
		if tt.displayName != nil {
			args["displayName"] = tt.displayName
		}
		existing := func() *musterv1alpha1.MCPServer {
			obj := existingServer()
			obj.Name = "kagi-search"
			for k, v := range tt.existing {
				if obj.Annotations == nil {
					obj.Annotations = map[string]string{}
				}
				obj.Annotations[k] = v
			}
			return obj
		}

		t.Run(tt.name+"/filesystem", func(t *testing.T) {
			dir := t.TempDir()
			fs := filesystem.New(dir)
			require.NoError(t, fs.CreateMCPServer(context.Background(), existing()))
			adapter := NewAdapterWithClient(fs, "default")

			executeOK(t, adapter, context.Background(), "mcpserver_update", args)

			got := readDefinitionAnnotations(t, dir, "kagi-search")
			if len(tt.want) == 0 {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tt.want, got)
			}
		})

		t.Run(tt.name+"/kubernetes", func(t *testing.T) {
			c := &recordingClient{stubMusterClient: stubMusterClient{existing: existing()}}
			adapter := NewAdapterWithClient(c, "test-ns")

			executeOK(t, adapter, context.Background(), "mcpserver_update", args)

			require.Len(t, c.updatedObjs, 1)
			assert.Equal(t, tt.want, c.updatedObjs[0].Annotations)
			assert.Equal(t, "web search", c.updatedObjs[0].Spec.Description)
		})
	}
}

func TestGetAndListSurfaceDisplayName(t *testing.T) {
	dir := t.TempDir()
	adapter := NewAdapterWithClient(filesystem.New(dir), "default")
	executeOK(t, adapter, context.Background(), "mcpserver_create", remoteServerArgs(map[string]interface{}{"displayName": "Kagi Search"}))

	info, err := adapter.GetMCPServer(context.Background(), "kagi-search")
	require.NoError(t, err)
	assert.Equal(t, "Kagi Search", info.DisplayName)

	servers, err := adapter.ListMCPServers(context.Background())
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, "Kagi Search", servers[0].DisplayName)

	data, err := yaml.Marshal(info)
	require.NoError(t, err)
	assert.Contains(t, string(data), "displayName: Kagi Search")
}

func TestValidateAcceptsDisplayName(t *testing.T) {
	adapter := NewAdapterWithClient(&recordingClient{}, "test-ns")
	for _, displayName := range []string{"Kagi Search", "  "} {
		executeOK(t, adapter, context.Background(), "mcpserver_validate", remoteServerArgs(map[string]interface{}{"displayName": displayName}))
	}
}

func TestMutationToolsDeclareDisplayName(t *testing.T) {
	declared := map[string]bool{}
	for _, tool := range NewAdapterWithClient(&recordingClient{}, "test-ns").GetTools() {
		for _, arg := range tool.Args {
			if arg.Name == "displayName" {
				assert.Equal(t, api.ArgTypeString, arg.Type)
				assert.False(t, arg.Required)
				declared[tool.Name] = true
			}
		}
	}
	assert.Equal(t, map[string]bool{"mcpserver_create": true, "mcpserver_update": true, "mcpserver_validate": true}, declared)
}
