package aggregator

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// signInElicitationTTL is how long a sign-in elicitation can still complete:
// the OAuth state behind its link lives 10 minutes (the state stores' expiry).
const signInElicitationTTL = 10 * time.Minute

// signInInputKey is the inputRequests key of the sign-in elicitation in an
// input_required answer.
const signInInputKey = "muster_sign_in"

// signInChallenge is the sign-in link muster answered a tool call with
// because the session is not authenticated to the server.
type signInChallenge struct {
	sessionID string
	server    string
	authURL   string
	reason    string
}

type challengeRecorderKey struct{}

// challengeRecorder holds the sign-in challenge muster issued while handling
// one tool call. It lives in the call's context, so only muster's own answer
// is ever turned into an elicitation: a backend that returns an auth_required
// shaped result of its own records nothing.
type challengeRecorder struct {
	challenge atomic.Pointer[signInChallenge]
}

func withChallengeRecorder(ctx context.Context) (context.Context, *challengeRecorder) {
	recorder := &challengeRecorder{}
	return context.WithValue(ctx, challengeRecorderKey{}, recorder), recorder
}

// recordSignInChallenge notes the sign-in challenge a tool call is answered
// with, for the URL elicitation middleware. Outside a tool call it does
// nothing.
func recordSignInChallenge(ctx context.Context, challenge signInChallenge) {
	if recorder, ok := ctx.Value(challengeRecorderKey{}).(*challengeRecorder); ok {
		recorder.challenge.Store(&challenge)
	}
}

// urlElicitation returns the ToolHandlerMiddleware that asks a client which
// supports URL mode elicitation to open the sign-in link of an auth_required
// answer, instead of handing the link over as tool output (MCP elicitation,
// URL mode). The link then never passes through the model's context.
//
// A request on protocol 2026-07-28 or later gets an input_required result
// carrying the elicitation; the client retries the call once the person
// signed in, and the retry is answered as any other call. A retry carrying a
// declined or cancelled elicitation is answered with a tool error instead of
// asking again. A request on an earlier protocol gets
// URLElicitationRequiredError (-32042), and the session is sent
// notifications/elicitation/complete when the sign-in completes
// (signInElicitations). A client that does not declare elicitation.url keeps
// the auth_required answer.
func (a *AggregatorServer) urlElicitation() mcpserver.ToolHandlerMiddleware {
	return func(next mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, recorder := withChallengeRecorder(ctx)
			result, err := next(ctx, req)
			if err != nil || !clientSupportsURLElicitation(ctx) {
				return result, err
			}
			challenge := recorder.challenge.Load()
			if challenge == nil || authRequiredLink(result) != challenge.authURL {
				return result, err
			}
			if answer := mcpserver.ElicitationResponse(req.Params.InputResponses, signInInputKey); answer != nil && answer.Action != mcp.ElicitationResponseActionAccept {
				return mcp.NewToolResultError(fmt.Sprintf("The tool did not run: the person answered %s to signing in to %s.", answer.Action, challenge.server)), nil
			}
			return a.elicitSignIn(ctx, *challenge)
		}
	}
}

// elicitSignIn answers a tool call with a URL elicitation of the challenge's
// sign-in link, in the shape of the request's protocol version.
func (a *AggregatorServer) elicitSignIn(ctx context.Context, challenge signInChallenge) (*mcp.CallToolResult, error) {
	message := fmt.Sprintf("Sign in to %s to continue: %s.", challenge.server, challenge.reason)

	if mcp.IsModernProtocol(mcpserver.RequestProtocolVersion(ctx)) {
		return mcpserver.NewInputRequestBuilder("").Elicit(signInInputKey, mcp.ElicitationParams{
			Mode:    mcp.ElicitationModeURL,
			Message: message,
			URL:     challenge.authURL,
		}).ToolResult(), nil
	}

	elicitationID := a.signInElicitations.add(challenge.sessionID, challenge.server, getTransportSessionID(ctx))
	logging.InfoWithAttrs("Aggregator", "Answering a tool call with a sign-in URL elicitation",
		slog.String("server", challenge.server),
		slog.String("sessionID", logging.TruncateIdentifier(challenge.sessionID)))
	return nil, mcp.URLElicitationRequiredError{
		Elicitations: []mcp.ElicitationParams{{
			Mode:          mcp.ElicitationModeURL,
			Message:       message,
			ElicitationID: elicitationID,
			URL:           challenge.authURL,
		}},
	}
}

