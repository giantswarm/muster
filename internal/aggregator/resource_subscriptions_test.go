package aggregator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
	"github.com/giantswarm/muster/v5/internal/server"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mailboxBackend is a 2026-07-28 MCP server whose resources are
// test://mailbox/<person>, each readable only by its person, the way
// beekeeper's central instance serves them. A leaky backend sends every
// update to every open stream, so the tests see what muster forwards.
type mailboxBackend struct {
	leaky bool

	mu       sync.Mutex
	streams  []*mailboxStream
	bearers  []string
	listened chan struct{}
}

type mailboxStream struct {
	person  string
	session mcpserver.ClientSession
	id      any
	uris    map[string]bool
}

type bearerKey struct{}

// personOf reads the sub of the unsigned JWT a test subscriber presents.
func personOf(bearer string) string {
	parts := strings.Split(strings.TrimPrefix(bearer, "Bearer "), ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	_ = json.Unmarshal(payload, &claims)
	return claims.Sub
}

func newMailboxBackend(t *testing.T, leaky bool) (*mailboxBackend, string) {
	t.Helper()
	b := &mailboxBackend{leaky: leaky, listened: make(chan struct{}, 16)}
	hooks := &mcpserver.Hooks{}
	hooks.AddOnRequestInitialization(func(ctx context.Context, _ any, message any) error {
		raw, _ := message.(json.RawMessage)
		var req subscriptionsListenRequest
		if json.Unmarshal(raw, &req) != nil || req.Method != string(mcp.MethodSubscriptionsListen) {
			return nil
		}
		person := personOf(ctx.Value(bearerKey{}).(string))
		for _, uri := range req.Params.Notifications.ResourceSubscriptions {
			if uri != "test://mailbox/"+person {
				return fmt.Errorf("%s is not %s's", uri, person)
			}
		}
		return nil
	})
	hooks.AddBeforeSubscriptionsListen(func(ctx context.Context, id any, req *mcp.SubscriptionsListenRequest) {
		bearer := ctx.Value(bearerKey{}).(string)
		s := &mailboxStream{person: personOf(bearer), session: mcpserver.ClientSessionFromContext(ctx), id: id, uris: map[string]bool{}}
		for _, uri := range req.Params.Notifications.ResourceSubscriptions {
			s.uris[uri] = true
		}
		b.mu.Lock()
		b.streams = append(b.streams, s)
		b.bearers = append(b.bearers, personOf(bearer))
		b.mu.Unlock()
		go func() {
			<-ctx.Done()
			b.mu.Lock()
			defer b.mu.Unlock()
			for i, open := range b.streams {
				if open == s {
					b.streams = append(b.streams[:i], b.streams[i+1:]...)
					break
				}
			}
		}()
		b.listened <- struct{}{}
	})
	srv := mcpserver.NewMCPServer("mailbox", "1",
		mcpserver.WithResourceCapabilities(true, false),
		mcpserver.WithHooks(hooks))
	handler := mcpserver.NewStreamableHTTPServer(srv,
		mcpserver.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			return context.WithValue(ctx, bearerKey{}, r.Header.Get("Authorization"))
		}))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return b, ts.URL + "/mcp"
}

// updated sends notifications/resources/updated for uri to the streams
// subscribed to it, or to every stream when the backend is leaky.
func (b *mailboxBackend) updated(uri string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.streams {
		if !b.leaky && !s.uris[uri] {
			continue
		}
		s.session.NotificationChannel() <- mcp.JSONRPCNotification{
			JSONRPC: mcp.JSONRPC_VERSION,
			Notification: mcp.Notification{
				Method: mcp.MethodNotificationResourceUpdated,
				Params: mcp.NotificationParams{
					Meta:             map[string]any{mcp.MetaKeySubscriptionID: s.id},
					AdditionalFields: map[string]any{"uri": uri},
				},
			},
		}
	}
}

func (b *mailboxBackend) openStreams() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.streams)
}

// downstreamSession is a client's subscriptions/listen stream on muster.
type downstreamSession struct {
	id string
	ch chan mcp.JSONRPCNotification
}

func (s *downstreamSession) Initialize()                                         {}
func (s *downstreamSession) Initialized() bool                                   { return true }
func (s *downstreamSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return s.ch }
func (s *downstreamSession) SessionID() string                                   { return s.id }

