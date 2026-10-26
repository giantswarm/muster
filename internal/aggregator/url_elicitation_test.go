package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/metatools"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
)

const testAuthURL = "https://muster.example.com/oauth/proxy/start?state=abc"

// legacySession is a client session that negotiated a protocol version
// through initialize and declared its capabilities there.
type legacySession struct {
	id           string
	ch           chan mcp.JSONRPCNotification
	capabilities mcp.ClientCapabilities
}

func (s *legacySession) Initialize()                                         {}
func (s *legacySession) Initialized() bool                                   { return true }
func (s *legacySession) NotificationChannel() chan<- mcp.JSONRPCNotification { return s.ch }
func (s *legacySession) SessionID() string                                   { return s.id }
func (s *legacySession) ProtocolVersion() string                             { return mcp.ProtocolVersion20251125 }
func (s *legacySession) GetClientInfo() mcp.Implementation                   { return mcp.Implementation{} }
func (s *legacySession) SetClientInfo(mcp.Implementation)                    {}
func (s *legacySession) GetClientCapabilities() mcp.ClientCapabilities       { return s.capabilities }
func (s *legacySession) SetClientCapabilities(c mcp.ClientCapabilities)      { s.capabilities = c }

func urlElicitationCapabilities() mcp.ClientCapabilities {
	return mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{URL: &struct{}{}}}
}

func newLegacySession(id string, capabilities mcp.ClientCapabilities) *legacySession {
	return &legacySession{id: id, ch: make(chan mcp.JSONRPCNotification, 4), capabilities: capabilities}
}

func modernContext(ctx context.Context, capabilities mcp.ClientCapabilities) context.Context {
	return mcpserver.WithRequestProtocolInfo(api.WithSessionID(ctx, "family-1"), &mcpserver.RequestProtocolInfo{
		Modern:             true,
		ProtocolVersion:    mcp.ProtocolVersion20260728,
		ClientCapabilities: &capabilities,
	})
}

func authRequiredAnswerFor(server string) *mcp.CallToolResult {
	return authRequiredResult(server, "this session is not authenticated to it", &api.CallToolResult{
		Content:           []any{"Sign in: " + testAuthURL},
		StructuredContent: map[string]any{"authUrl": testAuthURL},
	})
}

// signInHandler answers like a gated tool call: it records muster's
// challenge and returns the auth_required answer.
func signInHandler(record bool) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if record {
			recordSignInChallenge(ctx, signInChallenge{
				sessionID: "family-1",
				server:    "github",
				authURL:   testAuthURL,
				reason:    "this session is not authenticated to it",
			})
		}
		return authRequiredAnswerFor("github"), nil
	}
}