// authRequiredLink returns the sign-in link of an auth_required answer,
// directly or relayed by call_tool, which carries the wrapped tool's
// structuredContent; "" for any other result.
func authRequiredLink(result *mcp.CallToolResult) string {
	if result == nil || !result.IsError {
		return ""
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured["status"] != "auth_required" {
		return ""
	}
	link, _ := structured["authUrl"].(string)
	return link
}

// clientSupportsURLElicitation reports whether the client declared URL mode
// elicitation: on the request itself from protocol 2026-07-28, at the
// session's initialize before that.
func clientSupportsURLElicitation(ctx context.Context) bool {
	var capabilities *mcp.ClientCapabilities
	if info := mcpserver.RequestProtocolInfoFromContext(ctx); info != nil && info.Modern {
		capabilities = info.ClientCapabilities
	} else if session, ok := mcpserver.ClientSessionFromContext(ctx).(mcpserver.SessionWithClientInfo); ok {
		declared := session.GetClientCapabilities()
		capabilities = &declared
	}
	return capabilities != nil && capabilities.Elicitation != nil && capabilities.Elicitation.URL != nil
}

// signInElicitations remembers the URL elicitations sent as -32042 per
// session, server and transport session, so the transport sessions that
// received them are sent notifications/elicitation/complete when the sign-in
// completes. A transport session asked again for the same sign-in while it is
// pending gets the same elicitationId: it is one elicitation, completed once.
//
// The zero value is ready to use. It is local to the replica: a sign-in that
// completes on another replica sends no notification, which the protocol
// allows (the notification is optional; the client keeps its own retry).
type signInElicitations struct {
	mu      sync.Mutex
	entries map[signInElicitationKey]signInElicitation
	// now is the clock; nil means time.Now. Tests set it.
	now func() time.Time
}

type signInElicitationKey struct {
	pendingSignInKey
	transportID string
}

type signInElicitation struct {
	elicitationID string
	expiresAt     time.Time
}

func (s *signInElicitations) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// add returns the elicitationId the transport session is asked to sign the
// session in to the server with: the pending one, or a new one. A request
// without a transport session gets an id that nothing completes.
func (s *signInElicitations) add(sessionID, server, transportID string) string {
	if transportID == "" {
		return rand.Text()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	for key, e := range s.entries {
		if !now.Before(e.expiresAt) {
			delete(s.entries, key)
		}
	}
	key := signInElicitationKey{pendingSignInKey{sessionID: sessionID, server: server}, transportID}
	if e, ok := s.entries[key]; ok {
		return e.elicitationID
	}
	if s.entries == nil {
		s.entries = make(map[signInElicitationKey]signInElicitation)
	}
	e := signInElicitation{elicitationID: rand.Text(), expiresAt: now.Add(signInElicitationTTL)}
	s.entries[key] = e
	return e.elicitationID
}

// take removes the unexpired elicitations of the session's sign-in to the
// server and returns their elicitationId per transport session.
func (s *signInElicitations) take(sessionID, server string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	taken := make(map[string]string)
	for key, e := range s.entries {
		if key.pendingSignInKey != (pendingSignInKey{sessionID: sessionID, server: server}) {
			continue
		}
		delete(s.entries, key)
		if now.Before(e.expiresAt) {
			taken[key.transportID] = e.elicitationID
		}
	}
	return taken
}

// completeSignInElicitations sends notifications/elicitation/complete to each
// transport session that was asked to sign the session in to the server.
func (a *AggregatorServer) completeSignInElicitations(sessionID, server string) {
	if a.mcpServer == nil {
		return
	}
	for transportID, elicitationID := range a.signInElicitations.take(sessionID, server) {
		params := map[string]any{"elicitationId": elicitationID}
		if err := a.mcpServer.SendNotificationToSpecificClient(transportID, string(mcp.MethodNotificationElicitationComplete), params); err != nil {
			logging.Debug("Aggregator", "elicitation complete to session %s failed: %v",
				logging.TruncateIdentifier(transportID), err)
		}
	}
}
