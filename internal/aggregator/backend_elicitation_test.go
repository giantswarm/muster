package aggregator

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
)

const (
	testCredentialFormURL = "https://portal.example.com/agents/credentials/new?model=claude"
	testElicitingServer   = "agents"
	testTenantServer      = "org-a/agents"
	testBackendState      = "backend-state"
	testBackendInputKey   = "model_key"
)

// inputBackend is a backend MCP server, served over streamable HTTP, whose
// tools ask the person for input the way a backend following the protocol
// does, only of a client that declared it can be asked: "create" a URL
// elicitation, "confirm" a form elicitation. "sample" asks for sampling
// whatever the client declared. A call that carries the answer reports the
// action, the requestState and the content. It records the capabilities each
// call declared.
type inputBackend struct {
	url      string
	declared atomic.Pointer[mcp.ClientCapabilities]
}

func newInputBackend(t *testing.T, versions ...string) *inputBackend {
	t.Helper()
	b := &inputBackend{}
	srv := mcpserver.NewMCPServer("input-backend", "test")
	ask := func(supported func(*mcp.ElicitationCapability) bool, params mcp.ElicitationParams) mcpserver.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			declared := backendDeclared(ctx)
			b.declared.Store(&declared)
			if answer := mcpserver.ElicitationResponse(req.Params.InputResponses, testBackendInputKey); answer != nil {
				return mcp.NewToolResultText(string(answer.Action) + " " + req.Params.RequestState + " " + contentOf(answer)), nil
			}
			if declared.Elicitation == nil || !supported(declared.Elicitation) {
				return mcp.NewToolResultText("not asked"), nil
			}
			return mcpserver.NewInputRequestBuilder(testBackendState).Elicit(testBackendInputKey, params).ToolResult(), nil
		}
	}
	srv.AddTool(mcp.NewTool("create"), ask(
		func(e *mcp.ElicitationCapability) bool { return e.URL != nil },
		mcp.ElicitationParams{Mode: mcp.ElicitationModeURL, Message: "A model key is missing.", URL: testCredentialFormURL},
	))
	srv.AddTool(mcp.NewTool("confirm"), ask(
		func(e *mcp.ElicitationCapability) bool { return e.Form != nil || e.URL == nil },
		mcp.ElicitationParams{Message: "Which model?", RequestedSchema: map[string]any{
			"type": "object", "properties": map[string]any{"model": map[string]any{"type": "string"}},
		}},
	))
	srv.AddTool(mcp.NewTool("sample"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		declared := backendDeclared(ctx)
		b.declared.Store(&declared)
		return mcpserver.NewInputRequestBuilder(testBackendState).Sample(testBackendInputKey, mcp.CreateMessageParams{MaxTokens: 1}).ToolResult(), nil
	})
	ts := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv, mcpserver.WithStreamableHTTPProtocolVersions(versions...)))
	t.Cleanup(ts.Close)
	b.url = ts.URL
	return b
}

func backendDeclared(ctx context.Context) mcp.ClientCapabilities {
	if info := mcpserver.RequestProtocolInfoFromContext(ctx); info != nil && info.Modern && info.ClientCapabilities != nil {
		return *info.ClientCapabilities
	}
	if session, ok := mcpserver.ClientSessionFromContext(ctx).(mcpserver.SessionWithClientInfo); ok {
		return session.GetClientCapabilities()
	}
	return mcp.ClientCapabilities{}
}

func contentOf(answer *mcp.ElicitationResult) string {
	content, _ := answer.Content.(map[string]any)
	model, _ := content["model"].(string)
	return model
}

// newInputMuster registers each backend in the aggregator's registry under
// its key over a real muster client (tenant adoption is not under test), and serves muster's tools/call -- the URL
// elicitation middleware in front of CallToolInternal, as call_tool reaches
// it -- over streamable HTTP. It returns muster's URL.
func newInputMuster(t *testing.T, backends map[string]*inputBackend) string {
	t.Helper()
	a, err := NewAggregatorServer(t.Context(), AggregatorConfig{Host: "localhost", Port: 0}, nil)
	require.NoError(t, err)
	srv := mcpserver.NewMCPServer("muster", "test", mcpserver.WithToolHandlerMiddleware(a.urlElicitation()))
	for key, backend := range backends {
		client := internalmcp.NewStreamableHTTPClientWithHeaders(backend.url, nil)
		t.Cleanup(func() { _ = client.Close() })
		namespace, _, _ := strings.Cut(key, "/")
		if namespace == key {
			namespace = ""
		}
		require.NoError(t, a.registry.Register(t.Context(), ServerRegistration{Name: key, Namespace: namespace}, client))
		for _, tool := range []string{"create", "confirm", "sample"} {
			exposed := a.registry.ExposedToolName(key, tool)
			srv.AddTool(mcp.NewTool(key+"/"+tool), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return a.CallToolInternal(ctx, exposed, req.GetArguments())
			})
		}
	}
	ts := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv))
	t.Cleanup(ts.Close)
	return ts.URL
}