func TestURLElicitation(t *testing.T) {
	t.Run("a 2026-07-28 request is answered with an input_required URL elicitation", func(t *testing.T) {
		a := &AggregatorServer{}
		ctx := modernContext(t.Context(), urlElicitationCapabilities())

		result, err := a.urlElicitation()(signInHandler(true))(ctx, mcp.CallToolRequest{})

		require.NoError(t, err)
		require.Equal(t, mcp.ResultTypeInputRequired, result.ResultType)
		request, ok := result.InputRequests[signInInputKey]
		require.True(t, ok)
		require.NotNil(t, request.Elicitation)
		require.Equal(t, mcp.ElicitationModeURL, request.Elicitation.Mode)
		require.Equal(t, testAuthURL, request.Elicitation.URL)
		require.Empty(t, request.Elicitation.ElicitationID, "2026-07-28 removed elicitationId")
		require.Equal(t, "github", result.RequestState, "the retry names the sign-in to wait for")
		require.Empty(t, result.Content, "the link must not reach the model as tool output")
	})

	t.Run("a core_auth_login challenge is answered with a URL elicitation", func(t *testing.T) {
		a := &AggregatorServer{}
		ctx := modernContext(t.Context(), urlElicitationCapabilities())
		login := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			recordSignInChallenge(ctx, signInChallenge{sessionID: "family-1", server: "github", authURL: testAuthURL})
			return &mcp.CallToolResult{
				Content:           []mcp.Content{mcp.NewTextContent("Sign in: " + testAuthURL)},
				StructuredContent: map[string]any{"authUrl": testAuthURL},
			}, nil
		}

		result, err := a.urlElicitation()(login)(ctx, mcp.CallToolRequest{})

		require.NoError(t, err)
		require.Equal(t, mcp.ResultTypeInputRequired, result.ResultType)
		require.Equal(t, testAuthURL, result.InputRequests[signInInputKey].Elicitation.URL)
		require.Equal(t, "Sign in to github to continue.", result.InputRequests[signInInputKey].Elicitation.Message)
	})

	t.Run("a 2025-11-25 session is answered with URLElicitationRequiredError", func(t *testing.T) {
		a := &AggregatorServer{}
		session := newLegacySession("transport-1", urlElicitationCapabilities())
		ctx := mcpserver.NewMCPServer("muster", "test").WithContext(t.Context(), session)

		result, err := a.urlElicitation()(signInHandler(true))(ctx, mcp.CallToolRequest{})

		require.Nil(t, result)
		var required mcp.URLElicitationRequiredError
		require.True(t, errors.As(err, &required))
		require.Len(t, required.Elicitations, 1)
		elicitation := required.Elicitations[0]
		require.Equal(t, mcp.ElicitationModeURL, elicitation.Mode)
		require.Equal(t, testAuthURL, elicitation.URL)
		require.NoError(t, elicitation.Validate())

		retried, err := a.urlElicitation()(signInHandler(true))(ctx, mcp.CallToolRequest{})
		require.Nil(t, retried)
		var again mcp.URLElicitationRequiredError
		require.True(t, errors.As(err, &again))
		require.Equal(t, elicitation.ElicitationID, again.Elicitations[0].ElicitationID,
			"a retry while the sign-in is pending is the same elicitation")

		require.Equal(t, []sentElicitation{{transportID: "transport-1", elicitationID: elicitation.ElicitationID}},
			a.signInElicitations.take("family-1", "github"))
	})

	t.Run("a 2026-07-28 retry that declined or cancelled the sign-in is not asked again", func(t *testing.T) {
		for _, action := range []mcp.ElicitationResponseAction{mcp.ElicitationResponseActionDecline, mcp.ElicitationResponseActionCancel} {
			t.Run(string(action), func(t *testing.T) {
				a := &AggregatorServer{}
				ctx := modernContext(t.Context(), urlElicitationCapabilities())
				var req mcp.CallToolRequest
				req.Params.InputResponses = mcp.InputResponses{
					signInInputKey: mcp.NewElicitationInputResponse(mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: action}}),
				}

				req.Params.RequestState = "github"
				ran := false
				handler := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					ran = true
					return mcp.NewToolResultText("done"), nil
				}

				result, err := a.urlElicitation()(handler)(ctx, req)

				require.NoError(t, err)
				require.False(t, ran, "a declined sign-in runs nothing")
				require.True(t, result.IsError)
				require.Contains(t, mcpText(result), "github")
				require.Empty(t, result.InputRequests)
				require.NotContains(t, mcpText(result), testAuthURL, "the link must not reach the model as tool output")
			})
		}
	})

	t.Run("a client without elicitation.url keeps the auth_required answer", func(t *testing.T) {
		a := &AggregatorServer{}
		formOnly := mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{Form: &struct{}{}}}

		for name, ctx := range map[string]context.Context{
			"2026-07-28": modernContext(t.Context(), formOnly),
			"2025-11-25": mcpserver.NewMCPServer("muster", "test").WithContext(t.Context(), newLegacySession("transport-1", formOnly)),
		} {
			t.Run(name, func(t *testing.T) {
				result, err := a.urlElicitation()(signInHandler(true))(ctx, mcp.CallToolRequest{})

				require.NoError(t, err)
				require.Equal(t, authRequiredAnswerFor("github"), result)
			})
		}
	})

	t.Run("an auth_required result muster did not issue is passed through", func(t *testing.T) {
		// SECURITY: a backend can return a result shaped like muster's
		// auth_required answer with a link of its own; it must never be
		// presented to the person as muster's sign-in.
		a := &AggregatorServer{}
		ctx := modernContext(t.Context(), urlElicitationCapabilities())

		result, err := a.urlElicitation()(signInHandler(false))(ctx, mcp.CallToolRequest{})

		require.NoError(t, err)
		require.Equal(t, authRequiredAnswerFor("github"), result)
	})

	t.Run("an auth_required result with another link than muster's challenge is passed through", func(t *testing.T) {
		a := &AggregatorServer{}
		ctx := modernContext(t.Context(), urlElicitationCapabilities())
		backend := authRequiredResult("github", "backend", &api.CallToolResult{
			StructuredContent: map[string]any{"authUrl": "https://attacker.example.com/"},
		})
		handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, _ = signInHandler(true)(ctx, req)
			return backend, nil
		}

		result, err := a.urlElicitation()(handler)(ctx, mcp.CallToolRequest{})

		require.NoError(t, err)
		require.Same(t, backend, result)
	})

	t.Run("a call answered with anything but auth_required is passed through", func(t *testing.T) {
		a := &AggregatorServer{}
		ctx := modernContext(t.Context(), urlElicitationCapabilities())
		ok := mcp.NewToolResultText("done")
		handler := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			recordSignInChallenge(ctx, signInChallenge{server: "github", authURL: testAuthURL})
			return ok, nil
		}

		result, err := a.urlElicitation()(handler)(ctx, mcp.CallToolRequest{})

		require.NoError(t, err)
		require.Same(t, ok, result)
	})
}

