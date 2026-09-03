package mcpserver

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/mcpserver"
	"github.com/giantswarm/muster/internal/services"
)

// startAnonymousMCPServer serves a real MCP endpoint that accepts an
// initialize without any Authorization header — the shape of a backend that
// has not (yet) been rolled to an OAuth-protected release.
func startAnonymousMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	mcpServer := server.NewMCPServer("anonymous-backend", "1.0.0")
	ts := httptest.NewServer(server.NewStreamableHTTPServer(mcpServer, server.WithStateful(true)))
	t.Cleanup(ts.Close)
	return ts
}

// TestStartSessionAuthServerAcceptingAnonymousProbe is the regression test for
// issue #1135. The MCPServer is configured for session-level auth, but the
// backend still answers an anonymous initialize (the old pod during a rollover
// to an OAuth-protected release). The service must not treat that as a
// connected, globally usable server: the token-less probe client is discarded
// and the server settles in Auth Required, exactly as after a 401, so the
// aggregator registers it pending auth and every session connects with the
// caller's own token.
func TestStartSessionAuthServerAcceptingAnonymousProbe(t *testing.T) {
	sessionAuthConfigs := map[string]*api.MCPServerAuth{
		"forwardToken": {
			Type:         "oauth",
			ForwardToken: true,
		},
		"tokenExchange": {
			Type: "oauth",
			TokenExchange: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.example.com/token",
				ConnectorID:      "muster",
			},
		},
	}

	for name, auth := range sessionAuthConfigs {
		t.Run(name, func(t *testing.T) {
			ts := startAnonymousMCPServer(t)

			var authRequiredPublished atomic.Bool
			var hookRanBeforePublish atomic.Bool
			var hookDefinition atomic.Pointer[api.MCPServer]
			var hookErr atomic.Pointer[mcpserver.AuthRequiredError]

			def := &api.MCPServer{
				Name: "rolling-backend",
				Type: api.MCPServerTypeStreamableHTTP,
				URL:  ts.URL + "/mcp",
				Auth: auth,
			}
			svc, err := NewService(def, WithAuthRequiredHook(func(definition *api.MCPServer, authErr *mcpserver.AuthRequiredError) {
				hookDefinition.Store(definition)
				hookErr.Store(authErr)
				hookRanBeforePublish.Store(!authRequiredPublished.Load())
			}))
			require.NoError(t, err)
			// Registered after the server's Close, so it runs first: without the
			// fix the service keeps a client whose long-lived GET stream would
			// make httptest.Server.Close block forever instead of failing.
			t.Cleanup(func() { _ = svc.Stop(context.Background()) })

			var states []services.ServiceState
			svc.SetStateChangeCallback(func(_ string, _, newState services.ServiceState, _ services.HealthStatus, _ error) {
				states = append(states, newState)
				if newState == services.StateAuthRequired {
					authRequiredPublished.Store(true)
				}
			})

			err = svc.Start(t.Context())

			var authErr *mcpserver.AuthRequiredError
			require.ErrorAs(t, err, &authErr, "an accepted anonymous probe must still end in Auth Required for a session-auth server")
			assert.True(t, api.IsAuthRequiredError(err), "the reconciler must classify the result as Auth Required, not as a failed start")
			assert.Equal(t, def.URL, authErr.URL)
			assert.False(t, authErr.HasValidChallenge(), "no challenge was received; per-session connections need none")

			assert.Equal(t, services.StateAuthRequired, svc.GetState(), "CR state must read Auth Required, not Connected")
			assert.NotContains(t, states, services.StateConnected, "the server must never pass through Connected")
			assert.Nil(t, svc.GetMCPClient(), "the token-less probe client must be closed, not kept as the shared client")
			assert.False(t, svc.IsClientReady())
			assert.NotContains(t, svc.GetServiceData(), "client", "service data must not offer a client for global registration")

			require.NotNil(t, hookDefinition.Load(), "the auth-required hook must run so the aggregator gets a pending-auth entry")
			assert.Same(t, def, hookDefinition.Load())
			assert.Same(t, authErr, hookErr.Load())
			assert.True(t, hookRanBeforePublish.Load(), "hook must run before the StateAuthRequired state change is published")

			assert.Equal(t, 0, svc.GetConsecutiveFailures(), "a reachable backend is not a connectivity failure")
		})
	}
}

// TestStartAnonymousBackendWithoutSessionAuthConnects pins the control case:
// the same anonymous backend, reached by a server without session-level auth,
// is connected and keeps its client for global registration. The decision is
// driven by the configuration, not by the backend's answer.
func TestStartAnonymousBackendWithoutSessionAuthConnects(t *testing.T) {
	for name, auth := range map[string]*api.MCPServerAuth{
		"no auth block": nil,
		"oauth without forwarding": {
			Type: "oauth",
		},
		"token exchange disabled": {
			Type:          "oauth",
			TokenExchange: &api.TokenExchangeConfig{Enabled: false},
		},
	} {
		t.Run(name, func(t *testing.T) {
			ts := startAnonymousMCPServer(t)

			var hookCalled atomic.Bool
			svc, err := NewService(&api.MCPServer{
				Name: "plain-backend",
				Type: api.MCPServerTypeStreamableHTTP,
				URL:  ts.URL + "/mcp",
				Auth: auth,
			}, WithAuthRequiredHook(func(*api.MCPServer, *mcpserver.AuthRequiredError) {
				hookCalled.Store(true)
			}))
			require.NoError(t, err)

			require.NoError(t, svc.Start(t.Context()))
			t.Cleanup(func() { _ = svc.Stop(t.Context()) })

			assert.Equal(t, services.StateConnected, svc.GetState())
			assert.True(t, svc.IsClientReady())
			assert.Contains(t, svc.GetServiceData(), "client")
			assert.False(t, hookCalled.Load())
		})
	}
}

// TestStartSessionAuthServerAfterConfigurationUpdate covers the reconciler's
// path: the service was created without auth and connected globally; the
// definition then gains auth.forwardToken and the service is restarted while
// the backend still accepts anonymous requests. The restart must land in Auth
// Required with the probe client discarded.
func TestStartSessionAuthServerAfterConfigurationUpdate(t *testing.T) {
	ts := startAnonymousMCPServer(t)

	var hookCalls atomic.Int32
	svc, err := NewService(&api.MCPServer{
		Name: "flipped-backend",
		Type: api.MCPServerTypeStreamableHTTP,
		URL:  ts.URL + "/mcp",
	}, WithAuthRequiredHook(func(*api.MCPServer, *mcpserver.AuthRequiredError) {
		hookCalls.Add(1)
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	require.NoError(t, svc.Start(t.Context()))
	require.Equal(t, services.StateConnected, svc.GetState())
	require.True(t, svc.IsClientReady())

	require.NoError(t, svc.UpdateConfiguration(&api.MCPServer{
		Name: "flipped-backend",
		Type: api.MCPServerTypeStreamableHTTP,
		URL:  ts.URL + "/mcp",
		Auth: &api.MCPServerAuth{Type: "oauth", ForwardToken: true, RequiredAudiences: []string{"kubernetes"}},
	}))

	err = svc.Restart(t.Context())
	var authErr *mcpserver.AuthRequiredError
	require.ErrorAs(t, err, &authErr)

	assert.Equal(t, services.StateAuthRequired, svc.GetState())
	assert.Nil(t, svc.GetMCPClient())
	assert.Equal(t, int32(1), hookCalls.Load(), "the restart with the new definition must register the server pending auth")
}
