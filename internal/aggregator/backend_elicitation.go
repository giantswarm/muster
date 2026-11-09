package aggregator

import (
	"context"
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
// the elicitation capability of the client it calls for, and passes the
// result to its client with the backend's requestState wrapped with the
// server's key and the tool's name (backendInputState), so the client's retry
// reaches the same tool with the backend's own state, and with each
// elicitation's message prefixed with that key: the client only sees muster,
// and the person is told which server asks. An input request other than an
// elicitation (sampling, roots), which muster did not declare, fails the call,
// as does an elicitation in a mode the client did not declare: a request
// without a mode is a form, and a client declaring `elicitation: {}` supports
// forms only.
//
// Each URL elicitation the backend gave no elicitationId gets one, the same
// for every answer with one link (urlElicitationID): rmcp refuses a URL
// elicitation without it, on either revision.
//
// A 2025-11-25 client is asked in the shape of its revision: a request made
// only of URL elicitations as -32042 URLElicitationRequiredError, which the
// client answers by calling the tool again once the pages are done; a form
// elicitation by mcp-go's bridge, which sends elicitation/create and calls the
// tool again with the answers and the requestState. The -32042 retry carries
// no requestState, as that revision has none: the backend gets a call
// without its state and must tell from its own records whether the pages are
// done; a backend that relies on its requestState instead asks again.
// mcp-go's bridge would keep the state, but it retries as soon as the person
// agrees to open a page, before the page is done.
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
// 2026-07-28 backend answered it with input_required: the server's key, the
// backend's name of the tool, and the backend's own requestState, which
// muster does not read.
type backendInputState struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
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
	if json.Unmarshal(raw, &state) != nil || state.Server == "" || state.Tool == "" {
		return backendInputState{}, false
	}
	return state, true
}

// recordBackendInput notes the input_required result, requestState wrapped
// and messages prefixed, that muster passes on for the backend input request
// a tool call is answered with, for the URL elicitation middleware. Outside a
// tool call it does nothing.
func recordBackendInput(ctx context.Context, input *mcp.CallToolResult) {
	if recorder, ok := ctx.Value(challengeRecorderKey{}).(*challengeRecorder); ok {
		recorder.backendInput.Store(input)
	}
}

// attributed prefixes a backend's message with the server's key, so the
// person knows which server asks. The message is put on one line, its runs of
// white space made one space, so that it cannot start a line of its own that
// names another server.
func attributed(serverName, message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return fmt.Sprintf("[%s] The server asks for your input to continue.", serverName)
	}
	return fmt.Sprintf("[%s] %s", serverName, message)
}

// answerBackendElicitation answers a call of the server's tool that the
// server answered with an input_required result. Any other outcome is
// returned as is.
func answerBackendElicitation(ctx context.Context, serverName, tool string, result *mcp.CallToolResult, err error) (*mcp.CallToolResult, error) {
	if err == nil && result.NeedsInput() {
		return answerBackendInput(ctx, serverName, tool, result), nil
	}
	return result, err
}

