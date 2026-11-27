package testing

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

func signInRequired(state string) *mcp.CallToolResult {
	return mcpserver.NewInputRequestBuilder(state).Elicit("muster_sign_in", mcp.ElicitationParams{
		Mode:    mcp.ElicitationModeURL,
		Message: "Sign in to github to continue.",
		URL:     "https://dex.example.com/auth",
	}).ToolResult()
}

// scriptedCall answers each call with the next result and records the
// round-trip parameters it was called with.
type scriptedCall struct {
	results    []*mcp.CallToolResult
	roundTrips []mcp.MultiRoundTripParams
}

func (s *scriptedCall) call(_ context.Context, roundTrip mcp.MultiRoundTripParams) (*mcp.CallToolResult, error) {
	s.roundTrips = append(s.roundTrips, roundTrip)
	result := s.results[0]
	if len(s.results) > 1 {
		s.results = s.results[1:]
	}
	return result, nil
}

func TestElicitationAnswerRetriesWithTheStepsActionAndTheStateVerbatim(t *testing.T) {
	for _, action := range elicitationActions {
		t.Run(string(action), func(t *testing.T) {
			done := mcp.NewToolResultText("done")
			call := &scriptedCall{results: []*mcp.CallToolResult{signInRequired("opaque state"), done}}

			result, err := callWithElicitationAnswer(WithElicitationAnswer(t.Context(), action), call.call)
			require.NoError(t, err)
			require.Same(t, done, result)
			require.Len(t, call.roundTrips, 2)
			require.Empty(t, call.roundTrips[0].InputResponses)
			require.Equal(t, "opaque state", call.roundTrips[1].RequestState)
			require.Equal(t, action, call.roundTrips[1].InputResponses["muster_sign_in"].Elicitation.Action)
		})
	}
}

func TestElicitationAnswerFailsWhenTheCallAskedForNone(t *testing.T) {
	call := &scriptedCall{results: []*mcp.CallToolResult{mcp.NewToolResultText("done")}}

	_, err := callWithElicitationAnswer(WithElicitationAnswer(t.Context(), mcp.ElicitationResponseActionDecline), call.call)
	require.ErrorContains(t, err, "asked for none")
}

func TestElicitationUnansweredFailsWithTheRequest(t *testing.T) {
	call := &scriptedCall{results: []*mcp.CallToolResult{signInRequired("s")}}

	_, err := callWithElicitationAnswer(t.Context(), call.call)
	require.ErrorContains(t, err, `muster_sign_in: elicitation/create url "Sign in to github to continue." https://dex.example.com/auth`)
	require.Len(t, call.roundTrips, 1)
}

func TestElicitationAnswerStopsAfterTheBound(t *testing.T) {
	call := &scriptedCall{results: []*mcp.CallToolResult{signInRequired("s")}}

	_, err := callWithElicitationAnswer(WithElicitationAnswer(t.Context(), mcp.ElicitationResponseActionAccept), call.call)
	require.ErrorContains(t, err, "still asks for input")
	require.Len(t, call.roundTrips, maxElicitationRoundTrips+1)
}

func TestElicitationAnswerSendsTheLastAllowedAnswer(t *testing.T) {
	done := mcp.NewToolResultText("done")
	call := &scriptedCall{}
	for range maxElicitationRoundTrips {
		call.results = append(call.results, signInRequired("s"))
	}
	call.results = append(call.results, done)

	result, err := callWithElicitationAnswer(WithElicitationAnswer(t.Context(), mcp.ElicitationResponseActionAccept), call.call)
	require.NoError(t, err)
	require.Same(t, done, result)
	require.Len(t, call.roundTrips, maxElicitationRoundTrips+1)
}

func TestElicitationAnswerRefusesAFormElicitation(t *testing.T) {
	form := mcpserver.NewInputRequestBuilder("s").Elicit("name", mcp.ElicitationParams{Message: "name?"}).ToolResult()
	call := &scriptedCall{results: []*mcp.CallToolResult{form}}

	_, err := callWithElicitationAnswer(WithElicitationAnswer(t.Context(), mcp.ElicitationResponseActionAccept), call.call)
	require.ErrorContains(t, err, `input request "name" is not a URL mode elicitation`)
}

