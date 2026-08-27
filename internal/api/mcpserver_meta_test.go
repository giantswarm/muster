package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateMetaAllowed(t *testing.T) {
	meta := map[string]string{"AWS_REGION": "eu-central-1"}

	tests := []struct {
		name       string
		serverType MCPServerType
		meta       map[string]string
		wantError  string
	}{
		{
			name:       "meta on streamable-http",
			serverType: MCPServerTypeStreamableHTTP,
			meta:       meta,
		},
		{
			name:       "meta on sse",
			serverType: MCPServerTypeSSE,
			meta:       meta,
		},
		{
			// A stdio server speaks over a pipe, so no HTTP transport can
			// inject the entries. Rejected rather than dropped, because a
			// dropped entry fails nowhere.
			name:       "meta on stdio",
			serverType: MCPServerTypeStdio,
			meta:       meta,
			wantError:  "meta is only allowed",
		},
		{
			name:       "no meta on stdio",
			serverType: MCPServerTypeStdio,
		},
		{
			name:       "an empty map on stdio",
			serverType: MCPServerTypeStdio,
			meta:       map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMetaAllowed(string(tt.serverType), tt.meta)
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}
