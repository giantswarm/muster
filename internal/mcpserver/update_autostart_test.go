package mcpserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/client/filesystem"
)

func TestUpdateAutoStart(t *testing.T) {
	tests := []struct {
		name   string
		update map[string]interface{}
		want   bool
	}{
		{
			name:   "omitted keeps it",
			update: map[string]interface{}{"displayName": "Kagi Search"},
			want:   true,
		},
		{
			name:   "explicit false sets it",
			update: map[string]interface{}{"autoStart": false},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/filesystem", func(t *testing.T) {
			adapter := NewAdapterWithClient(filesystem.New(t.TempDir()), "default")
			executeOK(t, adapter, context.Background(), "mcpserver_create", remoteServerArgs(map[string]interface{}{"autoStart": true}))

			update := map[string]interface{}{"name": "kagi-search"}
			for k, v := range tt.update {
				update[k] = v
			}
			executeOK(t, adapter, context.Background(), "mcpserver_update", update)

			info, err := adapter.GetMCPServer(context.Background(), "kagi-search")
			require.NoError(t, err)
			assert.Equal(t, tt.want, info.AutoStart)
		})

		t.Run(tt.name+"/kubernetes", func(t *testing.T) {
			existing := existingServer()
			existing.Name = "kagi-search"
			existing.Spec.AutoStart = true
			c := &recordingClient{stubMusterClient: stubMusterClient{existing: existing}}
			adapter := NewAdapterWithClient(c, "test-ns")

			update := map[string]interface{}{"name": "kagi-search"}
			for k, v := range tt.update {
				update[k] = v
			}
			executeOK(t, adapter, context.Background(), "mcpserver_update", update)

			require.Len(t, c.updatedObjs, 1)
			assert.Equal(t, tt.want, c.updatedObjs[0].Spec.AutoStart)
		})
	}
}
