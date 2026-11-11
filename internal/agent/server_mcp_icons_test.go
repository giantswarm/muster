package agent

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/musterlogo"
)

func initializeIcons(t *testing.T, srv *server.MCPServer) []mcp.Icon {
	t.Helper()
	body := fmt.Appendf(nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"clientInfo":{"name":"t","version":"1"},"capabilities":{}}}`,
		mcp.LATEST_LEGACY_PROTOCOL_VERSION,
	)
	raw, err := json.Marshal(srv.HandleMessage(t.Context(), body))
	require.NoError(t, err)
	var resp struct {
		Result mcp.InitializeResult `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	return resp.Result.ServerInfo.Icons
}

func TestAgentMCPServersAdvertiseMusterLogo(t *testing.T) {
	client := &Client{client: &MockMCPGoClient{}, formatters: NewFormatters()}
	ms, err := NewMCPServer(client, NewDevNullLogger(), false)
	require.NoError(t, err)

	pending, err := NewPendingAuthMCPServer(NewDevNullLogger(), nil, "https://muster.example")
	require.NoError(t, err)

	for name, srv := range map[string]*server.MCPServer{
		"mcp server":          ms.mcpServer,
		"pending auth server": pending.GetMCPServer(),
	} {
		icons := initializeIcons(t, srv)
		require.Len(t, icons, 2, name)
		require.Equal(t, mcp.IconThemeLight, icons[0].Theme, name)
		require.Equal(t, mcp.IconThemeDark, icons[1].Theme, name)
		require.Equal(t, musterlogo.Icons(), icons, name)
	}
}