func TestCompleteSignInElicitations(t *testing.T) {
	srv := mcpserver.NewMCPServer("muster", "test")
	asked := newLegacySession("transport-asked", urlElicitationCapabilities())
	other := newLegacySession("transport-other", urlElicitationCapabilities())
	require.NoError(t, srv.RegisterSession(t.Context(), asked))
	require.NoError(t, srv.RegisterSession(t.Context(), other))
	a := &AggregatorServer{mcpServer: srv}
	elicitationID := a.signInElicitations.add("family-1", "github", asked.id, testAuthURL)

	a.completeSignInElicitations("family-1", "github")

	require.Len(t, asked.ch, 1)
	notification := <-asked.ch
	require.Equal(t, string(mcp.MethodNotificationElicitationComplete), notification.Method)
	require.Equal(t, elicitationID, notification.Params.AdditionalFields["elicitationId"])
	require.Empty(t, other.ch, "only the session that was asked is told")
	require.Empty(t, a.signInElicitations.take("family-1", "github"), "an elicitation completes once")
}

// The OAuth callback completes the elicitations of the sign-in whether or
// not the connection it then makes succeeds, so the client retries instead
// of waiting for a notification that never comes.
func TestAuthCompletionCompletesSignInElicitations(t *testing.T) {
	for name, suspended := range map[string]bool{
		"the server was suspended meanwhile": true,
		"the connection fails":               false,
	} {
		t.Run(name, func(t *testing.T) {
			a := newSuspensionTestServer(t)
			state := api.StateAuthRequired
			if suspended {
				state = api.StateDisconnected
			}
			registerSuspendableServer(t, a.registry, "miro", suspended, state)
			a.mcpServer = mcpserver.NewMCPServer("muster", "test")
			session := newLegacySession("transport-1", urlElicitationCapabilities())
			require.NoError(t, a.mcpServer.RegisterSession(t.Context(), session))
			elicitationID := a.signInElicitations.add("test-session", "miro", session.id, testAuthURL)
			am := &AggregatorManager{aggregatorServer: a}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_ = am.handleAuthCompletion(ctx, "test-session", "test-user", "miro", "access-token")

			require.Len(t, session.ch, 1)
			notification := <-session.ch
			require.Equal(t, string(mcp.MethodNotificationElicitationComplete), notification.Method)
			require.Equal(t, elicitationID, notification.Params.AdditionalFields["elicitationId"])
		})
	}
}

func TestSignInElicitationsExpire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s signInElicitations
		old := s.add("family-1", "github", "transport-1", testAuthURL)

		time.Sleep(signInElicitationTTL)
		fresh := s.add("family-1", "github", "transport-1", testAuthURL)

		require.NotEqual(t, old, fresh, "an expired elicitation is not reused")
		require.Equal(t, []sentElicitation{{transportID: "transport-1", elicitationID: fresh}}, s.take("family-1", "github"))
	})
}

func TestSignInElicitationsPerLink(t *testing.T) {
	var s signInElicitations
	first := s.add("family-1", "github", "transport-1", testAuthURL)
	require.Equal(t, first, s.add("family-1", "github", "transport-1", testAuthURL), "the same link is the same elicitation")

	second := s.add("family-1", "github", "transport-1", testAuthURL+"-new")

	require.NotEqual(t, first, second, "a new link is a new elicitation")
	require.ElementsMatch(t, []sentElicitation{
		{transportID: "transport-1", elicitationID: first},
		{transportID: "transport-1", elicitationID: second},
	}, s.take("family-1", "github"), "the sign-in completes every elicitation of it")
}