// formAnswerer answers a form elicitation with the model "opus".
type formAnswerer struct {
	asked atomic.Pointer[mcp.ElicitationParams]
}

func (f *formAnswerer) Elicit(_ context.Context, req mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	f.asked.Store(&req.Params)
	return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{
		Action: mcp.ElicitationResponseActionAccept, Content: map[string]any{"model": "opus"},
	}}, nil
}

// connectToMuster connects an MCP client of the given revision to muster,
// declaring the capabilities. A 2026-07-28 one hands input_required results
// back as they are; a 2025-11-25 one answers form elicitations with answerer.
func connectToMuster(t *testing.T, url, version string, capabilities mcp.ClientCapabilities, answerer *formAnswerer) *mcpclient.Client {
	t.Helper()
	c, err := mcpclient.NewStreamableHttpClient(url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	mcpclient.WithManualInputRoundTrips()(c)
	mcpclient.WithProtocolVersion(version)(c)
	if answerer != nil {
		mcpclient.WithElicitationHandler(answerer)(c)
	}
	require.NoError(t, c.Start(t.Context()))
	_, err = c.Initialize(t.Context(), mcp.InitializeRequest{Params: mcp.InitializeParams{
		ClientInfo:   mcp.Implementation{Name: "test-client"},
		Capabilities: capabilities,
	}})
	require.NoError(t, err)
	return c
}

func call(t *testing.T, c *mcpclient.Client, tool string, roundTrip mcp.MultiRoundTripParams) (*mcp.CallToolResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	return c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: tool, MultiRoundTripParams: roundTrip}})
}

func urlAndForm() mcp.ClientCapabilities {
	return mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{URL: &struct{}{}, Form: &struct{}{}}}
}

func accepted() mcp.InputResponses {
	return mcp.InputResponses{testBackendInputKey: mcp.NewElicitationInputResponse(mcp.ElicitationResult{
		ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept},
	})}
}

