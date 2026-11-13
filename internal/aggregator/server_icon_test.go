package aggregator

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/servericon"
)

// handshakingMockClient is a notifMockClient that reports handshakes to its
// OnHandshake handler, as the clients of internal/mcpserver do.
type handshakingMockClient struct {
	notifMockClient
	handshake func(mcp.Implementation)
}

func (m *handshakingMockClient) OnHandshake(handler func(mcp.Implementation)) {
	m.handshake = handler
}

func svgServerInfo(fill string) mcp.Implementation {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1" fill="` + fill + `"/></svg>`
	return mcp.Implementation{Name: "backend", Icons: []mcp.Icon{{Src: "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))}}}
}

func registerWithUID(t *testing.T, registry *ServerRegistry, name, uid string) {
	t.Helper()
	require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: name, UID: uid},
		URL:                "http://" + name + "/mcp",
		AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
	}))
}

// TestRecordHandshakes_FollowsEveryHandshakeOfTheRegisteredServer: each
// handshake of a per-session client, a recovered session's included,
// records the serverInfo of the process it met.
func TestRecordHandshakes_FollowsEveryHandshakeOfTheRegisteredServer(t *testing.T) {
	const name = "icon-handshakes"
	t.Cleanup(func() { servericon.Forget(name) })
	registry := NewServerRegistry("x")
	registerWithUID(t, registry, name, "uid-1")
	a := &AggregatorServer{registry: registry}
	client := &handshakingMockClient{}

	a.recordHandshakes(name, client)
	require.NotNil(t, client.handshake)

	client.handshake(mcp.Implementation{Name: "backend"})
	icon, known := servericon.Lookup(name)
	require.True(t, known)
	assert.Empty(t, icon, "the first process declares no icon")

	client.handshake(svgServerInfo("#000"))
	first, _ := servericon.Lookup(name)
	assert.NotEmpty(t, first, "the new process's icon is recorded")

	client.handshake(svgServerInfo("#fff"))
	second, _ := servericon.Lookup(name)
	assert.NotEqual(t, first, second, "a changed icon replaces the old one")
}

// TestRecordHandshakes_StopsWithTheServer: a connection that outlives its
// server's registration records nothing, nor after a server of the same
// name was created again.
func TestRecordHandshakes_StopsWithTheServer(t *testing.T) {
	const name = "icon-handshakes-removed"
	t.Cleanup(func() { servericon.Forget(name) })
	registry := NewServerRegistry("x")
	registerWithUID(t, registry, name, "uid-1")
	a := &AggregatorServer{registry: registry}
	client := &handshakingMockClient{}
	a.recordHandshakes(name, client)

	require.NoError(t, registry.Deregister(name))
	client.handshake(svgServerInfo("#000"))
	_, known := servericon.Lookup(name)
	assert.False(t, known, "a deregistered server records nothing")

	registerWithUID(t, registry, name, "uid-2")
	client.handshake(svgServerInfo("#000"))
	_, known = servericon.Lookup(name)
	assert.False(t, known, "a server created again under the name is not the one the client connects for")

	unregistered := &handshakingMockClient{}
	a.recordHandshakes("icon-never-registered", unregistered)
	assert.Nil(t, unregistered.handshake, "a client of a server never registered records nothing")
}

// TestRegisterServer_FailedRegistrationDropsTheRecoveryObserver: a shared
// client whose registration failed is not observed.
func TestRegisterServer_FailedRegistrationDropsTheRecoveryObserver(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}
	perSessionServer(t, registry, "srv")
	client := &recoveringMockClient{notifMockClient: notifMockClient{tools: []mcp.Tool{oldListIssues}}}

	require.Error(t, a.RegisterServer(context.Background(), ServerRegistration{Name: "srv"}, client), "the name is taken")

	client.recoveredMu.Lock()
	defer client.recoveredMu.Unlock()
	assert.Nil(t, client.recovered)
}
