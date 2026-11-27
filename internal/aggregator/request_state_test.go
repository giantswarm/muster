package aggregator

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"testing"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"
	"github.com/giantswarm/mcp-oauth/requeststate"
	"github.com/giantswarm/mcp-toolkit/urlelicitation"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

func testCaller() *providers.UserInfo {
	return &providers.UserInfo{Issuer: "https://muster.example", ID: "jane", ClientID: "client-1"}
}

// withTestCaller is a request of testCaller, validated by muster's inbound
// OAuth.
func withTestCaller(ctx context.Context) context.Context {
	return oauthhandler.ContextWithUserInfo(ctx, testCaller())
}

func testRequestStateSealer(t *testing.T) *requeststate.Sealer {
	t.Helper()
	sealer, err := newRequestStateSealer(config.OAuthServerConfig{})
	require.NoError(t, err)
	return sealer
}

// elicitingAggregator is an aggregator that seals requestState.
func elicitingAggregator(t *testing.T) *AggregatorServer {
	t.Helper()
	return &AggregatorServer{requestStates: testRequestStateSealer(t)}
}

// openedState opens the requestState a tool call was answered with for the
// test caller and the call.
func openedState(t *testing.T, a *AggregatorServer, ctx context.Context, req mcp.CallToolRequest, result *mcp.CallToolResult) *sealedRequestState {
	t.Helper()
	req.Params.RequestState = result.RequestState
	state, err := a.openRequestState(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, state)
	return state
}

// sealedFor seals state as muster does for the caller in ctx and the call.
func sealedFor(t *testing.T, a *AggregatorServer, ctx context.Context, req mcp.CallToolRequest, state sealedRequestState) string {
	t.Helper()
	sealed, err := a.sealRequestState(ctx, req, state)
	require.NoError(t, err)
	return sealed
}

func newKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(key)
}

func TestNewRequestStateSealer(t *testing.T) {
	for _, value := range []byte{0, 'a'} {
		t.Run(fmt.Sprintf("rejects repeated root byte %d", value), func(t *testing.T) {
			key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
			_, err := newRequestStateSealer(config.OAuthServerConfig{EncryptionKey: key})
			require.Error(t, err)
		})
	}
	t.Run("replicas with one encryption key open each other's state", func(t *testing.T) {
		key := newKey(t)
		one, err := newRequestStateSealer(config.OAuthServerConfig{EncryptionKey: key})
		require.NoError(t, err)
		two, err := newRequestStateSealer(config.OAuthServerConfig{EncryptionKey: key})
		require.NoError(t, err)

		sealed, err := one.Seal(testCaller(), "call", []byte("state"), requestStateTTL)
		require.NoError(t, err)
		opened, err := two.Open(testCaller(), "call", sealed)
		require.NoError(t, err)
		require.Equal(t, "state", string(opened))
	})

	t.Run("the requestState key is not the encryption key", func(t *testing.T) {
		key := newKey(t)
		raw, err := base64.StdEncoding.DecodeString(key)
		require.NoError(t, err)
		derived, err := requestStateSecret(key)
		require.NoError(t, err)
		require.Len(t, derived, 32)
		require.NotEqual(t, raw, derived)
	})

	t.Run("another encryption key does not open the state", func(t *testing.T) {
		one, err := newRequestStateSealer(config.OAuthServerConfig{EncryptionKey: newKey(t)})
		require.NoError(t, err)
		two, err := newRequestStateSealer(config.OAuthServerConfig{EncryptionKey: newKey(t)})
		require.NoError(t, err)

		sealed, err := one.Seal(testCaller(), "call", []byte("state"), requestStateTTL)
		require.NoError(t, err)
		_, err = two.Open(testCaller(), "call", sealed)
		require.ErrorIs(t, err, requeststate.ErrInvalid)
	})

	t.Run("two replicas without a key do not open each other's state", func(t *testing.T) {
		one, two := testRequestStateSealer(t), testRequestStateSealer(t)

		sealed, err := one.Seal(testCaller(), "call", []byte("state"), requestStateTTL)
		require.NoError(t, err)
		_, err = two.Open(testCaller(), "call", sealed)
		require.ErrorIs(t, err, requeststate.ErrInvalid)
	})

	t.Run("refuses an encryption key that is not 32 bytes", func(t *testing.T) {
		_, err := newRequestStateSealer(config.OAuthServerConfig{EncryptionKey: base64.StdEncoding.EncodeToString([]byte("short"))})
		require.Error(t, err)
	})

	t.Run("a requestState outlives the wait of a sign-in retry", func(t *testing.T) {
		require.Greater(t, requestStateTTL, signInRetryWait)
	})
}

func TestRequestStateCaller(t *testing.T) {
	t.Run("the validated user of muster's inbound OAuth", func(t *testing.T) {
		require.Equal(t, testCaller(), requestStateCaller(withTestCaller(t.Context())))
	})

	t.Run("the one local person of a muster without inbound OAuth, in their session", func(t *testing.T) {
		ctx := api.WithSessionID(api.WithSubject(t.Context(), stdioDefaultUser), stdioDefaultUser)
		require.Equal(t, &providers.UserInfo{Issuer: localCaller, ID: stdioDefaultUser, ClientID: localCaller}, requestStateCaller(ctx))
	})

	t.Run("nobody otherwise", func(t *testing.T) {
		require.Nil(t, requestStateCaller(api.WithSubject(t.Context(), "jane")))
	})
}

