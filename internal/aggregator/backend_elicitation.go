package aggregator

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// A backend MCP server that needs something of the person while it handles a
// tool call answers, from MCP 2026-07-28, with an input_required result
// (multi round-trip requests). muster speaks that revision to every backend
// that does (internal/mcpserver client_protocol.go), declaring on each call
// the capabilities of the client it calls for, and passes the result to its
// client with the backend's requestState wrapped with the server's key
// (backendInputState), so the client's retry reaches the same server with the
// backend's own state, and with each elicitation's message prefixed with that
// key: the client only sees muster, and the person is told which server asks.
//
// A 2025-11-25 client is asked in the shape of its revision: a request made
// only of URL elicitations as -32042 URLElicitationRequiredError, which the
// client answers by calling the tool again once the pages are done; anything
// else by mcp-go's bridge, which sends the server-initiated requests of that
// revision and calls the tool again with the answers and the requestState.
//
// The answer travels like muster's own sign-in elicitation: the dispatch
// records it in the call's context (challengeRecorder) and answers with a text
// result holding the messages and links, which is what a workflow step sees;
// the urlElicitation middleware turns that answer into the input request.
// call_tool, which wraps the tool's result, passes it on this way.
//
// A backend that falls back to 2025-11-25 is declared no capability to ask
// the person: its answer stays the one it gives a client that cannot be asked.

// backendInputMarkerKey is the structuredContent key that holds the
// requestState of a backend's input_required answer in its text answer.
const backendInputMarkerKey = "inputRequired"

// backendInputStatePrefix starts every requestState muster wraps a backend's
// in, telling it apart from muster's own (signInRequestState,
// relayedRequestState).
const backendInputStatePrefix = "muster.backend."

// backendInputState is the requestState muster answers a call with when a
// 2026-07-28 backend answered it with input_required: the server's key and
// the backend's own requestState, which muster does not read.
type backendInputState struct {
	Server string `json:"server"`
	State  string `json:"state,omitempty"`
}

func (s backendInputState) encode() string {
	encoded, _ := json.Marshal(s)
	return backendInputStatePrefix + base64.RawURLEncoding.EncodeToString(encoded)
}

// decodeBackendInputState reads a requestState muster wrapped a backend's in,
// and reports whether it is one.
func decodeBackendInputState(requestState string) (backendInputState, bool) {
	encoded, ok := strings.CutPrefix(requestState, backendInputStatePrefix)
	if !ok {
		return backendInputState{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return backendInputState{}, false
	}
	var state backendInputState
	if json.Unmarshal(raw, &state) != nil || state.Server == "" {
		return backendInputState{}, false
	}
	return state, true
}

// backendInput is a 2026-07-28 backend's input_required answer as muster
// passes it on: requestState wrapped, messages prefixed.
type backendInput struct {
	server string
	result *mcp.CallToolResult
}

// recordBackendInput notes the backend input request a tool call is answered
// with, for the URL elicitation middleware. Outside a tool call it does
// nothing.
func recordBackendInput(ctx context.Context, input backendInput) {
	if recorder, ok := ctx.Value(challengeRecorderKey{}).(*challengeRecorder); ok {
		recorder.backendInput.Store(&input)
	}
}

// attributed prefixes a backend's message with the server's key, so the
// person knows which server asks.
func attributed(serverName, message string) string {
	if message == "" {
		return fmt.Sprintf("[%s] The server asks for your input to continue.", serverName)
	}
	return fmt.Sprintf("[%s] %s", serverName, message)
}

// answerBackendElicitation answers a backend tool call that the server
// answered with an input_required result. Any other outcome is returned as is.
func answerBackendElicitation(ctx context.Context, serverName string, result *mcp.CallToolResult, err error) (*mcp.CallToolResult, error) {
	if err == nil && result.NeedsInput() {
		return answerBackendInput(ctx, serverName, result), nil
	}
	return result, err
}

// answerBackendInput passes on a 2026-07-28 backend's input_required answer
// (see the top of this file) and answers the call with its text answer.
func answerBackendInput(ctx context.Context, serverName string, result *mcp.CallToolResult) *mcp.CallToolResult {
	passed := &mcp.CallToolResult{
		Result: mcp.Result{ResultType: mcp.ResultTypeInputRequired},
		MultiRoundTripResult: mcp.MultiRoundTripResult{
			InputRequests: make(mcp.InputRequests, len(result.InputRequests)),
			RequestState:  backendInputState{Server: serverName, State: result.RequestState}.encode(),
		},
	}
	var text strings.Builder
	for key, request := range result.InputRequests {
		if request.Elicitation != nil {
			elicitation := *request.Elicitation
			elicitation.Message = attributed(serverName, elicitation.Message)
			request.Elicitation = &elicitation
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(elicitation.Message)
			if elicitation.Mode == mcp.ElicitationModeURL {
				fmt.Fprintf(&text, "\nOpen %s to continue, then call the tool again.", elicitation.URL)
			}
		}
		passed.InputRequests[key] = request
	}
	if text.Len() == 0 {
		fmt.Fprintf(&text, "[%s] The server asks the client for input muster's caller can provide only through MCP.", serverName)
	}
	recordBackendInput(ctx, backendInput{server: serverName, result: passed})
	logging.InfoWithAttrs("Aggregator", "Passing on a backend's input request",
		slog.String("server", serverName),
		slog.Int("inputRequests", len(passed.InputRequests)))
	return &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent(text.String())},
		IsError:           true,
		StructuredContent: map[string]any{backendInputMarkerKey: passed.RequestState},
	}
}

