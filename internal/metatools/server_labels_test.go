package metatools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/v5/internal/api"
)

type labelledServers struct {
	api.MCPServerManagerHandler
	servers []api.MCPServerInfo
}

func (m *labelledServers) ListMCPServers(context.Context) ([]api.MCPServerInfo, error) {
	return m.servers, nil
}

// TestMCPServerLabelsKeysByNamespaceAndName: the label lookup is keyed like
// the registry, so a tenant's server called github never lends its labels to
// the server for everyone of the same name, nor takes them.
func TestMCPServerLabelsKeysByNamespaceAndName(t *testing.T) {
	api.RegisterMCPServerManager(&labelledServers{servers: []api.MCPServerInfo{
		{Name: "github", Namespace: "agent-platform", Key: "github", Labels: map[string]string{"tool-group": "platform"}},
		{Name: "github", Namespace: "org-a", Key: "org-a/github", Labels: map[string]string{"tool-group": "sales"}},
		{Name: "local", Labels: map[string]string{"tool-group": "local"}},
	}})
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	labels := MCPServerLabels(context.Background())
	assert.Equal(t, "platform", labels("github")["tool-group"])
	assert.Equal(t, "sales", labels("org-a/github")["tool-group"])
	assert.Equal(t, "local", labels("local")["tool-group"], "a server without a key is keyed by its name")
}
