package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
)

// maxInputRoundTrips bounds how many times one tool call is retried with
// answers to the server's input requests. muster asks again when a sign-in
// did not complete, so a person may need more than one round.
const maxInputRoundTrips = 5

// URLElicitation is one URL mode elicitation of an input_required answer: the
// server asks the person to open URL, for the reason Message gives. Message
// names the server that asks (muster's sign-in message names the server to
// sign in to; a backend's message is prefixed with its key in brackets).
type URLElicitation struct {
	// Key is the elicitation's key in the answer's inputRequests.
	Key string
	// Message is the server's reason, shown to the person as is.
	Message string
	// URL is the page to open, as the server sent it: not yet validated.
	URL string
}

// URLElicitationHandler asks the person about the URL elicitations of one
// input_required answer and returns an action for each, in order: accept
// once the person is done on the page, decline or cancel otherwise. An error
// ends the tool call with that error and sends no answer.
type URLElicitationHandler func(ctx context.Context, elicitations []URLElicitation) ([]mcp.ElicitationResponseAction, error)

// ErrUnanswerableInput is returned for an input_required answer this client
// cannot answer: one asking for anything but URL mode elicitations, or any
// input request when no URLElicitationHandler is set.
var ErrUnanswerableInput = errors.New("the server asked for input this client cannot give")

// SetURLElicitationHandler makes the client answer URL mode elicitations with
// handler. Set before Connect: over streamable HTTP the client then asks for
// MCP api.MultiRoundTripProtocolVersion and declares elicitation.url on each
// tool call; a server that does not speak that revision is spoken to on
// api.ClientProtocolVersion without the capability, and keeps answering a
// sign-in with its auth_required result.
func (c *Client) SetURLElicitationHandler(handler URLElicitationHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.urlElicitation = handler
}

func (c *Client) urlElicitationHandler() URLElicitationHandler {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.urlElicitation
}

// answersURLElicitations reports whether this client asks for the revision
// on which it answers URL elicitations: a handler is set and the transport is
// streamable HTTP, the only one that revision defines.
func (c *Client) answersURLElicitations() bool {
	return c.transport == TransportStreamableHTTP && c.urlElicitationHandler() != nil
}

// requestedProtocolVersion is the revision initialize asks for.
func (c *Client) requestedProtocolVersion() string {
	if c.answersURLElicitations() {
		return api.MultiRoundTripProtocolVersion
	}
	return api.ClientProtocolVersion
}

// mcpClientOptions are the options of the streamable HTTP client: input
// requests come back to this client unanswered, and server/discover is
// bounded by the initialize timeout only, so a slow discover is not read as
// a server older than 2026-07-28.
func (c *Client) mcpClientOptions() []client.ClientOption {
	if !c.answersURLElicitations() {
		return nil
	}
	return []client.ClientOption{
		client.WithManualInputRoundTrips(),
		client.WithDiscoverTimeout(-1),
	}
}

// withURLElicitationCapability declares elicitation.url on the requests sent
// under the returned context when the connection negotiated a revision that
// carries capabilities per request and a handler is set. On an earlier
// revision the capabilities were fixed, empty, at initialize.
func (c *Client) withURLElicitationCapability(ctx context.Context) context.Context {
	if !c.answersURLElicitations() || !mcp.IsModernProtocol(c.negotiatedProtocolVersion()) {
		return ctx
	}
	return client.WithRequestClientCapabilities(ctx, mcp.ClientCapabilities{
		Elicitation: &mcp.ElicitationCapability{URL: &struct{}{}},
	})
}

func (c *Client) negotiatedProtocolVersion() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.protocolVersion
}

// callToolRoundTrips calls the tool and, while the server answers that it
// needs input, asks the URL elicitation handler and retries the same call
// with the person's answers and the server's requestState, echoed verbatim.
// Each round trip gets its own timeout: the time the person takes is not the
// call's.
func (c *Client) callToolRoundTrips(ctx context.Context, name string, args map[string]any, timeout time.Duration) (*mcp.CallToolResult, error) {
	var roundTrip mcp.MultiRoundTripParams
	for round := 0; ; round++ {
		result, err := c.callToolOnce(ctx, name, args, timeout, roundTrip)
		if err != nil || !result.NeedsInput() {
			return result, err
		}
		if round == maxInputRoundTrips {
			return nil, fmt.Errorf("tool call %s: the server still asks for input after %d answers", name, maxInputRoundTrips)
		}
		roundTrip, err = c.answerInputRequests(ctx, result)
		if err != nil {
			return nil, fmt.Errorf("tool call %s: %w", name, err)
		}
	}
}

// answerInputRequests returns the retry parameters answering an input_required
// result's URL elicitations with the handler's actions, in key order. An
// answer without input requests (a server shedding load) is retried with its
// requestState alone.
func (c *Client) answerInputRequests(ctx context.Context, result *mcp.CallToolResult) (mcp.MultiRoundTripParams, error) {
	retry := mcp.MultiRoundTripParams{RequestState: result.RequestState}
	if len(result.InputRequests) == 0 {
		return retry, nil
	}
	handler := c.urlElicitationHandler()
	if handler == nil {
		return retry, ErrUnanswerableInput
	}
	elicitations, err := URLElicitationsOf(result)
	if err != nil {
		return retry, err
	}
	actions, err := handler(ctx, elicitations)
	if err != nil {
		return retry, err
	}
	if len(actions) != len(elicitations) {
		return retry, fmt.Errorf("%d answers to %d elicitations", len(actions), len(elicitations))
	}
	retry.InputResponses = make(mcp.InputResponses, len(elicitations))
	for i, e := range elicitations {
		retry.InputResponses[e.Key] = mcp.NewElicitationInputResponse(mcp.ElicitationResult{
			ElicitationResponse: mcp.ElicitationResponse{Action: actions[i]},
		})
	}
	return retry, nil
}

// URLElicitationsOf returns the URL mode elicitations of an input_required
// result, in key order, or ErrUnanswerableInput when it asks for anything
// else.
func URLElicitationsOf(result *mcp.CallToolResult) ([]URLElicitation, error) {
	keys := make([]string, 0, len(result.InputRequests))
	for key := range result.InputRequests {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	elicitations := make([]URLElicitation, 0, len(keys))
	for _, key := range keys {
		request := result.InputRequests[key]
		e := request.Elicitation
		if request.Method != mcp.MethodElicitationCreate || e == nil || e.Mode != mcp.ElicitationModeURL {
			return nil, fmt.Errorf("%w: input request %q is %s, not a URL mode elicitation", ErrUnanswerableInput, key, request.Method)
		}
		elicitations = append(elicitations, URLElicitation{Key: key, Message: e.Message, URL: e.URL})
	}
	return elicitations, nil
}
