package aggregator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/chatgptlogin"
	"github.com/giantswarm/muster/v5/internal/chatgptlogin/chatgptlogintest"
	configPkg "github.com/giantswarm/muster/v5/internal/config"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// deviceGrantHandler is an appGrantHandler that files a stored token as the
// person's grant, the way the OAuth proxy files a subject-scoped one.
type deviceGrantHandler struct {
	*appGrantHandler
	mu      sync.Mutex
	stored  []string // session|user
	storedC chan struct{}
}

func (h *deviceGrantHandler) StoreToken(sessionID, userID, issuer string, token *api.OAuthToken) {
	h.mu.Lock()
	h.grants[userID+"|"+issuer] = token
	h.stored = append(h.stored, sessionID+"|"+userID)
	h.mu.Unlock()
	h.storedC <- struct{}{}
}

func (h *deviceGrantHandler) GetFullTokenByIssuerForUser(sessionID, userID, issuer string) *api.OAuthToken {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.appGrantHandler.GetFullTokenByIssuerForUser(sessionID, userID, issuer)
}

// deviceFixture is an aggregator with a ChatGPT App against a fake OpenAI,
// whose polls wait for a tick of the test and whose clock the test moves.
type deviceFixture struct {
	agg     *AggregatorServer
	handler *deviceGrantHandler
	openai  *chatgptlogintest.Server
	app     configPkg.ProviderAppConfig
	ticks   chan struct{}
	set     *providerAppSet

	clockMu sync.Mutex
	now     time.Time
}

func newDeviceFixture(t *testing.T) *deviceFixture {
	t.Helper()
	openai := chatgptlogintest.New(t, configPkg.ChatGPTClientID)
	handler := &deviceGrantHandler{appGrantHandler: newAppGrantHandler(), storedC: make(chan struct{}, 1)}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	apps := configPkg.ProviderAppsWithDefaults([]configPkg.ProviderAppConfig{
		{Name: "chatgpt", Issuer: openai.URL, Flow: configPkg.ProviderAppFlowChatGPTDeviceCode},
	})
	set, err := newProviderAppSet(apps, "muster")
	require.NoError(t, err)
	setAppOwnedIssuers(set)
	t.Cleanup(func() { setAppOwnedIssuers(nil) })

	f := &deviceFixture{
		handler: handler,
		openai:  openai,
		app:     apps[0],
		ticks:   make(chan struct{}, 1), // a poll may not have waited yet
		now:     time.Now(),
	}
	f.set = set
	f.agg = f.replica(t, newMemoryDeviceSignInStore(), nil)
	set.pinAll(context.Background())
	return f
}