func listenMessage(t *testing.T, id int, uris ...string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  mcp.MethodSubscriptionsListen,
		"params": map[string]any{
			"notifications": map[string]any{"resourceSubscriptions": uris},
			"_meta":         map[string]any{mcp.MetaKeyProtocolVersion: mcp.ProtocolVersion20260728},
		},
	})
	require.NoError(t, err)
	return raw
}

// subscriber is one user's subscriptions/listen request on muster.
type subscriber struct {
	session *downstreamSession
	ctx     context.Context
	cancel  context.CancelFunc
}

func newSubscriber(t *testing.T, person string) *subscriber {
	t.Helper()
	sess := &downstreamSession{id: "transport-" + person, ch: make(chan mcp.JSONRPCNotification, 16)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ctx = server.ContextWithBearerToken(ctx, unsignedJWT(t, map[string]any{"sub": person, "exp": time.Now().Add(time.Hour).Unix()}))
	ctx = api.WithSessionID(ctx, "session-"+person)
	ctx = mcpserver.NewMCPServer("muster", "test").WithContext(ctx, sess)
	return &subscriber{session: sess, ctx: ctx, cancel: cancel}
}

func (s *subscriber) next(t *testing.T) mcp.JSONRPCNotification {
	t.Helper()
	select {
	case n := <-s.session.ch:
		return n
	case <-time.After(10 * time.Second):
		t.Fatal("no notification relayed")
		return mcp.JSONRPCNotification{}
	}
}

// newSubscriptionAggregator registers the backend as a per-user server that
// forwards each session's own bearer, and records the mailbox URIs as listed.
func newSubscriptionAggregator(t *testing.T, url string, people ...string) *AggregatorServer {
	t.Helper()
	a := newTestAggregatorWithPool(t)
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "mailbox"},
		URL:                url,
		AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
	}))
	for _, person := range people {
		require.NoError(t, a.authStore.MarkAuthenticated(context.Background(), "session-"+person, "mailbox"))
		a.registry.ExposedResourceURI("mailbox", "test://mailbox/"+person)
	}
	return a
}

// TestRelaySubscriptionsListen_EachUserGetsOnlyItsOwnUpdates subscribes two
// users to their own mailboxes through a backend that leaks every update to
// every stream: each user receives its own updates, tagged with its own
// subscription ID, and never the other's; the backend saw each user's own
// token.
func TestRelaySubscriptionsListen_EachUserGetsOnlyItsOwnUpdates(t *testing.T) {
	backend, url := newMailboxBackend(t, true)
	a := newSubscriptionAggregator(t, url, "alice", "bob")

	alice, bob := newSubscriber(t, "alice"), newSubscriber(t, "bob")
	require.NoError(t, a.relaySubscriptionsListen(alice.ctx, 1, listenMessage(t, 1, "test://mailbox/alice")))
	require.NoError(t, a.relaySubscriptionsListen(bob.ctx, 2, listenMessage(t, 2, "test://mailbox/bob")))

	backend.mu.Lock()
	assert.ElementsMatch(t, []string{"alice", "bob"}, backend.bearers,
		"each backend stream is opened with its user's own token")
	backend.mu.Unlock()

	backend.updated("test://mailbox/alice")
	backend.updated("test://mailbox/bob")

	got := alice.next(t)
	assert.Equal(t, mcp.MethodNotificationResourceUpdated, got.Method)
	assert.Equal(t, "test://mailbox/alice", got.Params.AdditionalFields["uri"])
	assert.EqualValues(t, 1, got.Params.Meta[mcp.MetaKeySubscriptionID])

	got = bob.next(t)
	assert.Equal(t, "test://mailbox/bob", got.Params.AdditionalFields["uri"],
		"bob's first update is his own: alice's, leaked to his backend stream, was not relayed")
	assert.EqualValues(t, 2, got.Params.Meta[mcp.MetaKeySubscriptionID])

	backend.updated("test://mailbox/alice")
	got = alice.next(t)
	assert.Equal(t, "test://mailbox/alice", got.Params.AdditionalFields["uri"],
		"alice's next update is her own: bob's was not relayed to her")
}

