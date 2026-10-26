package oauth

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/config"
)

func TestStateStores_FinishedRecord(t *testing.T) {
	params := StateParams{SessionID: "session-1", UserID: "user-1", ServerName: "server-1", Issuer: "https://idp.example.com", CodeVerifier: "verifier"}

	t.Run("memory", func(t *testing.T) {
		ss := NewStateStore()
		defer ss.Stop()
		encoded, err := ss.GenerateState(params, nil)
		require.NoError(t, err)

		require.Empty(t, ss.Finished(encoded), "the callback has not run")
		ss.MarkFinished(encoded, "session-1")
		require.Equal(t, "session-1", ss.Finished(encoded))
		require.Equal(t, "session-1", ss.Finished(encoded), "reading the record does not consume it")
		require.Empty(t, ss.Finished("not-a-real-state"))

		nonce, err := decodeStateNonce(encoded)
		require.NoError(t, err)
		ss.mu.Lock()
		ss.finished[nonce].at = time.Now().Add(-finishedGrace - time.Minute)
		ss.mu.Unlock()
		require.Empty(t, ss.Finished(encoded), "an expired record is not returned")
		ss.cleanup()
		ss.mu.RLock()
		_, kept := ss.finished[nonce]
		ss.mu.RUnlock()
		require.False(t, kept, "cleanup purges expired records")
	})

	t.Run("valkey", func(t *testing.T) {
		srv := miniredis.RunT(t)
		vc, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
		require.NoError(t, err)
		t.Cleanup(vc.Close)
		writer := NewValkeyStateStore(vc, "muster:", nil)
		reader := NewValkeyStateStore(vc, "muster:", nil)
		encoded, err := writer.GenerateState(params, nil)
		require.NoError(t, err)

		require.Empty(t, reader.Finished(encoded), "the callback has not run")
		writer.MarkFinished(encoded, "session-1")
		require.Equal(t, "session-1", reader.Finished(encoded), "another replica reads the record")
		require.Empty(t, reader.Finished("not-a-real-state"))

		srv.FastForward(finishedGrace)
		require.Empty(t, reader.Finished(encoded), "the record expires")
	})
}

// The callback records the end of the flow whatever its outcome, for the
// session the flow was started for only.
func TestHandler_HandleCallback_MarksTheFlowFinished(t *testing.T) {
	t.Run("signed in", func(t *testing.T) {
		as := newDCRTestServer(t)
		as.advertiseCIMD = true
		client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
			"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
		defer client.Stop()
		callbackPath := startCallbackFlow(t, client, as.server.URL)
		startURL := startURLOfCallback(t, client, callbackPath)
		require.False(t, client.SignInFinished("session-1", startURL), "the callback has not run")

		rr := httptest.NewRecorder()
		NewHandler(client).HandleCallback(rr, httptest.NewRequest("GET", callbackPath, nil))

		require.Contains(t, rr.Body.String(), "Signed in")
		require.True(t, client.SignInFinished("session-1", startURL))
		require.False(t, client.SignInFinished("session-2", startURL), "another session's flow")
	})

	t.Run("the completion callback fails, after it ran", func(t *testing.T) {
		as := newDCRTestServer(t)
		as.advertiseCIMD = true
		manager := NewManager(config.OAuthMCPClientConfig{
			Enabled:      true,
			PublicURL:    "https://muster.example.com",
			ClientID:     "https://muster.example.com/.well-known/oauth-client.json",
			CallbackPath: "/oauth/proxy/callback",
		})
		defer manager.Stop()
		callbackPath := startCallbackFlow(t, manager.client, as.server.URL)
		startURL := startURLOfCallback(t, manager.client, callbackPath)
		finishedDuringCallback := true
		manager.SetAuthCompletionCallback(func(context.Context, string, string, string, string) error {
			finishedDuringCallback = manager.SignInFinished("session-1", startURL)
			return errors.New("connection refused")
		})

		manager.handler.HandleCallback(httptest.NewRecorder(), httptest.NewRequest("GET", callbackPath, nil))

		require.False(t, finishedDuringCallback, "a waiting call must not run before the session is connected")
		require.True(t, manager.SignInFinished("session-1", startURL))
	})

	t.Run("refused by the authorization server", func(t *testing.T) {
		authServer := newAuthorizationServer(t, authorizationServerOptions{})
		defer authServer.Close()
		client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
		defer client.Stop()
		encodedState := storeCallbackState(t, client, authServer.URL)

		rr := httptest.NewRecorder()
		NewHandler(client).HandleCallback(rr, httptest.NewRequest("GET", "/oauth/proxy/callback?error=access_denied&state="+url.QueryEscape(encodedState), nil))

		require.Contains(t, rr.Body.String(), summaryDenied)
		require.True(t, client.SignInFinished("session-1", client.GetStartURL(encodedState)))
	})

	t.Run("a link that is not a start URL", func(t *testing.T) {
		client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid")
		defer client.Stop()
		require.False(t, client.SignInFinished("session-1", "https://muster.example.com/elsewhere"))
		require.False(t, client.SignInFinished("session-1", "%zz"))
		require.False(t, client.SignInFinished("", client.GetStartURL("state")))
	})
}

func startURLOfCallback(t *testing.T, client *Client, callbackPath string) string {
	t.Helper()
	parsed, err := url.Parse(callbackPath)
	require.NoError(t, err)
	return client.GetStartURL(parsed.Query().Get("state"))
}
