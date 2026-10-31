package reconciler

import (
	"encoding/base64"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/servericon"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// TestApplyStatusFromServiceWritesIcon: the status carries the icon muster
// learned at a handshake, and keeps the stored one until muster learns it.
func TestApplyStatusFromServiceWritesIcon(t *testing.T) {
	const key = "icon-test-server"
	t.Cleanup(func() { servericon.Forget(key) })

	registry := NewMockServiceRegistry()
	registry.AddService(key, &MockServiceInfo{Name: key, ServiceType: api.TypeMCPServer, State: api.StateConnected})
	r := &MCPServerReconciler{serviceRegistry: registry}
	server := &musterv1alpha1.MCPServer{
		Spec:   musterv1alpha1.MCPServerSpec{Type: "streamable-http"},
		Status: musterv1alpha1.MCPServerStatus{Icon: "data:image/png;base64,stored"},
	}

	r.applyStatusFromService(server, key, nil, nil)
	assert.Equal(t, "data:image/png;base64,stored", server.Status.Icon, "nothing learned yet: the stored icon stays")

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
	icon := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svg)
	servericon.Record(key, mcp.Implementation{Icons: []mcp.Icon{{Src: icon}}})
	r.applyStatusFromService(server, key, nil, nil)
	assert.Equal(t, icon, server.Status.Icon)

	servericon.Record(key, mcp.Implementation{})
	r.applyStatusFromService(server, key, nil, nil)
	assert.Empty(t, server.Status.Icon, "the server no longer declares an icon")
}
