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

// signInRetryWait is how long a retry that accepted the sign-in elicitation
// waits for the sign-in to complete before asking again. The client retries
// as soon as the person agreed to open the link, before they signed in.
const signInRetryWait = 5 * time.Minute

// signInPollInterval is how often a waiting retry reads the session's
// authentication, which the sign-in's callback marks on whichever replica it
// reaches.
const signInPollInterval = time.Second

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
// one tool call: a gated call's auth_required answer or core_auth_login's
// challenge. It lives in the call's context, so only muster's own answer is
// ever turned into an elicitation: a backend that returns a result of the
// same shape records nothing.
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
// supports URL mode elicitation to open the sign-in link of muster's
// auth_required answer or core_auth_login challenge, instead of handing the
// link over as tool output (MCP elicitation, URL mode). The link then never
// passes through the model's context.
//
// A request on protocol 2026-07-28 or later gets an input_required result
// carrying the elicitation, with the server as its requestState. The client
// retries the call once the person agreed to open the link: that retry first
// waits for the session's sign-in to the server (awaitSignIn), then runs as
// any other call, so a sign-in that did not complete is asked again. A retry
// carrying a declined or cancelled elicitation runs nothing and is answered
// with a tool error. The echoed requestState only picks which sign-in to wait
// for: the call is gated as usual afterwards. A request on an earlier
// protocol gets URLElicitationRequiredError (-32042), and the session is sent
// notifications/elicitation/complete when the sign-in completes
// (signInElicitations). A client that does not declare elicitation.url keeps
// the auth_required answer.
func (a *AggregatorServer) urlElicitation() mcpserver.ToolHandlerMiddleware {
	return func(next mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if !clientSupportsURLElicitation(ctx) {
				return next(ctx, req)
			}
			if answer := mcpserver.ElicitationResponse(req.Params.InputResponses, signInInputKey); answer != nil {
				server := req.Params.RequestState
				if answer.Action != mcp.ElicitationResponseActionAccept {
					return declinedSignInResult(answer.Action, server), nil
				}
				a.awaitSignIn(ctx, getSessionIDFromContext(ctx), server)
				req.Params.MultiRoundTripParams = mcp.MultiRoundTripParams{}
			}

			callCtx, recorder := withChallengeRecorder(ctx)
			result, err := next(callCtx, req)
			if err != nil {
				return result, err
			}
			challenge := recorder.challenge.Load()
			if challenge == nil || signInLink(result) != challenge.authURL {
				return result, err
			}
			return a.elicitSignIn(callCtx, *challenge)
		}
	}
}

// declinedSignInResult answers a retry whose sign-in elicitation the person
// declined or cancelled. server is the client's echo of requestState.
func declinedSignInResult(action mcp.ElicitationResponseAction, server string) *mcp.CallToolResult {
	if server == "" {
		return mcp.NewToolResultError(fmt.Sprintf("The tool did not run: the person answered %s to signing in.", action))
	}
	return mcp.NewToolResultError(fmt.Sprintf("The tool did not run: the person answered %s to signing in to %s.", action, server))
}

// awaitSignIn waits, at most signInRetryWait and while ctx lasts, until the
// session is authenticated to the server.
func (a *AggregatorServer) awaitSignIn(ctx context.Context, sessionID, server string) {
	if a.authStore == nil || sessionID == "" || server == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, signInRetryWait)
	defer cancel()
	ticker := time.NewTicker(signInPollInterval)
	defer ticker.Stop()
	for {
		if authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, server); authenticated {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// elicitSignIn answers a tool call with a URL elicitation of the challenge's
// sign-in link, in the shape of the request's protocol version.
func (a *AggregatorServer) elicitSignIn(ctx context.Context, challenge signInChallenge) (*mcp.CallToolResult, error) {
	message := fmt.Sprintf("Sign in to %s to continue.", challenge.server)
	if challenge.reason != "" {
		message = fmt.Sprintf("Sign in to %s to continue: %s.", challenge.server, challenge.reason)
	}

	if mcp.IsModernProtocol(mcpserver.RequestProtocolVersion(ctx)) {
		return mcpserver.NewInputRequestBuilder(challenge.server).Elicit(signInInputKey, mcp.ElicitationParams{
			Mode:    mcp.ElicitationModeURL,
			Message: message,
			URL:     challenge.authURL,
		}).ToolResult(), nil
	}

	elicitationID := a.signInElicitations.add(challenge.sessionID, challenge.server, getTransportSessionID(ctx), challenge.authURL)
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

// signInLink returns the sign-in link a tool result carries in
// structuredContent.authUrl -- an auth_required answer or a core_auth_login
// challenge, directly or relayed by call_tool, which carries the wrapped
// tool's structuredContent -- or "".
func signInLink(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	structured, _ := result.StructuredContent.(map[string]any)
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
// session, server, transport session and sign-in link, so the transport
// sessions that received them are sent notifications/elicitation/complete
// when the sign-in completes. A transport session asked again with the same
// link gets the same elicitationId: it is one elicitation, completed once. A
// new link is a new elicitation.
//
// The zero value is ready to use. It is local to the replica: a sign-in that
// completes on another replica sends no notification, which the protocol
// allows (the notification is optional; the client keeps its own retry).
type signInElicitations struct {
	mu      sync.Mutex
	entries map[signInElicitationKey]signInElicitation
}

type signInElicitationKey struct {
	pendingSignInKey
	transportID string
	authURL     string
}

type signInElicitation struct {
	elicitationID string
	expiresAt     time.Time
}

// sentElicitation is an elicitation sent to a transport session.
type sentElicitation struct {
	transportID   string
	elicitationID string
}

// add returns the elicitationId the transport session is asked to open the
// session's sign-in link for the server with: the pending one for that link,
// or a new one. A request without a transport session gets an id that
// nothing completes.
func (s *signInElicitations) add(sessionID, server, transportID, authURL string) string {
	if transportID == "" {
		return rand.Text()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, e := range s.entries {
		if !now.Before(e.expiresAt) {
			delete(s.entries, key)
		}
	}
	key := signInElicitationKey{pendingSignInKey{sessionID: sessionID, server: server}, transportID, authURL}
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

// take removes the elicitations of the session's sign-in to the server and
// returns the unexpired ones.
func (s *signInElicitations) take(sessionID, server string) []sentElicitation {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var taken []sentElicitation
	for key, e := range s.entries {
		if key.pendingSignInKey != (pendingSignInKey{sessionID: sessionID, server: server}) {
			continue
		}
		delete(s.entries, key)
		if now.Before(e.expiresAt) {
			taken = append(taken, sentElicitation{transportID: key.transportID, elicitationID: e.elicitationID})
		}
	}
	return taken
}

// completeSignInElicitations sends notifications/elicitation/complete for
// each elicitation of the session's sign-in to the server.
func (a *AggregatorServer) completeSignInElicitations(sessionID, server string) {
	if a.mcpServer == nil {
		return
	}
	for _, sent := range a.signInElicitations.take(sessionID, server) {
		params := map[string]any{"elicitationId": sent.elicitationID}
		if err := a.mcpServer.SendNotificationToSpecificClient(sent.transportID, string(mcp.MethodNotificationElicitationComplete), params); err != nil {
			logging.Debug("Aggregator", "elicitation complete to session %s failed: %v",
				logging.TruncateIdentifier(sent.transportID), err)
		}
	}
}
