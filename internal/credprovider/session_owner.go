package credprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// actorAuthoritySuffix ends the A2A authority kagent records for a Session's
// actor: <actor>.<atespace>.actors.resources.substrate.ate.dev.
const actorAuthoritySuffix = ".actors.resources.substrate.ate.dev"

var (
	// ErrSessionNotFound is a Session the controller does not answer to the
	// caller: missing, deleted, or another person's.
	ErrSessionNotFound = errors.New("the session does not exist or is not the caller's")
	// ErrSessionCallerRefused is a caller token the controller refused.
	ErrSessionCallerRefused = errors.New("the kagent controller did not accept the caller's token")
)

// KagentSession is what a bind checks of the Session it names.
type KagentSession struct {
	// AgentNamespace is the namespace of the Session's Agent.
	AgentNamespace string
	// A2AAuthority is the Session's actor address.
	A2AAuthority string
}

// SessionLookup reads the kagent Session sessionID with bearer as the only
// identity, so the controller answers only the caller's own Sessions. It
// fails with ErrSessionNotFound, ErrSessionCallerRefused, or another error
// when the controller cannot be asked.
type SessionLookup func(ctx context.Context, bearer, sessionID string) (*KagentSession, error)

// errNotSessionActor is a bind whose actor or namespace is not its Session's.
var errNotSessionActor = errors.New("the actor is not the session's")

// checkSessionActor reads the Session a bind names as the caller and requires
// its Agent's namespace and its actor to be the bind's.
func (p *Provider) checkSessionActor(ctx context.Context, bearer, session, actor string) error {
	namespace, id, ok := strings.Cut(session, "/")
	if !ok || namespace == "" || id == "" || strings.Contains(id, "/") {
		return fmt.Errorf("%w: session %q is not <namespace>/<id>", errNotSessionActor, session)
	}
	s, err := p.sessions(ctx, bearer, id)
	if err != nil {
		return err
	}
	if s.AgentNamespace != namespace {
		return fmt.Errorf("%w: session %s belongs to an Agent in namespace %q", errNotSessionActor, id, s.AgentNamespace)
	}
	sessionActor, err := actorFromAuthority(s.A2AAuthority)
	if err != nil {
		return fmt.Errorf("%w: session %s: %v", errNotSessionActor, id, err)
	}
	if sessionActor != actor {
		return fmt.Errorf("%w: session %s runs actor %s", errNotSessionActor, id, sessionActor)
	}
	return nil
}

// actorFromAuthority maps a Session's A2A authority,
// <actor>.<atespace>.actors.resources.substrate.ate.dev, to the actor's
// SPIFFE id.
func actorFromAuthority(authority string) (string, error) {
	name, ok := strings.CutSuffix(authority, actorAuthoritySuffix)
	actor, atespace, hasAtespace := strings.Cut(name, ".")
	if !ok || !hasAtespace || actor == "" || atespace == "" || strings.Contains(atespace, ".") {
		return "", fmt.Errorf("a2a authority %q names no Substrate actor", authority)
	}
	return actorSPIFFEPrefix + atespace + "/" + actor, nil
}

// bearerToken returns the token of a "Bearer <token>" Authorization value, or
// "".
func bearerToken(value string) string {
	const prefix = "Bearer "
	if len(value) > len(prefix) && strings.EqualFold(value[:len(prefix)], prefix) {
		return value[len(prefix):]
	}
	return ""
}