// TestClientDeclaresURLElicitationOnlyForAStepThatAnswers runs the test
// client against a server that answers as muster does: an input_required
// sign-in to a request declaring elicitation.url, a plain answer otherwise.
func TestClientDeclaresURLElicitationOnlyForAStepThatAnswers(t *testing.T) {
	var versions []string
	mcpServer := mcpserver.NewMCPServer("muster", "1.0.0")
	mcpServer.AddTool(mcp.NewTool("call_tool"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		info := mcpserver.RequestProtocolInfoFromContext(ctx)
		versions = append(versions, mcpserver.RequestProtocolVersion(ctx))
		declared := info.ClientCapabilities != nil && info.ClientCapabilities.Elicitation != nil && info.ClientCapabilities.Elicitation.URL != nil
		if !declared {
			return mcp.NewToolResultText(`{"isError":false,"content":[{"type":"text","text":"auth_required"}]}`), nil
		}
		if answer := mcpserver.ElicitationResponse(req.Params.InputResponses, "muster_sign_in"); answer != nil && req.Params.RequestState == "opaque" {
			return mcp.NewToolResultError("The tool did not run: the person answered " + string(answer.Action) + " to signing in to github."), nil
		}
		return signInRequired("opaque"), nil
	})
	ts := httptest.NewServer(mcpserver.NewStreamableHTTPServer(mcpServer))
	t.Cleanup(ts.Close)

	legacy := NewMCPTestClient(false)
	require.NoError(t, legacy.Connect(t.Context(), ts.URL+"/mcp"))
	require.Equal(t, api.ClientProtocolVersion, legacy.InitializeResult().ProtocolVersion)
	require.NoError(t, legacy.Close())

	c := NewMCPTestClient(false)
	require.NoError(t, c.Connect(WithClientProtocolVersion(t.Context(), api.MultiRoundTripProtocolVersion), ts.URL+"/mcp"))
	t.Cleanup(func() { _ = c.Close() })
	require.Equal(t, api.MultiRoundTripProtocolVersion, c.InitializeResult().ProtocolVersion)

	result, err := c.CallTool(t.Context(), "x_github_list", nil)
	require.NoError(t, err)
	require.Equal(t, "auth_required", result.(*mcp.CallToolResult).Content[0].(mcp.TextContent).Text)

	_, err = c.CallTool(WithElicitationAnswer(t.Context(), mcp.ElicitationResponseActionDecline), "x_github_list", nil)
	require.ErrorContains(t, err, "the person answered decline to signing in to github")
	require.Equal(t, []string{api.MultiRoundTripProtocolVersion, api.MultiRoundTripProtocolVersion, api.MultiRoundTripProtocolVersion}, versions)
}

func TestValidateStepElicitation(t *testing.T) {
	loader := &scenarioLoader{}
	step := TestStep{ID: "s", Tool: "x_github_list", Elicitation: mcp.ElicitationResponseActionDecline}
	require.NoError(t, loader.validateStep(step, 0))

	step.Elicitation = "ignore"
	require.ErrorContains(t, loader.validateStep(step, 0), "elicitation must be one of")

	step = TestStep{ID: "s", Tool: "test_inject_token", Elicitation: mcp.ElicitationResponseActionAccept}
	require.ErrorContains(t, loader.validateStep(step, 0), "run by the test runner")
}

func TestValidateScenarioProtocolVersion(t *testing.T) {
	loader := &scenarioLoader{}
	scenario := TestScenario{Name: "n", Category: "behavioral", Concept: "oauth", Steps: []TestStep{
		{ID: "s", Tool: "x_gh_whoami", Elicitation: mcp.ElicitationResponseActionDecline},
	}}
	require.ErrorContains(t, loader.validateScenario(scenario, "f.yaml"), "elicitation needs protocol_version")

	scenario.ProtocolVersion = api.MultiRoundTripProtocolVersion
	require.NoError(t, loader.validateScenario(scenario, "f.yaml"))

	scenario.ProtocolVersion = "2024-11-05"
	require.ErrorContains(t, loader.validateScenario(scenario, "f.yaml"), "protocol_version must be")
}