// TestRelaySubscriptionsListen_EndsWithTheClientStream pins the teardown: when
// the client's stream ends, its backend stream ends too.
func TestRelaySubscriptionsListen_EndsWithTheClientStream(t *testing.T) {
	backend, url := newMailboxBackend(t, false)
	a := newSubscriptionAggregator(t, url, "alice")

	alice := newSubscriber(t, "alice")
	require.NoError(t, a.relaySubscriptionsListen(alice.ctx, 1, listenMessage(t, 1, "test://mailbox/alice")))
	require.Equal(t, 1, backend.openStreams())

	alice.cancel()
	require.Eventually(t, func() bool { return backend.openStreams() == 0 }, 10*time.Second, 10*time.Millisecond,
		"the backend stream closes with the client's")
}

// TestRelaySubscriptionsListen_Refusals pins what is answered with an error
// before anything is held open.
func TestRelaySubscriptionsListen_Refusals(t *testing.T) {
	_, url := newMailboxBackend(t, false)
	a := newSubscriptionAggregator(t, url, "alice", "bob")

	t.Run("the backend refuses another person's mailbox", func(t *testing.T) {
		bob := newSubscriber(t, "bob")
		err := a.relaySubscriptionsListen(bob.ctx, 1, listenMessage(t, 1, "test://mailbox/alice"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not bob's")
	})

	t.Run("an unknown resource", func(t *testing.T) {
		alice := newSubscriber(t, "alice")
		err := a.relaySubscriptionsListen(alice.ctx, 1, listenMessage(t, 1, "nothing-here"))
		require.ErrorContains(t, err, "nothing-here")
	})

	t.Run("a shared server muster reaches with its own identity", func(t *testing.T) {
		require.NoError(t, a.registry.Register(context.Background(), ServerRegistration{Name: "shared"}, &callToolMockClient{}))
		a.registry.ExposedResourceURI("shared", "shared://thing")
		alice := newSubscriber(t, "alice")
		err := a.relaySubscriptionsListen(alice.ctx, 1, listenMessage(t, 1, "shared://thing"))
		require.ErrorContains(t, err, "only as the subscribing user")
	})

	t.Run("legacy requests and other methods are left to mcp-go", func(t *testing.T) {
		alice := newSubscriber(t, "alice")
		legacy, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": mcp.MethodSubscriptionsListen,
			"params": map[string]any{"notifications": map[string]any{"resourceSubscriptions": []string{"nothing-here"}}}})
		require.NoError(t, err)
		assert.NoError(t, a.relaySubscriptionsListen(alice.ctx, 1, json.RawMessage(legacy)))
		assert.NoError(t, a.relaySubscriptionsListen(alice.ctx, 1, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	})
}

func TestHideLegacySubscribe(t *testing.T) {
	result := &mcp.InitializeResult{}
	require.NoError(t, json.Unmarshal([]byte(`{"capabilities":{"resources":{"subscribe":true,"listChanged":true}}}`), result))
	hideLegacySubscribe(result)
	assert.False(t, result.Capabilities.Resources.Subscribe)
	assert.True(t, result.Capabilities.Resources.ListChanged)
	hideLegacySubscribe(&mcp.InitializeResult{})
	hideLegacySubscribe(nil)
}

// TestResolveSubscribedResource_TemplateURIByScheme pins how a URI no listing
// named, one a backend's resource template fills, finds its backend: the one
// server whose resources in the session's view share its scheme.
func TestResolveSubscribedResource_TemplateURIByScheme(t *testing.T) {
	a := newSubscriptionAggregator(t, "http://unused.invalid/mcp")
	ctx := context.Background()
	require.NoError(t, a.capabilityStore.Set(ctx, "session-alice", "mailbox",
		&oauthstore.Capabilities{Resources: []mcp.Resource{{URI: "test://feed"}}}))

	server, original, err := a.resolveSubscribedResource(ctx, "session-alice", "test://mailbox/alice")
	require.NoError(t, err)
	assert.Equal(t, "mailbox", server)
	assert.Equal(t, "test://mailbox/alice", original)

	_, _, err = a.resolveSubscribedResource(ctx, "session-alice", "other://mailbox/alice")
	assert.ErrorContains(t, err, "unknown resource")

	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "mailbox-2"},
		URL:                "http://unused.invalid/mcp",
		AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
	}))
	require.NoError(t, a.capabilityStore.Set(ctx, "session-alice", "mailbox-2",
		&oauthstore.Capabilities{Resources: []mcp.Resource{{URI: "test://other"}}}))
	_, _, err = a.resolveSubscribedResource(ctx, "session-alice", "test://mailbox/alice")
	assert.ErrorContains(t, err, "mailbox, mailbox-2")
}
