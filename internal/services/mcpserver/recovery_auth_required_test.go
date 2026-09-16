package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/events"
	"github.com/giantswarm/muster/v5/internal/services"
)

// startHTTPStatusServer answers every request with the given status. A 401
// carries a bare Bearer challenge, the shape of an OAuth resource server that
// has not seen a token.
func startHTTPStatusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// recordedRestart runs Restart against a server answering with status and
// returns the service, the events it emitted and the Restart error.
func recordedRestart(t *testing.T, auth *api.MCPServerAuth, status int) (*Service, *countingEventManager, error) {
	t.Helper()
	cm := &countingEventManager{counts: map[string]int{}}
	api.RegisterEventManager(cm)
	t.Cleanup(func() { api.RegisterEventManager(nil) })

	ts := startHTTPStatusServer(t, status)
	svc, err := NewService(&api.MCPServer{
		Name: "recovering-backend",
		Type: api.MCPServerTypeStreamableHTTP,
		URL:  ts.URL + "/mcp",
		Auth: auth,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	err = svc.Restart(t.Context())
	return svc, cm, err
}

// TestRestartAuthRequiredIsNotARecoveryFailure is the regression test for issue
// #1265. Automatic recovery restarts a server whose callers bring their own
// credentials; the server is up now and answers the token-less probe with 401,
// exactly as configured. That is the recovery's outcome -- the server waits in
// Auth Required for a signed-in caller -- and must not be reported as a failed
// recovery: operators and alert rules key on Warning events, and every
// platform install emitted one for each OAuth-protected server on start-up.
func TestRestartAuthRequiredIsNotARecoveryFailure(t *testing.T) {
	for name, auth := range map[string]*api.MCPServerAuth{
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
		// No auth block: a 401 starts OAuth discovery and the server waits
		// for a user to complete core_auth_login (per-server sign-in).
		"oauth login through muster": nil,
	} {
		t.Run(name, func(t *testing.T) {
			svc, cm, err := recordedRestart(t, auth, http.StatusUnauthorized)

			require.Error(t, err)
			assert.True(t, api.IsAuthRequiredError(err), "callers still learn that the server awaits a sign-in")
			assert.Equal(t, services.StateAuthRequired, svc.GetState())

			assert.Equal(t, 0, cm.count(string(events.ReasonMCPServerRecoveryFailed)), "a 401 the server is configured to answer is not a failed recovery")
			assert.Equal(t, 1, cm.count(string(events.ReasonMCPServerRecoveryAwaitingAuth)), "recovery must say how it ended")
			assert.Equal(t, 1, cm.count(string(events.ReasonMCPServerRecoveryStarted)))
			assert.Equal(t, 1, cm.count(string(events.ReasonMCPServerAuthRequired)))
			assert.Equal(t, 0, cm.count(string(events.ReasonMCPServerRecoverySucceeded)), "the server is not connected; it waits for a caller")
			assert.Equal(t, 0, cm.count(string(events.ReasonMCPServerFailed)))
		})
	}
}

// TestRestartGenuineFailuresStayRecoveryFailed pins the control cases: a
// server that answers 5xx, one that refuses the connection, and a 401 from a
// machine identity -- there is no user to sign in, so the credential or the
// role is wrong -- are recovery failures and keep the Warning.
func TestRestartGenuineFailuresStayRecoveryFailed(t *testing.T) {
	t.Run("5xx", func(t *testing.T) {
		svc, cm, err := recordedRestart(t, nil, http.StatusInternalServerError)

		require.Error(t, err)
		assert.False(t, api.IsAuthRequiredError(err))
		assert.Equal(t, services.StateFailed, svc.GetState())
		assert.Equal(t, 1, cm.count(string(events.ReasonMCPServerRecoveryFailed)))
		assert.Equal(t, 0, cm.count(string(events.ReasonMCPServerRecoveryAwaitingAuth)))
	})

	t.Run("connection refused", func(t *testing.T) {
		cm := &countingEventManager{counts: map[string]int{}}
		api.RegisterEventManager(cm)
		t.Cleanup(func() { api.RegisterEventManager(nil) })

		// A server that is closed before the restart: the port answers with
		// a refused connection.
		ts := startHTTPStatusServer(t, http.StatusOK)
		url := ts.URL + "/mcp"
		ts.Close()

		svc, err := NewService(&api.MCPServer{
			Name: "gone-backend",
			Type: api.MCPServerTypeStreamableHTTP,
			URL:  url,
		})
		require.NoError(t, err)

		err = svc.Restart(t.Context())
		require.Error(t, err)
		assert.False(t, api.IsAuthRequiredError(err))
		assert.Equal(t, services.StateFailed, svc.GetState())
		assert.Equal(t, 1, cm.count(string(events.ReasonMCPServerRecoveryFailed)))
		assert.Equal(t, 0, cm.count(string(events.ReasonMCPServerRecoveryAwaitingAuth)))
	})

	t.Run("401 from a machine identity", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIATEST")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
		t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

		svc, cm, err := recordedRestart(t, &api.MCPServerAuth{
			Type:  api.MCPServerAuthTypeSigV4,
			SigV4: &api.MCPServerSigV4{Region: "eu-central-1"},
		}, http.StatusUnauthorized)

		require.Error(t, err)
		assert.False(t, api.IsAuthRequiredError(err), "a machine identity has no user to send to a login")
		assert.Equal(t, services.StateFailed, svc.GetState())
		assert.Equal(t, 1, cm.count(string(events.ReasonMCPServerRecoveryFailed)))
		assert.Equal(t, 0, cm.count(string(events.ReasonMCPServerRecoveryAwaitingAuth)))
		assert.Equal(t, 0, cm.count(string(events.ReasonMCPServerAuthRequired)))
	})
}
