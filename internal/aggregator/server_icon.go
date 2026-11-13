package aggregator

import (
	"github.com/mark3labs/mcp-go/mcp"

	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/internal/servericon"
)

// recordHandshakes has a per-session client record the serverInfo of every
// handshake it completes in servericon, under serverName, so status.icon
// follows the process behind the address. A client records only while the
// MCPServer it was created for is the one registered under serverName: one
// that outlives the server's registration, or that of a server deleted and
// created again under the same name, records nothing. Called before the
// client's Initialize.
func (a *AggregatorServer) recordHandshakes(serverName string, client any) {
	observer, ok := client.(internalmcp.HandshakeObserver)
	if !ok {
		return
	}
	info := registeredServerInfo(a, serverName)
	if info == nil {
		return
	}
	uid := info.UID
	observer.OnHandshake(func(impl mcp.Implementation) {
		if current := registeredServerInfo(a, serverName); current != nil && current.UID == uid {
			servericon.Record(serverName, impl)
		}
	})
}
