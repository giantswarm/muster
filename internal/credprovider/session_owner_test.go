package credprovider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// personBearer is the bearer a person presents in the tests.
func personBearer(sub string) string { return "token-of-" + sub }

// fakeSession is a kagent Session in fakeSessions.
type fakeSession struct {
	owner, namespace, authority string
}

// fakeSessions answers GetSession as kagent does: only the owner's bearer
// reads a Session, any other bearer gets ErrSessionNotFound as for a missing
// one. err, when set, is answered for every lookup.
type fakeSessions struct {
	sessions map[string]fakeSession
	err      error
}

// testSessions holds s1, alice's Session in kagent running testActor.
func testSessions() *fakeSessions {
	return &fakeSessions{sessions: map[string]fakeSession{
		"s1": {owner: "alice", namespace: "kagent", authority: "session-a.kagent" + actorAuthoritySuffix},
	}}
}

func (f *fakeSessions) lookup(_ context.Context, bearer, id string) (*KagentSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	s, ok := f.sessions[id]
	if !ok || bearer != personBearer(s.owner) {
		return nil, ErrSessionNotFound
	}
	return &KagentSession{AgentNamespace: s.namespace, A2AAuthority: s.authority}, nil
}

func newSessionsProvider(t *testing.T, sessions *fakeSessions) (*Provider, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	p, err := NewProvider(Options{
		GatewayIdentity: testGateway,
		Releasable:      map[string]string{"github": testIssuer},
		Store:           store,
		Grants:          fakeGrants{}.source,
		Sessions:        sessions.lookup,
	})
	require.NoError(t, err)
	return p, store
}

func bindBody(actor, session string) string {
	return `{"actor":"` + actor + `","session":"` + session + `","providers":["github"]}`
}

func TestBindRequiresTheCallersOwnSessionAndItsActor(t *testing.T) {
	sessions := testSessions()
	sessions.sessions["s2"] = fakeSession{owner: "bob", namespace: "kagent", authority: "session-b.kagent" + actorAuthoritySuffix}
	sessions.sessions["s3"] = fakeSession{owner: "alice", namespace: "kagent", authority: "not-an-actor.example.com"}
	p, store := newSessionsProvider(t, sessions)
	bobsActor := "spiffe://substrate-actor.local/actor/kagent/session-b"

	cases := map[string]struct {
		sub, body, want string
	}{
		"another person's session":       {"alice", bindBody(bobsActor, "kagent/s2"), "does not exist or is not the caller's"},
		"an unknown session":             {"alice", bindBody(testActor, "kagent/nope"), "does not exist or is not the caller's"},
		"another session's actor":        {"bob", bindBody(testActor, "kagent/s2"), "the actor is not the session's: session s2 runs actor " + bobsActor},
		"the actor of someone's session": {"mallory", bindBody(testActor, "kagent/s1"), "does not exist or is not the caller's"},
		"another namespace":              {"alice", bindBody(testActor, "other/s1"), `belongs to an Agent in namespace "kagent"`},
		"a session without an actor":     {"alice", bindBody(testActor, "kagent/s3"), "names no Substrate actor"},
		"a session that is no reference": {"alice", bindBody(testActor, "s1"), "is not <namespace>/<id>"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := bind(t, p, tc.sub, tc.body)
			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.want)
		})
	}
	for _, actor := range []string{testActor, bobsActor} {
		b, err := store.Get(t.Context(), actor)
		require.NoError(t, err)
		assert.Nil(t, b, "a refused bind writes nothing")
	}

	require.Equal(t, http.StatusNoContent, bind(t, p, "alice", testBindBody).Code, "alice binds her own session's actor")
	require.Equal(t, http.StatusNoContent, bind(t, p, "bob", bindBody(bobsActor, "kagent/s2")).Code, "bob binds his")
}

func TestBindForwardsTheCallersBearer(t *testing.T) {
	var seen string
	p, _ := newSessionsProvider(t, testSessions())
	inner := p.sessions
	p.sessions = func(ctx context.Context, bearer, id string) (*KagentSession, error) {
		seen = bearer
		return inner(ctx, bearer, id)
	}
	require.Equal(t, http.StatusNoContent, bind(t, p, "alice", testBindBody).Code)
	assert.Equal(t, personBearer("alice"), seen, "kagent is asked with the caller's own bearer")
}

func TestBindWhenKagentCannotAnswer(t *testing.T) {
	cases := map[string]struct {
		err  error
		code int
	}{
		"the caller's token refused": {ErrSessionCallerRefused, http.StatusForbidden},
		"controller unreachable":     {errors.New("connection refused"), http.StatusServiceUnavailable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p, store := newSessionsProvider(t, &fakeSessions{err: tc.err})
			rec := bind(t, p, "alice", testBindBody)
			assert.Equal(t, tc.code, rec.Code)
			assert.False(t, strings.Contains(rec.Body.String(), "connection refused"), "an outage is no detail for the caller")
			b, err := store.Get(t.Context(), testActor)
			require.NoError(t, err)
			assert.Nil(t, b, "nothing is bound unchecked")
		})
	}
}

func TestActorFromAuthority(t *testing.T) {
	actor, err := actorFromAuthority("session-a.kagent" + actorAuthoritySuffix)
	require.NoError(t, err)
	assert.Equal(t, testActor, actor)
	for _, bad := range []string{"", "session-a" + actorAuthoritySuffix, "a.b.c" + actorAuthoritySuffix, ".kagent" + actorAuthoritySuffix, "session-a.kagent.example.com"} {
		_, err := actorFromAuthority(bad)
		assert.Error(t, err, bad)
	}
}
