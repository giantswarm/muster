package reconciler

import (
	"context"
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

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`)
	icon := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svg)
	servericon.Record(key, mcp.Implementation{Icons: []mcp.Icon{{Src: icon}}})
	r.applyStatusFromService(server, key, nil, nil)
	assert.Equal(t, icon, server.Status.Icon)

	servericon.Record(key, mcp.Implementation{})
	r.applyStatusFromService(server, key, nil, nil)
	assert.Empty(t, server.Status.Icon, "the server no longer declares an icon")
}

// TestApplyIconBrandFallback: without an icon of its own a server gets the
// built-in icon of its URL's brand domain, and the server's own icon wins.
func TestApplyIconBrandFallback(t *testing.T) {
	const key = "icon-brand-server"
	t.Cleanup(func() { servericon.Forget(key) })

	atlassian := servericon.BrandIcon("https://mcp.atlassian.com/v1/sse")
	github := servericon.BrandIcon("https://api.githubcopilot.com/mcp/")
	assert.NotEmpty(t, atlassian)
	assert.NotEmpty(t, github)

	server := &musterv1alpha1.MCPServer{Spec: musterv1alpha1.MCPServerSpec{URL: "https://mcp.atlassian.com/v1/sse"}}
	applyIcon(server, key)
	assert.Equal(t, atlassian, server.Status.Icon, "never connected: the brand icon")

	server.Spec.URL = "https://api.githubcopilot.com/mcp/"
	applyIcon(server, key)
	assert.Equal(t, github, server.Status.Icon, "a URL change updates the brand icon")

	server.Status.Icon = "data:image/png;base64,stored"
	applyIcon(server, key)
	assert.Equal(t, "data:image/png;base64,stored", server.Status.Icon, "a stored server icon persists until muster learns one")

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`)
	icon := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svg)
	servericon.Record(key, mcp.Implementation{Icons: []mcp.Icon{{Src: icon}}})
	applyIcon(server, key)
	assert.Equal(t, icon, server.Status.Icon, "the server's own icon wins over the brand icon")

	servericon.Record(key, mcp.Implementation{})
	applyIcon(server, key)
	assert.Equal(t, github, server.Status.Icon, "a server without a usable icon gets the brand icon")
}

// TestSuspendedServerGetsBrandIcon: a suspended server never connects, and
// its status still carries the brand icon.
func TestSuspendedServerGetsBrandIcon(t *testing.T) {
	mgr := NewMockMCPServerManager()
	statusUpdater := NewMockStatusUpdater()
	reconciler := NewMCPServerReconciler(NewMockOrchestratorAPI(), mgr, NewMockServiceRegistry()).
		WithStatusUpdater(statusUpdater, "default")

	const url = "https://mcp.pagerduty.com/mcp"
	mgr.AddMCPServer(&api.MCPServerInfo{Name: "test-server", Type: "streamable-http", URL: url, Suspended: true})
	stored := &musterv1alpha1.MCPServer{Spec: musterv1alpha1.MCPServerSpec{Type: "streamable-http", URL: url, Suspended: true}}
	stored.Name, stored.Namespace = "test-server", "default"
	statusUpdater.MCPServers["default/test-server"] = stored

	result := reconciler.Reconcile(context.Background(), lifecycleReconcileRequest())

	assert.NoError(t, result.Error)
	if assert.NotNil(t, statusUpdater.LastUpdatedMCPServer) {
		assert.Equal(t, servericon.BrandIcon(url), statusUpdater.LastUpdatedMCPServer.Status.Icon)
		assert.NotEmpty(t, statusUpdater.LastUpdatedMCPServer.Status.Icon)
	}
}