func TestRequestStateBinding(t *testing.T) {
	a := elicitingAggregator(t)
	ctx := withTestCaller(t.Context())
	call := mcp.CallToolRequest{}
	call.Params.Name = "x_github_list_issues"
	call.Params.Arguments = map[string]any{"repo": "muster", "state": "open"}
	state := sealedRequestState{Kind: stateKindSignIn, SignIn: &signInRequestState{Server: "github", Link: testAuthURL}}
	sealed := sealedFor(t, a, ctx, call, state)

	retry := func(ctx context.Context, mutate func(*mcp.CallToolRequest)) (*sealedRequestState, error) {
		req := call
		req.Params.Arguments = map[string]any{"state": "open", "repo": "muster"}
		req.Params.RequestState = sealed
		if mutate != nil {
			mutate(&req)
		}
		return a.openRequestState(ctx, req)
	}

	t.Run("the same caller retrying the same call opens it", func(t *testing.T) {
		opened, err := retry(ctx, nil)
		require.NoError(t, err)
		require.Equal(t, &state, opened)
	})

	refused := map[string]struct {
		ctx    context.Context
		mutate func(*mcp.CallToolRequest)
	}{
		"another person":                 {ctx: oauthhandler.ContextWithUserInfo(t.Context(), &providers.UserInfo{Issuer: "https://muster.example", ID: "john", ClientID: "client-1"})},
		"another client":                 {ctx: oauthhandler.ContextWithUserInfo(t.Context(), &providers.UserInfo{Issuer: "https://muster.example", ID: "jane", ClientID: "client-2"})},
		"a caller it cannot be bound to": {ctx: t.Context()},
		"another tool": {ctx: ctx, mutate: func(req *mcp.CallToolRequest) {
			req.Params.Name = "x_github_merge_pull_request"
		}},
		"other arguments": {ctx: ctx, mutate: func(req *mcp.CallToolRequest) {
			req.Params.Arguments = map[string]any{"repo": "mcp-oauth", "state": "open"}
		}},
		"a changed state": {ctx: ctx, mutate: func(req *mcp.CallToolRequest) {
			req.Params.RequestState = sealed[:len(sealed)-4] + "AAAA"
		}},
		"a state muster did not seal": {ctx: ctx, mutate: func(req *mcp.CallToolRequest) {
			req.Params.RequestState = `{"server":"github","link":"https://evil.example"}`
		}},
	}
	for name, tc := range refused {
		t.Run(name+" is refused with invalid params", func(t *testing.T) {
			_, err := retry(tc.ctx, tc.mutate)
			requireInvalidParams(t, err)
		})
	}

	t.Run("a lapsed state is a first call", func(t *testing.T) {
		lapsed, err := a.requestStates.Seal(testCaller(), mustBinding(t, call), []byte(`{"kind":"signIn","signIn":{}}`), 1)
		require.NoError(t, err)
		opened, err := retry(ctx, func(req *mcp.CallToolRequest) { req.Params.RequestState = lapsed })
		require.NoError(t, err)
		require.Nil(t, opened)
	})

	t.Run("a caller without a validated identity cannot be sealed for", func(t *testing.T) {
		_, err := a.sealRequestState(t.Context(), call, state)
		require.ErrorIs(t, err, requeststate.ErrIncompleteIdentity)
	})

	t.Run("a token naming no single client cannot be sealed for", func(t *testing.T) {
		_, err := a.sealRequestState(oauthhandler.ContextWithUserInfo(t.Context(), &providers.UserInfo{Issuer: "https://dex.example", ID: "jane"}), call, state)
		require.ErrorIs(t, err, requeststate.ErrMissingClientID)
	})
}

func mustBinding(t *testing.T, req mcp.CallToolRequest) string {
	t.Helper()
	binding, err := urlelicitation.RequestBinding(req)
	require.NoError(t, err)
	return binding
}

func requireInvalidParams(t *testing.T, err error) {
	t.Helper()
	require.ErrorIs(t, err, errInvalidRequestState)
	var provider mcpserver.JSONRPCErrorProvider
	require.True(t, errors.As(err, &provider))
	require.Equal(t, mcp.INVALID_PARAMS, provider.JSONRPCError().Error.Code)
}

func TestRejectedRequestStateIsAuditedWithoutState(t *testing.T) {
	var logs bytes.Buffer
	logging.InitForCLI(logging.LevelWarn, &logs)
	t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, os.Stderr) })
	aggregator := elicitingAggregator(t)
	request := mcp.CallToolRequest{}
	request.Params.Name = "create"
	request.Params.RequestState = "opaque-secret-not-to-log"
	_, err := aggregator.openRequestState(withTestCaller(t.Context()), request)
	require.ErrorIs(t, err, errInvalidRequestState)
	require.Contains(t, logs.String(), "event=request_state_rejected")
	require.Contains(t, logs.String(), "reason=verification_failed")
	require.Contains(t, logs.String(), "tool=create")
	require.Contains(t, logs.String(), "caller.issuer=https://muster.example")
	require.NotContains(t, logs.String(), request.Params.RequestState)
}
