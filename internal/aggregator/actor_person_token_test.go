package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/credprovider"
	"github.com/giantswarm/muster/v5/internal/server"
)

// personMirrorHandler keeps the person's freshest login ID token beside the
// sessions' tokens, as the OAuth adapter does.
type personMirrorHandler struct {
	*lockedOAuthHandler
	person map[string]*api.OAuthToken
	// audiences records the audience each lookup asked for.
	audiences []string
}

var _ api.PersonLoginTokenMirror = (*personMirrorHandler)(nil)

func (h *personMirrorHandler) PersonLoginIDToken(subject, issuer, audience string) *api.OAuthToken {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.audiences = append(h.audiences, audience)
	if issuer != identityTestIssuer {
		return nil
	}
	return h.person[subject]
}

func (h *personMirrorHandler) ClearLoginIDToken(sessionID, issuer string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.tokens, sessionID+"|"+issuer)
}

func (h *personMirrorHandler) setPerson(subject string, token *api.OAuthToken) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.person[subject] = token
}

func newActorPersonTestAggregator(t *testing.T) (*AggregatorServer, *personMirrorHandler) {
	t.Helper()
	a, locked := newIdentityTestAggregator(t)
	handler := &personMirrorHandler{lockedOAuthHandler: locked, person: map[string]*api.OAuthToken{}}
	api.RegisterOAuthHandler(handler)
	a.subjectSessions = newSubjectSessionTracker()
	return a, handler
}

func testActorCaller() *credprovider.ActorCaller {
	return &credprovider.ActorCaller{Subject: "alice", Session: "agents/s1", SessionID: "actor-0123", Actor: "spiffe://x/actor"}
}

func TestWithPersonIDToken_PutsThePersonsTokenOnTheActorSession(t *testing.T) {
	a, handler := newActorPersonTestAggregator(t)
	exp := time.Now().Add(time.Hour)
	handler.setPerson("alice", &api.OAuthToken{IDToken: identityTokenBefore, ExpiresAt: exp})
	caller := testActorCaller()

	ctx := a.withPersonIDToken(context.Background(), caller)

	got, ok := server.GetIDTokenFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, identityTokenBefore, got, "the request carries the person's ID token")
	assert.Equal(t, identityTokenBefore,
		getIDTokenForForwarding(context.Background(), caller.SessionID, identityTestIssuer, nil),
		"a background connection of the actor session finds it")
}

func TestWithPersonIDToken_NoTokenLeavesTheRequestAlone(t *testing.T) {
	a, handler := newActorPersonTestAggregator(t)
	caller := testActorCaller()
	handler.setPerson("alice", &api.OAuthToken{IDToken: identityTokenBefore, ExpiresAt: time.Now().Add(time.Hour)})
	a.withPersonIDToken(context.Background(), caller)

	handler.setPerson("alice", nil) // the person signed out
	ctx := a.withPersonIDToken(context.Background(), caller)
	_, ok := server.GetIDTokenFromContext(ctx)
	assert.False(t, ok)
	assert.Empty(t, getIDTokenForForwarding(context.Background(), caller.SessionID, identityTestIssuer, nil),
		"the actor session's copy goes with the person's sign-out")
}

func TestWithPersonIDToken_RefreshesThePersonsLoginsOnce(t *testing.T) {
	a, handler := newActorPersonTestAggregator(t)
	a.subjectSessions.TrackOAuth("alice", "actor-0123")
	a.subjectSessions.TrackOAuth("alice", "family-dead")
	a.subjectSessions.TrackOAuth("alice", "family-live")
	a.subjectSessions.TrackOAuth("bob", "family-bob")
	var refreshed []string
	a.oauthHTTPServer = &refreshingOAuthServer{onRefresh: func(familyID string) {
		refreshed = append(refreshed, familyID)
		if familyID == "family-live" {
			handler.setPerson("alice", &api.OAuthToken{IDToken: identityTokenAfter, ExpiresAt: time.Now().Add(time.Hour)})
		}
	}}
	now := time.Now()
	a.personTokenRefreshes.now = func() time.Time { return now }

	ctx := a.withPersonIDToken(context.Background(), testActorCaller())
	got, _ := server.GetIDTokenFromContext(ctx)
	assert.Equal(t, identityTokenAfter, got, "a refreshed login of the person serves the actor")
	assert.NotContains(t, refreshed, "actor-0123", "the actor session has no login to refresh")
	assert.NotContains(t, refreshed, "family-bob", "another person's login is never touched")
	assert.Contains(t, refreshed, "family-live")

	handler.setPerson("alice", nil)
	refreshed = nil
	a.withPersonIDToken(context.Background(), testActorCaller())
	assert.Empty(t, refreshed, "within the backoff no login is refreshed again")

	now = now.Add(personTokenRefreshBackoff)
	a.withPersonIDToken(context.Background(), testActorCaller())
	assert.NotEmpty(t, refreshed, "after the backoff the logins are refreshed again")
}

func TestWithPersonIDToken_OAuthOff(t *testing.T) {
	a, _ := newActorPersonTestAggregator(t)
	api.RegisterOAuthHandler(nil)
	ctx := context.Background()
	assert.Equal(t, ctx, a.withPersonIDToken(ctx, testActorCaller()))
}

// visibilityWithAudience is a server visibility check whose listings need
// audience on the caller's token.
type visibilityWithAudience struct{ audience string }

func (v visibilityWithAudience) CallerVisibility(context.Context) api.ServerVisibility {
	return api.AllServersVisible()
}

func (v visibilityWithAudience) LoginAudience() string { return v.audience }

func TestWithPersonIDToken_AsksForTheAudienceTheVisibilityCheckNeeds(t *testing.T) {
	a, handler := newActorPersonTestAggregator(t)
	handler.setPerson("alice", &api.OAuthToken{IDToken: identityTokenBefore, ExpiresAt: time.Now().Add(time.Hour)})

	a.withPersonIDToken(context.Background(), testActorCaller())
	api.RegisterMCPServerVisibility(visibilityWithAudience{audience: "dex-k8s-authenticator"})
	t.Cleanup(func() { api.RegisterMCPServerVisibility(nil) })
	a.withPersonIDToken(context.Background(), testActorCaller())

	assert.Equal(t, []string{"", "dex-k8s-authenticator"}, handler.audiences)
}
