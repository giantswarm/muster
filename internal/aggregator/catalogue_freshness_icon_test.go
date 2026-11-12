package aggregator

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/servericon"
)

// serverInfoRecoveringClient is a recoveringMockClient that exposes the
// server info of its last handshake, as the streamable-http transports do;
// a test swaps it for the new process's before recovering.
type serverInfoRecoveringClient struct {
	recoveringMockClient
	infoMu sync.Mutex
	info   mcp.Implementation
}

func (m *serverInfoRecoveringClient) ServerImplementation() mcp.Implementation {
	m.infoMu.Lock()
	defer m.infoMu.Unlock()
	return m.info
}

// recoverAs has the client meet a new process that answers the handshake
// with info.
func (m *serverInfoRecoveringClient) recoverAs(info mcp.Implementation) {
	m.infoMu.Lock()
	m.info = info
	m.infoMu.Unlock()
	m.recover()
}

func svgIcon(fill string) mcp.Implementation {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1" fill="` + fill + `"/></svg>`
	return mcp.Implementation{Name: "backend", Icons: []mcp.Icon{{Src: "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))}}}
}

// TestSharedRecovery_RecordsTheNewServerInfo: a shared client whose
// connection met a new process records that process's server info, so a
// changed icon reaches status.icon.
func TestSharedRecovery_RecordsTheNewServerInfo(t *testing.T) {
	const name = "icon-shared-recovery"
	t.Cleanup(func() { servericon.Forget(name) })
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}
	client := &serverInfoRecoveringClient{
		recoveringMockClient: recoveringMockClient{notifMockClient: notifMockClient{tools: []mcp.Tool{oldListIssues}}},
		info:                 mcp.Implementation{Name: "backend"},
	}
	require.NoError(t, a.RegisterServer(context.Background(), ServerRegistration{Name: name}, client))
	drainRegistryUpdate(registry)
	servericon.RecordClient(name, client)
	icon, known := servericon.Lookup(name)
	require.True(t, known)
	require.Empty(t, icon, "the old process declares no icon")

	client.recoverAs(svgIcon("#000"))

	icon, _ = servericon.Lookup(name)
	assert.NotEmpty(t, icon, "the new process's icon is recorded")
}

// TestSessionRecovery_RecordsTheNewServerInfo: a pooled connection that met
// a new process records that process's server info, the icon it changed
// included.
func TestSessionRecovery_RecordsTheNewServerInfo(t *testing.T) {
	t.Cleanup(func() { servericon.Forget("pro") })
	f := newFreshnessFixture(t)
	f.listedBefore(t, "sess-1")
	client := &serverInfoRecoveringClient{
		recoveringMockClient: recoveringMockClient{notifMockClient: notifMockClient{tools: []mcp.Tool{newListIssues}}},
		info:                 svgIcon("#000"),
	}
	f.pool.Put("sess-1", "pro", client)
	servericon.RecordClient("pro", client)
	before, _ := servericon.Lookup("pro")
	require.NotEmpty(t, before)

	client.recoverAs(svgIcon("#fff"))

	after, _ := servericon.Lookup("pro")
	assert.NotEmpty(t, after)
	assert.NotEqual(t, before, after, "the new process's icon replaces the old one")
}
