package aggregator

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// A backend MCP server may answer a tool call with a URL elicitation: the
// request that the person open a page, -32042 URLElicitationRequiredError on
// the protocol muster speaks to its backends (api.ClientProtocolVersion).
// muster relays it to its own client only from a server whose MCPServer opts
// in (spec.urlElicitation Relay) and only to a page on the origin of
// aggregator.devPortalUrl, so no backend, an organization's own MCP server
// included, can send people to a page of its choosing. Every other one is
// dropped and logged with the server and the page's origin, and the call is
// answered with a tool error that does not hold the URL.
//
// A relayed elicitation travels like muster's own sign-in elicitation: the
// dispatch records it in the call's context (challengeRecorder) and answers
// with a text result holding the link, the answer of a client that does not
// support URL elicitation; the urlElicitation middleware turns that answer
// into the elicitation, in the shape of the request's protocol version.
//
// The backend tells muster when the person completed the page: it sends
// notifications/elicitation/complete with the elicitationId on the connection
// that received the -32042. muster keeps the backend's elicitationId when it
// relays, records each relayed one (relayedCompletions), and on the backend's
// notification sends the same notification to the transport sessions that
// received the -32042, and ends the wait of a 2026-07-28 retry that accepted
// it (awaitRelayedCompletion). The record is local to the replica that
// relayed, as the backend connection is: a retry on another replica, or after
// a restart, waits for nothing.

// relayedElicitationInputKey prefixes the inputRequests keys of a relayed
// backend elicitation in an input_required answer.
const relayedElicitationInputKey = "muster_relayed_elicitation"

// relayedElicitationURLKey is the structuredContent key that holds the
// link of a relayed elicitation's text answer.
const relayedElicitationURLKey = "elicitationUrl"

// relayedElicitation is a backend's URL elicitation muster relays: the
// server it came from and the elicitations, each on the Dev Portal's origin.
type relayedElicitation struct {
	server       string
	elicitations []mcp.ElicitationParams
}

// link is the URL the relayed elicitation's text answer carries.
func (r relayedElicitation) link() string {
	return r.elicitations[0].URL
}

// relayedRequestState is the requestState of a relayed elicitation, echoed
// by the client on its retry: the server, named in the answer to a declined
// elicitation, and the backend's elicitationIds, whose completion a retry
// that accepted waits for. Being the client's echo, it only decides what the
// retry waits for, and only among the elicitations relayed to its own
// session: the call is gated as any other afterwards.
type relayedRequestState struct {
	Relayed        string   `json:"relayed"`
	ElicitationIDs []string `json:"elicitationIds,omitempty"`
}

func (s relayedRequestState) encode() string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}

// recordRelayedElicitation notes the backend elicitation a tool call is
// answered with, for the URL elicitation middleware. Outside a tool call it
// does nothing.
func recordRelayedElicitation(ctx context.Context, relayed relayedElicitation) {
	if recorder, ok := ctx.Value(challengeRecorderKey{}).(*challengeRecorder); ok {
		recorder.relayed.Store(&relayed)
	}
}

// answerBackendElicitation answers a backend tool call that the server
// answered with a URL elicitation: relayed when the server and the page are
// allowed (see the top of this file), dropped otherwise. Any other outcome is
// returned as is.
func (a *AggregatorServer) answerBackendElicitation(ctx context.Context, serverName string, result *mcp.CallToolResult, err error) (*mcp.CallToolResult, error) {
	var required mcp.URLElicitationRequiredError
	if err == nil || !errors.As(err, &required) {
		return result, err
	}
	if reason := a.refuseBackendElicitation(ctx, serverName, required.Elicitations); reason != "" {
		return droppedElicitationResult(serverName, reason, required.Elicitations), nil
	}

	relayed := relayedElicitation{server: serverName}
	for _, elicitation := range required.Elicitations {
		relayed.elicitations = append(relayed.elicitations, mcp.ElicitationParams{
			Mode:          mcp.ElicitationModeURL,
			Message:       elicitation.Message,
			URL:           elicitation.URL,
			ElicitationID: elicitation.ElicitationID,
		})
	}
	recordRelayedElicitation(ctx, relayed)
	logging.InfoWithAttrs("Aggregator", "Relaying a backend's URL elicitation",
		slog.String("server", serverName),
		slog.String("origin", a.devPortalOrigin))
	return relayedElicitationResult(relayed), nil
}

