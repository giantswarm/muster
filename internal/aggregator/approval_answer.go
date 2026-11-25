package aggregator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// approvalInputKey is the inputRequests key of the approval elicitation in
// an input_required answer.
const approvalInputKey = "muster_approval"

// approvalChallenge is the approval link muster answered a call with.
type approvalChallenge struct {
	request *api.ApprovalRequest
	link    string
}

// recordApprovalChallenge notes the approval link a call is answered with,
// for the URL elicitation middleware, and returns the text fallback: a tool
// error carrying the message and the link, for a client that cannot show a
// URL elicitation. Either way the retry is matched by muster, not by
// anything the client echoes.
func (a *AggregatorServer) recordApprovalChallenge(ctx context.Context, request *api.ApprovalRequest) *mcp.CallToolResult {
	link := a.approval.page.URL(request.ID)
	if recorder, ok := ctx.Value(challengeRecorderKey{}).(*challengeRecorder); ok {
		recorder.approval.Store(&approvalChallenge{request: request, link: link})
	}
	text := fmt.Sprintf("approval_required: %s Approve or decline at %s then run the tool again.", request.Display.Message, link)
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{mcp.NewTextContent(text)},
		StructuredContent: map[string]any{
			"status":      "approval_required",
			"message":     request.Display.Message,
			"approvalUrl": link,
			"expiresAt":   request.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		},
	}
}

// approvalLink returns the approval link a result carries, "" for any other
// result.
func approvalLink(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	structured, _ := result.StructuredContent.(map[string]any)
	link, _ := structured["approvalUrl"].(string)
	return link
}

// elicitApproval answers a call with the approval link as a URL elicitation,
// in the shape of the request's protocol: an input_required result whose
// requestState is only the request's random id, or URLElicitationRequiredError
// (-32042) with exactly one elicitation. The elicitation's id is the
// request's own random elicitationId, distinct from the id in the link.
func elicitApproval(ctx context.Context, challenge approvalChallenge) (*mcp.CallToolResult, error) {
	params := mcp.ElicitationParams{
		Mode:          mcp.ElicitationModeURL,
		Message:       challenge.request.Display.Message,
		URL:           challenge.link,
		ElicitationID: challenge.request.ElicitationID,
	}
	logging.InfoWithAttrs("Aggregator", "Answering a tool call with an approval URL elicitation",
		slog.String("approval", logging.TruncateIdentifier(challenge.request.ID)),
		slog.String("target", challenge.request.Target.Name))
	if mcp.IsModernProtocol(mcpserver.RequestProtocolVersion(ctx)) {
		return mcpserver.NewInputRequestBuilder(challenge.request.ID).Elicit(approvalInputKey, params).ToolResult(), nil
	}
	return nil, mcp.URLElicitationRequiredError{Elicitations: []mcp.ElicitationParams{params}}
}

// approvalPromptAnswer handles a retry that carries the client's answer to
// the approval prompt: an accept only means the person went to the page, so
// the call proceeds and muster finds the decision itself; any other answer
// runs nothing.
func approvalPromptAnswer(req *mcp.CallToolRequest) (*mcp.CallToolResult, bool) {
	answer := mcpserver.ElicitationResponse(req.Params.InputResponses, approvalInputKey)
	if answer == nil {
		return nil, false
	}
	if answer.Action != mcp.ElicitationResponseActionAccept {
		return mcp.NewToolResultError(fmt.Sprintf("The tool did not run: the approval prompt was answered %s.", answer.Action)), true
	}
	req.Params.MultiRoundTripParams = mcp.MultiRoundTripParams{}
	return nil, false
}