// answerBackendInput passes on a 2026-07-28 backend's input_required answer
// (see the top of this file) and answers the call with its text answer. An
// answer asking for anything but elicitations in the modes the client
// declared fails the call.
func answerBackendInput(ctx context.Context, serverName, tool string, result *mcp.CallToolResult) *mcp.CallToolResult {
	declared := callerCapabilities(ctx).Elicitation
	for _, request := range result.InputRequests {
		if request.Method != mcp.MethodElicitationCreate || request.Elicitation == nil {
			logging.WarnWithAttrs("Aggregator", "Refusing a backend's input request muster did not declare",
				slog.String("server", serverName),
				slog.String("method", string(request.Method)))
			return mcp.NewToolResultError(fmt.Sprintf("[%s] The server asked for %s, which muster does not pass on to its client.", serverName, request.Method))
		}
		if mode := elicitationMode(*request.Elicitation); !supportsElicitationMode(declared, mode) {
			logging.WarnWithAttrs("Aggregator", "Refusing a backend's elicitation in a mode the client did not declare",
				slog.String("server", serverName),
				slog.String("mode", mode))
			return mcp.NewToolResultError(fmt.Sprintf("[%s] The server asked for a %s mode elicitation, which the client did not declare.", serverName, mode))
		}
	}
	passed := &mcp.CallToolResult{
		Result: mcp.Result{ResultType: mcp.ResultTypeInputRequired},
		MultiRoundTripResult: mcp.MultiRoundTripResult{
			InputRequests: make(mcp.InputRequests, len(result.InputRequests)),
			RequestState:  backendInputState{Server: serverName, Tool: tool, State: result.RequestState}.encode(),
		},
	}
	var text strings.Builder
	for key, request := range result.InputRequests {
		elicitation := *request.Elicitation
		elicitation.Message = attributed(serverName, elicitation.Message)
		if elicitation.Mode == mcp.ElicitationModeURL && elicitation.ElicitationID == "" {
			elicitation.ElicitationID = urlElicitationID(elicitation.URL)
		}
		request.Elicitation = &elicitation
		if text.Len() > 0 {
			text.WriteString("\n\n")
		}
		text.WriteString(elicitation.Message)
		if elicitation.Mode == mcp.ElicitationModeURL {
			fmt.Fprintf(&text, "\nOpen %s to continue, then call the tool again.", elicitation.URL)
		}
		passed.InputRequests[key] = request
	}
	recordBackendInput(ctx, passed)
	logging.InfoWithAttrs("Aggregator", "Passing on a backend's input request",
		slog.String("server", serverName),
		slog.Int("inputRequests", len(passed.InputRequests)))
	return &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent(text.String())},
		IsError:           true,
		StructuredContent: map[string]any{backendInputMarkerKey: passed.RequestState},
	}
}

// elicitationMode returns the elicitation's mode, form when it names none.
func elicitationMode(elicitation mcp.ElicitationParams) string {
	if elicitation.Mode == "" {
		return mcp.ElicitationModeForm
	}
	return elicitation.Mode
}

// supportsElicitationMode reports whether a client that declared the
// elicitation capability supports the mode. A capability naming no mode is
// form only.
func supportsElicitationMode(declared *mcp.ElicitationCapability, mode string) bool {
	if declared == nil {
		return false
	}
	switch mode {
	case mcp.ElicitationModeForm:
		return declared.Form != nil || declared.URL == nil
	case mcp.ElicitationModeURL:
		return declared.URL != nil
	default:
		return false
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

// prepareBackendCall returns the context a call of the server's tool runs
// under: the capabilities of the client the call is made for, and, on the
// retry of the server's input request, its answer and the server's own
// requestState. The answer goes to the first backend call, once. A retry
// whose requestState names another server or tool than the one the call is
// routed to is refused: the client's echo does not choose where its answer
// goes.
func prepareBackendCall(ctx context.Context, serverName, tool string) (context.Context, error) {
	ctx = internalmcp.WithCallerCapabilities(ctx, callerCapabilities(ctx))
	pending, ok := ctx.Value(backendRoundTripKey{}).(*pendingBackendRoundTrip)
	if !ok || !pending.taken.CompareAndSwap(false, true) {
		return ctx, nil
	}
	if pending.state.Server != serverName || pending.state.Tool != tool {
		return ctx, fmt.Errorf("the retry answers a request of tool %s of %s, but the call goes to tool %s of %s: call the tool again without inputResponses",
			pending.state.Tool, pending.state.Server, tool, serverName)
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
// answers by calling the tool again without the requestState; any other
// request is returned as is, and mcp-go asks it with elicitation/create and
// calls the tool again with the answers and the requestState.
// answerBackendInput already refused an elicitation in a mode the client did
// not declare.
func passBackendInput(ctx context.Context, input *mcp.CallToolResult) (*mcp.CallToolResult, error) {
	if mcp.IsModernProtocol(mcpserver.RequestProtocolVersion(ctx)) {
		return input, nil
	}
	elicitations := make([]mcp.ElicitationParams, 0, len(input.InputRequests))
	for _, request := range input.InputRequests {
		if request.Elicitation == nil || request.Elicitation.Mode != mcp.ElicitationModeURL {
			return input, nil
		}
		elicitations = append(elicitations, *request.Elicitation)
	}
	if len(elicitations) == 0 {
		return input, nil
	}
	return nil, mcp.URLElicitationRequiredError{Elicitations: elicitations}
}