// refuseBackendElicitation returns why the server's URL elicitations are
// not relayed, or "" when they are, and logs each one it drops with the
// server and the origin of its page. One elicitation that may not be relayed
// drops them all.
func (a *AggregatorServer) refuseBackendElicitation(ctx context.Context, serverName string, elicitations []mcp.ElicitationParams) string {
	drop := func(origin, reason string) {
		logging.WarnWithAttrs("Aggregator", "Dropped a backend's URL elicitation",
			slog.String("server", serverName),
			slog.String("origin", origin),
			slog.String("reason", reason))
	}
	if len(elicitations) == 0 {
		drop("", "the error holds no elicitation")
		return "it holds no page to open"
	}

	reason := ""
	switch {
	case !serverRelaysURLElicitation(ctx, serverName):
		reason = "its MCPServer does not set spec.urlElicitation to Relay"
	case a.devPortalOrigin == "":
		reason = "muster has no aggregator.devPortalUrl"
	}
	refused := reason
	for _, elicitation := range elicitations {
		origin := config.URLOrigin(elicitation.URL)
		elicitationReason := reason
		if elicitationReason == "" {
			switch {
			case elicitation.Mode != mcp.ElicitationModeURL:
				elicitationReason = fmt.Sprintf("the elicitation's mode is %q, not url", elicitation.Mode)
			case origin == "" || origin != a.devPortalOrigin:
				elicitationReason = "the page is not on the Dev Portal's origin"
			}
		}
		if elicitationReason != "" {
			drop(origin, elicitationReason)
			if refused == "" {
				refused = elicitationReason
			}
		}
	}
	return refused
}

// serverRelaysURLElicitation reports whether the server's MCPServer sets
// spec.urlElicitation to Relay. The field is read from the definition source,
// as spec.suspended is (serverSuspended): the aggregator keeps no copy of it.
// Without a manager, or for a name it does not know or cannot read, nothing is
// relayed. Nor is anything relayed from a server outside muster's namespace,
// whose Relay its namespace refuses (api.ValidateServerNamespace): an
// organization cannot put its own server on the allowlist, even while a
// refused definition is still listed.
func serverRelaysURLElicitation(ctx context.Context, serverName string) bool {
	if api.IsTenantMCPServerKey(serverName) {
		return false
	}
	manager := api.GetMCPServerManager()
	if manager == nil {
		return false
	}
	info, err := manager.GetMCPServer(ctx, serverName)
	if err != nil || info == nil {
		return false
	}
	return info.URLElicitation == api.MCPServerURLElicitationRelay
}

// declareURLElicitation makes a session-scoped client declare URL mode
// elicitation to its server when the server's MCPServer relays it, as the
// client factory does for a shared client (MCPClientConfig.URLElicitation),
// and passes the server's completions of the elicitations muster relayed on
// (observeRelayedCompletions) for the session the client belongs to. Call it
// before the client's Initialize.
func (a *AggregatorServer) declareURLElicitation(ctx context.Context, serverName string, client MCPClient) {
	declarer, ok := client.(internalmcp.URLElicitationDeclarer)
	if !ok || !serverRelaysURLElicitation(ctx, serverName) {
		return
	}
	declarer.DeclareURLElicitation()
	a.observeRelayedCompletions(serverName, getSessionIDFromContext(ctx), client)
}

// droppedElicitationResult answers a call whose URL elicitation muster
// dropped. It names the server and why, and carries the server's own words,
// the elicitations' messages, with every link in them removed: never a URL.
func droppedElicitationResult(serverName, reason string, elicitations []mcp.ElicitationParams) *mcp.CallToolResult {
	text := fmt.Sprintf("%s answered the call with a request to open a page, which muster does not pass on: %s.", serverName, reason)
	for _, elicitation := range elicitations {
		if message := strings.TrimSpace(linkPattern.ReplaceAllString(elicitation.Message, droppedLinkText)); message != "" {
			text += "\n\n" + message
		}
	}
	return mcp.NewToolResultError(text)
}

// linkPattern matches what a client or a model could open as a link in a
// dropped elicitation's message: a URL with a scheme, and a scheme-relative
// or bare-host one (//host/path, host.tld/path).
var linkPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*:|//)\S+|\b[a-z0-9-]+(\.[a-z0-9-]+)+/\S*`)

// droppedLinkText stands for a link removed from a dropped elicitation's
// message.
const droppedLinkText = "[link removed]"

// relayedElicitationResult answers a call with a relayed elicitation's
// messages and links as text: the answer of a client that does not support
// URL elicitation. The links are on the Dev Portal's origin.
func relayedElicitationResult(relayed relayedElicitation) *mcp.CallToolResult {
	var text strings.Builder
	for i, elicitation := range relayed.elicitations {
		if i > 0 {
			text.WriteString("\n\n")
		}
		if elicitation.Message != "" {
			text.WriteString(elicitation.Message + "\n")
		}
		fmt.Fprintf(&text, "Open %s to continue, then call the tool again.", elicitation.URL)
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent(text.String())},
		IsError:           true,
		StructuredContent: map[string]any{relayedElicitationURLKey: relayed.link()},
	}
}