// TestURLElicitationOnTheWire sends real tools/call messages through an mcp-go
// server with the middleware installed, as a client of each protocol version.
func TestURLElicitationOnTheWire(t *testing.T) {
	a := &AggregatorServer{}
	srv := mcpserver.NewMCPServer("muster", "test", mcpserver.WithToolHandlerMiddleware(a.urlElicitation()))
	srv.AddTool(mcp.NewTool("github_list_repos"), signInHandler(true))

	t.Run("2025-11-25", func(t *testing.T) {
		session := newLegacySession("transport-1", urlElicitationCapabilities())
		ctx := srv.WithContext(t.Context(), session)

		reply := srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"github_list_repos"}}`))

		raw, err := json.Marshal(reply)
		require.NoError(t, err)
		var decoded struct {
			Error struct {
				Code int `json:"code"`
				Data struct {
					Elicitations []mcp.ElicitationParams `json:"elicitations"`
				} `json:"data"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(raw, &decoded), string(raw))
		require.Equal(t, mcp.URL_ELICITATION_REQUIRED, decoded.Error.Code, string(raw))
		require.Len(t, decoded.Error.Data.Elicitations, 1)
		require.Equal(t, testAuthURL, decoded.Error.Data.Elicitations[0].URL)
		require.NotEmpty(t, decoded.Error.Data.Elicitations[0].ElicitationID)
	})

	t.Run("2026-07-28", func(t *testing.T) {
		message := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"github_list_repos","_meta":{%q:%q,%q:{"elicitation":{"url":{}}}}}}`,
			mcp.MetaKeyProtocolVersion, mcp.ProtocolVersion20260728, mcp.MetaKeyClientCapabilities)

		reply := srv.HandleMessage(t.Context(), []byte(message))

		raw, err := json.Marshal(reply)
		require.NoError(t, err)
		var decoded struct {
			Result struct {
				ResultType    string `json:"resultType"`
				InputRequests map[string]struct {
					Method string                `json:"method"`
					Params mcp.ElicitationParams `json:"params"`
				} `json:"inputRequests"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal(raw, &decoded), string(raw))
		require.Equal(t, string(mcp.ResultTypeInputRequired), string(decoded.Result.ResultType), string(raw))
		request := decoded.Result.InputRequests[signInInputKey]
		require.Equal(t, string(mcp.MethodElicitationCreate), request.Method, string(raw))
		require.Equal(t, mcp.ElicitationModeURL, request.Params.Mode)
		require.Equal(t, testAuthURL, request.Params.URL)
	})
}

// A gated tool call of a signed-out session records the challenge its
// auth_required answer carries, for the middleware to elicit.
func TestURLElicitation_GatedCallRecordsMustersChallenge(t *testing.T) {
	f := newPollFixture(t)
	ctx := api.WithSubject(api.WithSessionID(t.Context(), answerSession), answerSubject)
	ctx, recorder := withChallengeRecorder(ctx)

	result, err := f.agg.callToolWithTokenExchangeRetry(ctx, answerServer, "op", map[string]any{}, answerSession, answerSubject)
	require.NoError(t, err)

	challenge := recorder.challenge.Load()
	require.NotNil(t, challenge)
	require.Equal(t, authURLOf(t, result), challenge.authURL)
	require.Equal(t, answerServer, challenge.server)
	require.Equal(t, answerSession, challenge.sessionID)
}

// core_auth_login records the challenge it answers with, so a client that
// supports URL elicitation is asked to open its link too.
func TestURLElicitation_CoreAuthLoginRecordsMustersChallenge(t *testing.T) {
	f := newPollFixture(t)
	ctx := api.WithSubject(api.WithSessionID(t.Context(), answerSession), answerSubject)
	ctx, recorder := withChallengeRecorder(ctx)

	result, err := NewAuthToolProvider(f.agg).handleAuthLogin(ctx, map[string]any{resourceServerArg: answerServer})
	require.NoError(t, err)

	challenge := recorder.challenge.Load()
	require.NotNil(t, challenge)
	require.Equal(t, challengeURL(result), challenge.authURL)
	require.NotEmpty(t, challenge.authURL)
	require.Equal(t, answerServer, challenge.server)
	require.Equal(t, answerSession, challenge.sessionID)
}

