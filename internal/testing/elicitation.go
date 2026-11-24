package testing

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
)

// maxElicitationRoundTrips bounds how many times a step's call is retried
// with its elicitation answer.
const maxElicitationRoundTrips = 5

// elicitationActions are the answers a step's elicitation field may give.
var elicitationActions = []mcp.ElicitationResponseAction{
	mcp.ElicitationResponseActionAccept,
	mcp.ElicitationResponseActionDecline,
	mcp.ElicitationResponseActionCancel,
}

type clientProtocolVersionKey struct{}

// WithClientProtocolVersion returns a context under which the test client
// connects asking for version; an empty version asks for
// api.ClientProtocolVersion.
func WithClientProtocolVersion(ctx context.Context, version string) context.Context {
	if version == "" {
		return ctx
	}
	return context.WithValue(ctx, clientProtocolVersionKey{}, version)
}

func clientProtocolVersionFromContext(ctx context.Context) string {
	if version, ok := ctx.Value(clientProtocolVersionKey{}).(string); ok {
		return version
	}
	return api.ClientProtocolVersion
}

type elicitationAnswerKey struct{}

// WithElicitationAnswer returns a context under which the test client
// declares URL mode elicitation on its tool calls and answers every URL
// elicitation of an input_required result with action. A step's elicitation
// field is stashed this way, like its headers.
func WithElicitationAnswer(ctx context.Context, action mcp.ElicitationResponseAction) context.Context {
	if action == "" {
		return ctx
	}
	return context.WithValue(ctx, elicitationAnswerKey{}, action)
}

func elicitationAnswerFromContext(ctx context.Context) mcp.ElicitationResponseAction {
	action, _ := ctx.Value(elicitationAnswerKey{}).(mcp.ElicitationResponseAction)
	return action
}

// withDeclaredElicitation declares elicitation.url on the requests sent under
// ctx when the step answers elicitations.
func withDeclaredElicitation(ctx context.Context) context.Context {
	if elicitationAnswerFromContext(ctx) == "" {
		return ctx
	}
	return client.WithRequestClientCapabilities(ctx, mcp.ClientCapabilities{
		Elicitation: &mcp.ElicitationCapability{URL: &struct{}{}},
	})
}

// callWithElicitationAnswer sends the call and, when ctx carries an
// elicitation answer, answers the input_required results with it and retries
// with the echoed requestState until the call has a result. A step that
// answers elicitations fails when the call asks for none, so that the field
// cannot pass vacuously; a step that does not fails on an input_required
// result.
func callWithElicitationAnswer(ctx context.Context, call func(context.Context, mcp.MultiRoundTripParams) (*mcp.CallToolResult, error)) (*mcp.CallToolResult, error) {
	action := elicitationAnswerFromContext(ctx)
	ctx = withDeclaredElicitation(ctx)
	var roundTrip mcp.MultiRoundTripParams
	for round := range maxElicitationRoundTrips {
		result, err := call(ctx, roundTrip)
		if err != nil {
			return nil, err
		}
		if !result.NeedsInput() {
			if action != "" && round == 0 {
				return nil, fmt.Errorf("the step answers elicitations with %s, but the call asked for none", action)
			}
			return result, nil
		}
		if action == "" {
			return nil, fmt.Errorf("the call asked for input (%s); answer it with the step's elicitation field", describeInputRequests(result.InputRequests))
		}
		roundTrip = mcp.MultiRoundTripParams{RequestState: result.RequestState, InputResponses: mcp.InputResponses{}}
		for key, request := range result.InputRequests {
			if request.Method != mcp.MethodElicitationCreate || request.Elicitation == nil || request.Elicitation.Mode != mcp.ElicitationModeURL {
				return nil, fmt.Errorf("input request %q is not a URL mode elicitation: %s", key, describeInputRequests(result.InputRequests))
			}
			roundTrip.InputResponses[key] = mcp.NewElicitationInputResponse(mcp.ElicitationResult{
				ElicitationResponse: mcp.ElicitationResponse{Action: action},
			})
		}
	}
	return nil, fmt.Errorf("the call still asks for input after %d answers", maxElicitationRoundTrips)
}

// describeInputRequests names each input request with its key, method, mode,
// message and URL, in key order, for a failing step's message.
func describeInputRequests(requests mcp.InputRequests) string {
	keys := make([]string, 0, len(requests))
	for key := range requests {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		request := requests[key]
		if e := request.Elicitation; e != nil {
			parts = append(parts, fmt.Sprintf("%s: %s %s %q %s", key, request.Method, e.Mode, e.Message, e.URL))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", key, request.Method))
	}
	return strings.Join(parts, "; ")
}