// replica is an aggregator on the store, as one muster replica; beats, when
// given, paces its lease renewals.
func (f *deviceFixture) replica(t *testing.T, store deviceSignInStore, beats chan time.Time) *AggregatorServer {
	t.Helper()
	signIns := newDeviceSignIns(store)
	signIns.newClient = func(app configPkg.ProviderAppConfig) deviceCodeClient {
		return &chatgptlogin.Client{
			Issuer:   app.Issuer,
			ClientID: app.ClientID,
			HTTP:     f.openai.Client(),
			Now:      f.clock,
			Wait: func(ctx context.Context, _ time.Duration) error {
				select {
				case <-f.ticks:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		}
	}
	if beats != nil {
		signIns.heartbeat = func() (<-chan time.Time, func()) { return beats, func() {} }
	}
	t.Cleanup(signIns.shutdown)
	return &AggregatorServer{registry: NewServerRegistry("x"), providerApps: f.set, deviceSignIns: signIns}
}

func (f *deviceFixture) clock() time.Time {
	f.clockMu.Lock()
	defer f.clockMu.Unlock()
	return f.now
}

func (f *deviceFixture) advance(d time.Duration) {
	f.clockMu.Lock()
	defer f.clockMu.Unlock()
	f.now = f.now.Add(d)
}

func (f *deviceFixture) status(t *testing.T) pkgoauth.ProviderAppAuthStatus {
	t.Helper()
	got := NewAuthToolProvider(f.agg).providerAppStatuses(context.Background(), appSession, appSubject, true)
	require.Len(t, got, 1)
	return got[0]
}

func deviceCtx() context.Context {
	return api.WithSubject(api.WithSessionID(context.Background(), appSession), appSubject)
}

func structured(t *testing.T, result *api.CallToolResult) map[string]any {
	t.Helper()
	m, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok, "%v", result.Content)
	return m
}

func TestChatGPTApp_PinnedAsAPublicSubjectScopedClient(t *testing.T) {
	f := newDeviceFixture(t)
	pin := f.handler.pins[f.openai.URL]
	assert.True(t, pin.SubjectScoped)
	assert.Equal(t, configPkg.ChatGPTClientID, pin.ClientID)
	assert.Empty(t, pin.ClientSecret)
	assert.Equal(t, f.openai.URL+"/oauth/token", pin.TokenEndpoint)
	assert.Empty(t, pin.RevocationEndpoint, "OpenAI publishes no revocation endpoint for this client")
}

func TestChatGPTApp_PendingThenConnected(t *testing.T) {
	f := newDeviceFixture(t)
	p := NewAuthToolProvider(f.agg)

	assert.Equal(t, pkgoauth.SessionServerStatusAuthRequired, f.status(t).Status, "a status read starts no sign-in")
	assert.Zero(t, f.openai.Polls())

	result, err := p.handleAuthLogin(deviceCtx(), map[string]any{"app": "chatgpt"})
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	got := structured(t, result)
	assert.Equal(t, "pending", got["status"])
	assert.Equal(t, chatgptlogintest.UserCode, got["userCode"])
	assert.Equal(t, f.openai.URL+"/codex/device", got["verificationUrl"])
	assert.Contains(t, contentText(result), chatgptlogintest.UserCode)

	status := f.status(t)
	assert.Equal(t, pkgoauth.SessionServerStatusPending, status.Status)
	assert.Equal(t, chatgptlogintest.UserCode, status.UserCode)
	assert.Equal(t, f.openai.URL+"/codex/device", status.VerificationURL)
	require.NotNil(t, status.CodeExpiresAt)

	again, err := p.handleAuthLogin(deviceCtx(), map[string]any{"app": "chatgpt"})
	require.NoError(t, err)
	assert.Equal(t, got["expiresAt"], structured(t, again)["expiresAt"], "the outstanding code, not a new one")

	f.openai.Enter()
	f.ticks <- struct{}{}
	<-f.handler.storedC
	f.agg.deviceSignIns.polls.Wait()

	grant := f.handler.grants[appSubject+"|"+f.openai.URL]
	require.NotNil(t, grant)
	assert.Equal(t, f.openai.RefreshToken(), grant.RefreshToken)
	assert.Equal(t, f.openai.URL, grant.Issuer)
	assert.False(t, grant.ExpiresAt.IsZero(), "due for refresh before its exp")
	assert.Equal(t, "acct-alice", chatgptlogin.AccountID(grant.IDToken), "the account id is kept with the grant")
	assert.Equal(t, []string{appSession + "|" + appSubject}, f.handler.stored)

	status = f.status(t)
	assert.Equal(t, pkgoauth.SessionServerStatusConnected, status.Status)
	assert.Empty(t, status.UserCode)

	result, err = p.handleAuthLogin(deviceCtx(), map[string]any{"app": "chatgpt"})
	require.NoError(t, err)
	assert.True(t, loginConnected(result), "%v", result.Content)
	assert.Equal(t, 1, f.openai.Exchanges())
}

func TestChatGPTApp_ExpiredCode(t *testing.T) {
	f := newDeviceFixture(t)
	p := NewAuthToolProvider(f.agg)

	_, err := p.handleAuthLogin(deviceCtx(), map[string]any{"app": "chatgpt"})
	require.NoError(t, err)
	f.advance(chatgptlogin.CodeLifetime)
	f.ticks <- struct{}{}
	f.agg.deviceSignIns.polls.Wait()

	status := f.status(t)
	assert.Equal(t, pkgoauth.SessionServerStatusAuthRequired, status.Status)
	assert.Equal(t, "core_auth_login", status.AuthTool)
	assert.Equal(t, chatgptlogin.ErrExpired.Error(), status.LastFailure)
	assert.Nil(t, f.handler.grants[appSubject+"|"+f.openai.URL])

	result, err := p.handleAuthLogin(deviceCtx(), map[string]any{"app": "chatgpt"})
	require.NoError(t, err)
	assert.Equal(t, "pending", structured(t, result)["status"], "connecting again issues a new code")
	assert.Empty(t, f.status(t).LastFailure, "and forgets the failure")
}

func TestChatGPTApp_DeviceCodeSignInNotEnabled(t *testing.T) {
	f := newDeviceFixture(t)
	f.openai.Disable()

	result, err := NewAuthToolProvider(f.agg).handleAuthLogin(deviceCtx(), map[string]any{"app": "chatgpt"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, contentText(result), "device-code sign-in is not enabled for this ChatGPT account")
	assert.Contains(t, contentText(result), "Settings → Security")

	status := f.status(t)
	assert.Equal(t, pkgoauth.SessionServerStatusAuthRequired, status.Status)
	assert.Equal(t, chatgptlogin.ErrNotEnabled.Error(), status.LastFailure)
}

func TestChatGPTApp_Disconnect(t *testing.T) {
	t.Run("ends a pending sign-in", func(t *testing.T) {
		f := newDeviceFixture(t)
		p := NewAuthToolProvider(f.agg)
		_, err := p.handleAuthLogin(deviceCtx(), map[string]any{"app": "chatgpt"})
		require.NoError(t, err)

		result, err := p.handleAuthLogout(deviceCtx(), map[string]any{"app": "chatgpt"})
		require.NoError(t, err)
		require.False(t, result.IsError, "%v", result.Content)
		f.agg.deviceSignIns.polls.Wait()

		status := f.status(t)
		assert.Equal(t, pkgoauth.SessionServerStatusAuthRequired, status.Status)
		assert.Empty(t, status.LastFailure)
		assert.Zero(t, f.openai.Exchanges())
	})

	t.Run("drops the grant", func(t *testing.T) {
		f := newDeviceFixture(t)
		f.handler.grant(f.openai.URL, "chatgpt-access")

		result, err := NewAuthToolProvider(f.agg).handleAuthLogout(deviceCtx(), map[string]any{"app": "chatgpt"})
		require.NoError(t, err)
		require.False(t, result.IsError, "%v", result.Content)
		got := structured(t, result)
		assert.Equal(t, false, got["revoked"], "OpenAI publishes no revocation endpoint")
		assert.Equal(t, true, got["dropped"])
		assert.Contains(t, contentText(result), "offers no revocation")
		assert.Equal(t, []string{f.openai.URL}, f.handler.cleared)
		assert.Nil(t, f.handler.grants[appSubject+"|"+f.openai.URL])
		assert.Equal(t, pkgoauth.SessionServerStatusAuthRequired, f.status(t).Status)
	})
}

func TestChatGPTApp_ReleasableUnderItsName(t *testing.T) {
	apps := configPkg.ProviderAppsWithDefaults([]configPkg.ProviderAppConfig{
		{Name: "chatgpt", Flow: configPkg.ProviderAppFlowChatGPTDeviceCode},
	})
	releasable, _, err := releasableWithProviderApps(nil, nil, apps)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"chatgpt": configPkg.ChatGPTIssuer}, releasable)
}

func TestChatGPTApp_GrantRequiredAnswersTheCode(t *testing.T) {
	f := newDeviceFixture(t)
	server := &ServerInfo{
		Name:       "codex-tools",
		URL:        "http://codex-tools.agent-platform.svc/mcp",
		AuthConfig: &api.MCPServerAuth{ForwardToken: true, ProviderApps: []string{"chatgpt"}},
	}
	f.agg.registry.mu.Lock()
	f.agg.registry.servers[server.Name] = server
	f.agg.registry.mu.Unlock()

	got := f.agg.answerGrantRequired(deviceCtx(), server.Name, appSession, appSubject, grantRequiredResult("chatgpt"))
	require.True(t, got.IsError)
	structuredGot, ok := got.StructuredContent.(map[string]any)
	require.True(t, ok)
	missing, ok := structuredGot[pkgoauth.FieldGrantApps].([]map[string]any)
	require.True(t, ok)
	require.Len(t, missing, 1)
	assert.Equal(t, chatgptlogintest.UserCode, missing[0]["userCode"])
	assert.Equal(t, f.openai.URL+"/codex/device", missing[0]["verificationUrl"])
}
