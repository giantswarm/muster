package toolset

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFromScope(t *testing.T) {
	require.Equal(t, []string{"preset:read-only", "tool:x_k8s_get"}, FromScope([]string{"openid", "email", "preset:read-only", "tool:x_k8s_get"}))
	require.Equal(t, []string{"toolset:shared", "label:a"}, FromScope([]string{"toolset:shared", "label:a", "audience:server:client_id:x"}))
	require.Empty(t, FromScope([]string{"openid", "email", "groups"}))
	require.Empty(t, FromScope(nil))
}