// relayedElicitationLink returns the link a relayed elicitation's text
// answer carries in structuredContent, directly or relayed by call_tool, or "".
func relayedElicitationLink(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	structured, _ := result.StructuredContent.(map[string]any)
	link, _ := structured[relayedElicitationURLKey].(string)
	return link
}

// elicitRelayed answers a tool call with a relayed backend elicitation, in
// the shape of the request's protocol version: an input_required result
// from 2026-07-28, URLElicitationRequiredError (-32042) before. Each
// elicitation that holds the backend's elicitationId is recorded, so the
// backend's completion of it reaches this request's transport session
// (2025-11-25) or ends the wait of the retry (2026-07-28). waitExpired says
// that this call is a retry which waited relayedRetryWait for the completion
// in vain: the person is told so.
func (a *AggregatorServer) elicitRelayed(ctx context.Context, relayed relayedElicitation, waitExpired bool) (*mcp.CallToolResult, error) {
	modern := mcp.IsModernProtocol(mcpserver.RequestProtocolVersion(ctx))
	transportID := ""
	if !modern {
		transportID = getTransportSessionID(ctx)
	}
	sessionID := getSessionIDFromContext(ctx)
	state := relayedRequestState{Relayed: relayed.server}
	elicitations := make([]mcp.ElicitationParams, len(relayed.elicitations))
	for i, elicitation := range relayed.elicitations {
		if elicitation.Message == "" {
			elicitation.Message = fmt.Sprintf("%s asks you to open a page of the Dev Portal to continue.", relayed.server)
		}
		if waitExpired {
			elicitation.Message = fmt.Sprintf("muster waited %s for %s to report that its page was completed, and it did not. %s",
				fmt.Sprintf("%d minutes", int(relayedRetryWait/time.Minute)), relayed.server, elicitation.Message)
		}
		if elicitation.ElicitationID != "" {
			a.relayedCompletions.add(relayed.server, sessionID, elicitation.ElicitationID, transportID)
			state.ElicitationIDs = append(state.ElicitationIDs, elicitation.ElicitationID)
		}
		elicitations[i] = elicitation
	}

	if modern {
		builder := mcpserver.NewInputRequestBuilder(state.encode())
		for i, elicitation := range elicitations {
			if elicitation.ElicitationID == "" {
				// rmcp refuses a URL elicitation without one (signInElicitationID).
				elicitation.ElicitationID = signInElicitationID(elicitation.URL)
			}
			builder.Elicit(relayedElicitationKey(i), elicitation)
		}
		return builder.ToolResult(), nil
	}

	for i := range elicitations {
		if elicitations[i].ElicitationID == "" {
			elicitations[i].ElicitationID = rand.Text()
		}
	}
	return nil, mcp.URLElicitationRequiredError{Elicitations: elicitations}
}

// relayedElicitationKey is the inputRequests key of the i-th relayed
// elicitation of an input_required answer.
func relayedElicitationKey(i int) string {
	return fmt.Sprintf("%s_%d", relayedElicitationInputKey, i)
}

// relayedElicitationAnswer returns the action of a 2026-07-28 retry that
// answers relayed elicitations -- the first that is not accept, else accept --
// and whether the retry answers any.
func relayedElicitationAnswer(responses mcp.InputResponses) (mcp.ElicitationResponseAction, bool) {
	action, answered := mcp.ElicitationResponseActionAccept, false
	for key := range responses {
		if !strings.HasPrefix(key, relayedElicitationInputKey+"_") {
			continue
		}
		answered = true
		answer := mcpserver.ElicitationResponse(responses, key)
		if answer == nil || answer.Action != mcp.ElicitationResponseActionAccept {
			action = mcp.ElicitationResponseActionDecline
			if answer != nil {
				action = answer.Action
			}
		}
	}
	return action, answered
}

// decodeRelayedRequestState reads a retry's requestState; an unreadable one
// is the zero value, which waits for nothing.
func decodeRelayedRequestState(requestState string) relayedRequestState {
	var state relayedRequestState
	_ = json.Unmarshal([]byte(requestState), &state)
	return state
}

// declinedRelayedResult answers a retry whose relayed elicitation the person
// declined or cancelled. The server is from the client's echo of requestState.
func declinedRelayedResult(action mcp.ElicitationResponseAction, requestState string) *mcp.CallToolResult {
	state := decodeRelayedRequestState(requestState)
	if state.Relayed == "" {
		return mcp.NewToolResultError(fmt.Sprintf("The tool did not run: the person answered %s to opening the page.", action))
	}
	return mcp.NewToolResultError(fmt.Sprintf("The tool did not run: the person answered %s to opening the page %s asked for.", action, state.Relayed))
}