// backendInputMarker returns the requestState a backend input request's text
// answer carries in structuredContent, directly or relayed by call_tool, or "".
func backendInputMarker(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	structured, _ := result.StructuredContent.(map[string]any)
	marker, _ := structured[backendInputMarkerKey].(string)
	return marker
}

type backendRoundTripKey struct{}

// pendingBackendRoundTrip is a retry's answer to a backend's input request,
// handed to the dispatch that calls that backend.
type pendingBackendRoundTrip struct {
	state     backendInputState
	responses mcp.InputResponses
	taken     atomic.Bool
}

// withBackendRoundTrip carries a retry's answer to a backend's input request
// to the dispatch of the call.
func withBackendRoundTrip(ctx context.Context, state backendInputState, responses mcp.InputResponses) context.Context {
	return context.WithValue(ctx, backendRoundTripKey{}, &pendingBackendRoundTrip{state: state, responses: responses})
}

// prepareBackendCall returns the context a tool call to the server runs
// under: the capabilities of the client the call is made for, and, on the
// retry of the server's input request, its answer and the server's own
// requestState. The answer goes to the first call to the server, once. A retry
// whose requestState names another server than the one the call is routed
// to is refused: the client's echo does not choose where its answer goes.
func prepareBackendCall(ctx context.Context, serverName string) (context.Context, error) {
	ctx = internalmcp.WithCallerCapabilities(ctx, callerCapabilities(ctx))
	pending, ok := ctx.Value(backendRoundTripKey{}).(*pendingBackendRoundTrip)
	if !ok || !pending.taken.CompareAndSwap(false, true) {
		return ctx, nil
	}
	if pending.state.Server != serverName {
		return ctx, fmt.Errorf("the retry answers a request of %s, but the call goes to %s: call the tool again without inputResponses", pending.state.Server, serverName)
	}
	return internalmcp.WithRoundTrip(ctx, mcp.MultiRoundTripParams{
		InputResponses: pending.responses,
		RequestState:   pending.state.State,
	}), nil
}

// passBackendInput answers a tool call with a 2026-07-28 backend's input
// request, in the shape of the request's protocol version. A 2026-07-28
// client gets the input_required result. A 2025-11-25 client is asked to open
// the pages of a request made only of URL elicitations with -32042, which it
// answers by calling the tool again; any other request is returned as is, and
// mcp-go asks it with the server-initiated requests of that revision and calls
// the tool again with the answers and the requestState.
func passBackendInput(ctx context.Context, input backendInput) (*mcp.CallToolResult, error) {
	if mcp.IsModernProtocol(mcpserver.RequestProtocolVersion(ctx)) {
		return input.result, nil
	}
	elicitations := make([]mcp.ElicitationParams, 0, len(input.result.InputRequests))
	for _, request := range input.result.InputRequests {
		if request.Elicitation == nil || request.Elicitation.Mode != mcp.ElicitationModeURL {
			return input.result, nil
		}
		elicitation := *request.Elicitation
		if elicitation.ElicitationID == "" {
			elicitation.ElicitationID = rand.Text()
		}
		elicitations = append(elicitations, elicitation)
	}
	if len(elicitations) == 0 || !clientSupportsURLElicitation(ctx) {
		return input.result, nil
	}
	return nil, mcp.URLElicitationRequiredError{Elicitations: elicitations}
}
