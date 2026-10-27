package api

import "strings"

// forwardedSessionPrefix starts the ID of a session mcp-oauth opens for a
// bearer a client forwarded (ForwardedIDTokenAcceptance.SessionID).
const forwardedSessionPrefix = "ext-"

// ActorSessionPrefix starts the ID of the muster session of a caller that
// authenticated with a Session sandbox's actor JWT.
const ActorSessionPrefix = "actor-"

// IsActorSession reports whether a muster session is an actor's: it holds
// copies of a person's login, never a login of its own.
func IsActorSession(sessionID string) bool {
	return strings.HasPrefix(sessionID, ActorSessionPrefix)
}

// IsForwardedSession reports whether a muster session was opened for a token
// a client forwarded, so muster holds no refresh token of its own for it.
func IsForwardedSession(sessionID string) bool {
	return strings.HasPrefix(sessionID, forwardedSessionPrefix)
}