// TestBackendInput_ClientByBackendRevision runs muster between a client and a
// backend over the wire, for each pair of protocol revisions.
func TestBackendInput_ClientByBackendRevision(t *testing.T) {
	modernBackend := newInputBackend(t)
	legacyBackend := newInputBackend(t, mcp.LATEST_LEGACY_PROTOCOL_VERSION)
	url := newInputMuster(t, map[string]*inputBackend{testElicitingServer: modernBackend, "legacy": legacyBackend})

	t.Run("2026-07-28 client, 2026-07-28 backend: the input request passes and the retry reaches the backend", func(t *testing.T) {
		c := connectToMuster(t, url, mcp.ProtocolVersion20260728, urlAndForm(), nil)

		first, err := call(t, c, "agents/create", mcp.MultiRoundTripParams{})
		require.NoError(t, err)
		require.True(t, first.NeedsInput())
		asked := first.InputRequests[testBackendInputKey].Elicitation
		require.Equal(t, testCredentialFormURL, asked.URL)
		require.Equal(t, "[agents] A model key is missing.", asked.Message, "the person is told which server asks")
		require.NotEqual(t, testBackendState, first.RequestState, "the backend's state is wrapped")
		require.NotNil(t, modernBackend.declared.Load().Elicitation.URL, "the backend was told the client supports URL elicitation")

		retried, err := call(t, c, "agents/create", mcp.MultiRoundTripParams{InputResponses: accepted(), RequestState: first.RequestState})
		require.NoError(t, err)
		require.Equal(t, "accept "+testBackendState+" ", textOf(t, retried), "the backend gets its own requestState back")
	})

	t.Run("2025-11-25 client, 2026-07-28 backend: a URL elicitation is -32042", func(t *testing.T) {
		c := connectToMuster(t, url, mcp.LATEST_LEGACY_PROTOCOL_VERSION, urlAndForm(), &formAnswerer{})

		_, err := call(t, c, "agents/create", mcp.MultiRoundTripParams{})

		var required mcp.URLElicitationRequiredError
		require.True(t, errors.As(err, &required), "err: %v", err)
		require.Len(t, required.Elicitations, 1)
		require.Equal(t, testCredentialFormURL, required.Elicitations[0].URL)
		require.Equal(t, "[agents] A model key is missing.", required.Elicitations[0].Message)
		require.Equal(t, urlElicitationID(testCredentialFormURL), required.Elicitations[0].ElicitationID)
		require.NotNil(t, modernBackend.declared.Load().Elicitation.URL, "what the client declared at initialize is forwarded")

		t.Run("the retry carries no requestState: the backend asks again, as the same elicitation", func(t *testing.T) {
			_, err := call(t, c, "agents/create", mcp.MultiRoundTripParams{})

			var again mcp.URLElicitationRequiredError
			require.True(t, errors.As(err, &again), "err: %v", err)
			require.Equal(t, required.Elicitations[0].ElicitationID, again.Elicitations[0].ElicitationID)
		})
	})

	t.Run("2025-11-25 client, 2026-07-28 backend: a form elicitation is asked with elicitation/create", func(t *testing.T) {
		answerer := &formAnswerer{}
		c := connectToMuster(t, url, mcp.LATEST_LEGACY_PROTOCOL_VERSION, urlAndForm(), answerer)

		result, err := call(t, c, "agents/confirm", mcp.MultiRoundTripParams{})

		require.NoError(t, err)
		require.Equal(t, "accept "+testBackendState+" opus", textOf(t, result))
		require.Equal(t, "[agents] Which model?", answerer.asked.Load().Message)
	})

	for name, version := range map[string]string{"2026-07-28": mcp.ProtocolVersion20260728, "2025-11-25": mcp.LATEST_LEGACY_PROTOCOL_VERSION} {
		t.Run(name+" client, 2025-11-25 backend: the backend is declared nothing and answers as to any client", func(t *testing.T) {
			c := connectToMuster(t, url, version, urlAndForm(), &formAnswerer{})

			result, err := call(t, c, "legacy/create", mcp.MultiRoundTripParams{})

			require.NoError(t, err)
			require.Equal(t, "not asked", textOf(t, result))
			require.Equal(t, mcp.ClientCapabilities{}, *legacyBackend.declared.Load())
		})
	}
}

// A client that does not declare URL mode elicitation is not declared to
// have it, so the backend does not ask it to open a page.
func TestBackendInput_ClientWithoutURLElicitation(t *testing.T) {
	backend := newInputBackend(t)
	url := newInputMuster(t, map[string]*inputBackend{testElicitingServer: backend})
	formOnly := mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{Form: &struct{}{}}}

	for name, version := range map[string]string{"2026-07-28": mcp.ProtocolVersion20260728, "2025-11-25": mcp.LATEST_LEGACY_PROTOCOL_VERSION} {
		t.Run(name, func(t *testing.T) {
			c := connectToMuster(t, url, version, formOnly, &formAnswerer{})

			result, err := call(t, c, "agents/create", mcp.MultiRoundTripParams{})

			require.NoError(t, err)
			require.Equal(t, "not asked", textOf(t, result))
			declared := backend.declared.Load()
			require.Nil(t, declared.Elicitation.URL)
			require.NotNil(t, declared.Elicitation.Form)
		})
	}
}