// call_tool relays the wrapped tool's structuredContent, so a gated call made
// through it is elicited like a direct call, and the envelope that repeats the
// link in its text never reaches the model.
func TestURLElicitation_CallToolRelaysMustersChallenge(t *testing.T) {
	f := newPollFixture(t)
	previousMeta, previousData := api.GetMetaTools(), api.GetMetaToolsDataProvider()
	meta := metatools.NewAdapter()
	meta.Register()
	api.RegisterMetaToolsDataProvider(f.agg)
	t.Cleanup(func() {
		api.RegisterMetaTools(previousMeta)
		api.RegisterMetaToolsDataProvider(previousData)
	})
	callTool := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := meta.GetProvider().ExecuteTool(ctx, "call_tool", map[string]any{
			"name":      f.agg.registry.ExposedToolPrefix(answerServer) + "op",
			"arguments": map[string]any{},
		})
		return convertToMCPResult(result), err
	}
	ctx := mcpserver.WithRequestProtocolInfo(api.WithSubject(api.WithSessionID(t.Context(), answerSession), answerSubject), &mcpserver.RequestProtocolInfo{
		Modern:             true,
		ProtocolVersion:    mcp.ProtocolVersion20260728,
		ClientCapabilities: new(urlElicitationCapabilities()),
	})

	result, err := f.agg.urlElicitation()(callTool)(ctx, mcp.CallToolRequest{})

	require.NoError(t, err)
	require.Equal(t, mcp.ResultTypeInputRequired, result.ResultType)
	request := result.InputRequests[signInInputKey]
	require.NotNil(t, request.Elicitation)
	require.Contains(t, request.Elicitation.URL, "/oauth/proxy/start")
	require.Equal(t, answerServer, result.RequestState)
	require.Empty(t, result.Content, "the call_tool envelope carries the link too")
}

// acceptedRetry is a 2026-07-28 retry of a call whose sign-in elicitation the
// person accepted.
func acceptedRetry() mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.InputResponses = mcp.InputResponses{
		signInInputKey: mcp.NewElicitationInputResponse(mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept}}),
	}
	req.Params.RequestState = "github"
	return req
}

// gatedHandler answers like a gated tool call: done once the session is
// authenticated to the server, muster's challenge until then.
func gatedHandler(store oauthstore.SessionAuthStore) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if authenticated, _ := store.IsAuthenticated(ctx, "family-1", "github"); authenticated {
			return mcp.NewToolResultText("done"), nil
		}
		return signInHandler(true)(ctx, req)
	}
}

func TestURLElicitation_AcceptedRetryWaitsForTheSignIn(t *testing.T) {
	t.Run("the call runs once the sign-in completes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			store := oauthstore.NewInMemorySessionAuthStore(time.Hour)
			a := &AggregatorServer{authStore: store}
			ctx := modernContext(t.Context(), urlElicitationCapabilities())
			go func() {
				time.Sleep(30 * time.Second)
				_ = store.MarkAuthenticated(context.Background(), "family-1", "github")
			}()
			start := time.Now()
			calls := 0
			counted := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls++
				require.Empty(t, req.Params.InputResponses, "the tool sees a plain call")
				return gatedHandler(store)(ctx, req)
			}

			result, err := a.urlElicitation()(counted)(ctx, acceptedRetry())

			require.NoError(t, err)
			require.Equal(t, "done", mcpText(result))
			require.Equal(t, 1, calls, "the call runs once, after the wait: no login is spent before it")
			require.Less(t, time.Since(start), 32*time.Second, "the retry is answered as soon as the sign-in completes")
		})
	})

	t.Run("a sign-in that does not complete is asked again", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			a := &AggregatorServer{authStore: oauthstore.NewInMemorySessionAuthStore(time.Hour)}
			ctx := modernContext(t.Context(), urlElicitationCapabilities())
			start := time.Now()

			result, err := a.urlElicitation()(gatedHandler(a.authStore))(ctx, acceptedRetry())

			require.NoError(t, err)
			require.Equal(t, mcp.ResultTypeInputRequired, result.ResultType)
			require.Equal(t, signInRetryWait, time.Since(start))
		})
	})

	t.Run("a cancelled call stops waiting", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			a := &AggregatorServer{authStore: oauthstore.NewInMemorySessionAuthStore(time.Hour)}
			ctx, cancel := context.WithTimeout(modernContext(t.Context(), urlElicitationCapabilities()), time.Minute)
			defer cancel()
			start := time.Now()

			_, _ = a.urlElicitation()(gatedHandler(a.authStore))(ctx, acceptedRetry())

			require.Equal(t, time.Minute, time.Since(start))
		})
	})
}