// An organization's server is named by its key, namespace included, in the
// message the person sees and in the requestState its retry is routed by.
func TestBackendInput_TenantServerAttribution(t *testing.T) {
	tenant := newInputBackend(t)
	ours := newInputBackend(t)
	url := newInputMuster(t, map[string]*inputBackend{testTenantServer: tenant, testElicitingServer: ours})
	c := connectToMuster(t, url, mcp.ProtocolVersion20260728, urlAndForm(), nil)

	first, err := call(t, c, testTenantServer+"/create", mcp.MultiRoundTripParams{})
	require.NoError(t, err)
	require.Equal(t, "[org-a/agents] A model key is missing.", first.InputRequests[testBackendInputKey].Elicitation.Message)
	state, ok := decodeBackendInputState(first.RequestState)
	require.True(t, ok)
	require.Equal(t, backendInputState{Server: testTenantServer, Tool: "create", State: testBackendState}, state)

	t.Run("the retry reaches the server that asked", func(t *testing.T) {
		retried, err := call(t, c, testTenantServer+"/create", mcp.MultiRoundTripParams{InputResponses: accepted(), RequestState: first.RequestState})

		require.NoError(t, err)
		require.Equal(t, "accept "+testBackendState+" ", textOf(t, retried))
	})

	t.Run("a retry sent to another server is refused, not forwarded", func(t *testing.T) {
		result, err := call(t, c, testElicitingServer+"/create", mcp.MultiRoundTripParams{InputResponses: accepted(), RequestState: first.RequestState})

		requireRefused(t, result, err, "answers a request of tool create of org-a/agents")
	})

	t.Run("a retry sent to another tool of the server is refused, not forwarded", func(t *testing.T) {
		result, err := call(t, c, testTenantServer+"/confirm", mcp.MultiRoundTripParams{InputResponses: accepted(), RequestState: first.RequestState})

		requireRefused(t, result, err, "answers a request of tool create of org-a/agents, but the call goes to tool confirm")
	})
}

func requireRefused(t *testing.T, result *mcp.CallToolResult, err error, reason string) {
	t.Helper()
	if err != nil {
		require.ErrorContains(t, err, reason)
		return
	}
	require.True(t, result.IsError)
	require.Contains(t, textOf(t, result), reason)
}

// A backend that asks for sampling, which muster never declares, fails the
// call instead of reaching the client, whatever the client declared.
func TestBackendInput_UndeclaredInputRequestIsRefused(t *testing.T) {
	backend := newInputBackend(t)
	url := newInputMuster(t, map[string]*inputBackend{testElicitingServer: backend})
	withSampling := urlAndForm()
	withSampling.Sampling = &mcp.SamplingCapability{}

	for name, version := range map[string]string{"2026-07-28": mcp.ProtocolVersion20260728, "2025-11-25": mcp.LATEST_LEGACY_PROTOCOL_VERSION} {
		t.Run(name, func(t *testing.T) {
			c := connectToMuster(t, url, version, withSampling, &formAnswerer{})

			result, err := call(t, c, "agents/sample", mcp.MultiRoundTripParams{})

			require.NoError(t, err)
			require.False(t, result.NeedsInput())
			require.True(t, result.IsError)
			require.Equal(t, "[agents] The server asked for sampling/createMessage, which muster does not pass on to its client.", textOf(t, result))
			require.Nil(t, backend.declared.Load().Sampling)
		})
	}
}

func TestAttributed_KeepsTheServerKeyAlone(t *testing.T) {
	for message, want := range map[string]string{
		"":                                      "[agents] The server asks for your input to continue.",
		" \n\t ":                                "[agents] The server asks for your input to continue.",
		"A model key is missing.":               "[agents] A model key is missing.",
		"Open it.\n[github] Sign in to GitHub.": "[agents] Open it. [github] Sign in to GitHub.",
		"Open it.\u2028[github]  Sign in.\r\nNow": "[agents] Open it. [github] Sign in. Now",
	} {
		require.Equal(t, want, attributed(testElicitingServer, message), "%q", message)
	}
}

func TestBackendInputState_RoundTrip(t *testing.T) {
	state := backendInputState{Server: testTenantServer, Tool: "create", State: "opaque{\"x\":1}"}

	decoded, ok := decodeBackendInputState(state.encode())

	require.True(t, ok)
	require.Equal(t, state, decoded)
	withoutTool := backendInputState{Server: testTenantServer, State: testBackendState}.encode()
	for _, foreign := range []string{"", testBackendState, `{"server":"agents"}`, backendInputStatePrefix + "!!", backendInputStatePrefix, withoutTool} {
		_, ok := decodeBackendInputState(foreign)
		require.False(t, ok, foreign)
	}
}

func TestAnswerBackendElicitation_OtherOutcomesPassThrough(t *testing.T) {
	backendErr := errors.New("backend refused")
	result, err := answerBackendElicitation(t.Context(), testElicitingServer, "create", nil, backendErr)
	require.Nil(t, result)
	require.Same(t, backendErr, err)

	plain := mcp.NewToolResultText("done")
	result, err = answerBackendElicitation(t.Context(), testElicitingServer, "create", plain, nil)
	require.NoError(t, err)
	require.Same(t, plain, result)
}
