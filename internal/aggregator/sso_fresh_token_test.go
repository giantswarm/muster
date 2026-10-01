package aggregator

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/server"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// forwardedTokenIssuedAt returns a session context for alice whose
// forwardable ID token was issued at iat.
func forwardedTokenIssuedAt(t *testing.T, iat time.Time) context.Context {
	t.Helper()
	idToken := unsignedJWT(t, map[string]any{
		"sub": "alice", "iss": "https://dex.example.com", "aud": []string{"muster"},
		"iat": iat.Unix(), "exp": iat.Add(time.Hour).Unix(),
	})
	ctx := server.ContextWithCallerTokens(context.Background(), server.CallerTokens{IDToken: idToken, Bearer: idToken})
	ctx = api.WithSubject(ctx, "alice")
	return api.WithSessionID(ctx, "session-1")
}

// scheduledRetry is one retry muster scheduled through ssoRetryAfter.
type scheduledRetry struct {
	delay time.Duration
	run   func()
}

// captureSSORetries makes a's scheduled retries land in the returned channel
// instead of on a timer, so a test runs them when it chooses.
func captureSSORetries(a *AggregatorServer) <-chan scheduledRetry {
	retries := make(chan scheduledRetry, 8)
	a.ssoRetryAfter = func(delay time.Duration, retry func()) {
		retries <- scheduledRetry{delay: delay, run: retry}
	}
	return retries
}

func nextRetry(t *testing.T, retries <-chan scheduledRetry) scheduledRetry {
	t.Helper()
	select {
	case r := <-retries:
		return r
	default:
		require.FailNow(t, "no retry was scheduled")
		return scheduledRetry{}
	}
}

// TestEstablishSSOConnection_FreshTokenRefusedIsRetriedSoon: right after the
// issuer rotated its signing key, a backend (or the gateway in front of it)
// refuses a token issued moments ago with 401 until it fetched the new key.
// muster holds the server back on the short transport backoff, reports
// sso_pending with the reason, and retries on its own: once the backend
// accepts the token, the retry connects without a request from the person.
func TestEstablishSSOConnection_FreshTokenRefusedIsRetriedSoon(t *testing.T) {
	backend := newFlakyBackend(t)
	a, info, _ := newForwardingTestAggregator(t, backend.URL+"/mcp")
	retries := captureSSORetries(a)
	ctx := forwardedTokenIssuedAt(t, time.Now().Add(-30*time.Second))

	backend.fail.Store(http.StatusUnauthorized)
	require.Equal(t, ssoConnectFailed, a.establishSSOConnection(ctx, info, "https://dex.example.com"))

	require.True(t, a.ssoTracker.HasSSOFreshTokenRefused("alice", "backend"),
		"a 401 on a token issued 30s ago is a fresh-token refusal, not a refused credential")
	failure, ok := a.ssoTracker.activeFailure("alice", "backend")
	require.True(t, ok)
	assert.Equal(t, ssoTransportBackoffBase, failure.backoff(),
		"a fresh-token refusal is held back on the short schedule, not the 5-30 min auth backoff")
	assert.Contains(t, failure.reason, "fresh token refused: the backend may not know the issuer's new signing key yet")
	assert.Equal(t, pkgoauth.SessionServerStatusSSOPending,
		a.determineSessionAuthStatus("alice", "session-1", "backend", info),
		"muster retries on its own, so the person is not asked to sign in again")

	retry := nextRetry(t, retries)
	assert.Equal(t, ssoTransportBackoffBase, retry.delay, "the retry comes after the short backoff")

	// The backend still does not know the key: the next retry backs off further.
	retry.run()
	retry = nextRetry(t, retries)
	assert.Equal(t, 2*ssoTransportBackoffBase, retry.delay)
	assert.True(t, a.ssoTracker.HasSSOFreshTokenRefused("alice", "backend"))

	// The backend fetched the new key: the retry connects.
	backend.fail.Store(0)
	retry.run()
	_, pooled := a.connPool.Get("session-1", "backend")
	assert.True(t, pooled, "the retry connected the backend without a request from the person")
	assert.Empty(t, retries, "a successful retry schedules none")
}

// TestScheduleSSORetry_FollowsTheSessionNotItsToolListing: a session that has
// only initialized (no tools/list, no tool call yet) is retried as well; once
// the session is torn down, its pending retry does nothing.
func TestScheduleSSORetry_FollowsTheSessionNotItsToolListing(t *testing.T) {
	for name, endSession := range map[string]bool{"live session": false, "ended session": true} {
		t.Run(name, func(t *testing.T) {
			backend := newFlakyBackend(t)
			a, info, _ := newForwardingTestAggregator(t, backend.URL+"/mcp")
			a.subjectSessions = newSubjectSessionTracker()
			retries := captureSSORetries(a)
			ctx := forwardedTokenIssuedAt(t, time.Now().Add(-30*time.Second))

			backend.fail.Store(http.StatusUnauthorized)
			require.Equal(t, ssoConnectFailed, a.establishSSOConnection(ctx, info, "https://dex.example.com"))
			retry := nextRetry(t, retries)

			backend.fail.Store(0)
			if endSession {
				a.subjectSessions.UntrackOAuth("session-1")
			}
			retry.run()
			_, pooled := a.connPool.Get("session-1", "backend")
			assert.Equal(t, !endSession, pooled)
		})
	}
}

// TestEstablishSSOConnection_OldTokenRefusedKeepsTheAuthBackoff: a 401 for a
// token issued longer ago than the gateway's JWKS refresh is the backend's
// verdict on the token (expired, wrongly signed, wrong audience). It keeps the
// long auth backoff and reauth_required, and muster schedules no retry.
func TestEstablishSSOConnection_OldTokenRefusedKeepsTheAuthBackoff(t *testing.T) {
	for name, ctxFor := range map[string]func(t *testing.T) context.Context{
		"issued 10 min ago": func(t *testing.T) context.Context {
			return forwardedTokenIssuedAt(t, time.Now().Add(-10*time.Minute))
		},
		"without iat": func(t *testing.T) context.Context {
			_, _, ctx := newForwardingTestAggregator(t, "http://unused")
			return ctx
		},
	} {
		t.Run(name, func(t *testing.T) {
			backend := newFlakyBackend(t)
			a, info, _ := newForwardingTestAggregator(t, backend.URL+"/mcp")
			retries := captureSSORetries(a)
			ctx := ctxFor(t)

			backend.fail.Store(http.StatusUnauthorized)
			require.Equal(t, ssoConnectFailed, a.establishSSOConnection(ctx, info, "https://dex.example.com"))

			assert.False(t, a.ssoTracker.HasSSOFreshTokenRefused("alice", "backend"))
			failure, ok := a.ssoTracker.activeFailure("alice", "backend")
			require.True(t, ok)
			assert.Equal(t, ssoTrackerFailureTTL, failure.backoff())
			assert.Equal(t, pkgoauth.SessionServerStatusReauthRequired,
				a.determineSessionAuthStatus("alice", "session-1", "backend", info))
			assert.Empty(t, retries, "a refused credential is not retried by muster")
		})
	}
}

// TestEstablishSSOConnection_FreshTokenRefusalByItsCause: a fresh token's
// 401 is a possibly unknown signing key only when the backend names no other
// cause. One that names the audience is a refused credential at once.
func TestEstablishSSOConnection_FreshTokenRefusalByItsCause(t *testing.T) {
	for description, wantFresh := range map[string]bool{
		"":                                      true,
		"token uses the unknown key":            true,
		"no key found for kid":                  true,
		"token validation failed: audience [x]": false,
		"token has expired":                     false,
	} {
		t.Run(description, func(t *testing.T) {
			backend := newFlakyBackend(t)
			backend.description.Store(description)
			a, info, _ := newForwardingTestAggregator(t, backend.URL+"/mcp")
			retries := captureSSORetries(a)

			backend.fail.Store(http.StatusUnauthorized)
			ctx := forwardedTokenIssuedAt(t, time.Now().Add(-time.Minute))
			require.Equal(t, ssoConnectFailed, a.establishSSOConnection(ctx, info, "https://dex.example.com"))

			assert.Equal(t, wantFresh, a.ssoTracker.HasSSOFreshTokenRefused("alice", "backend"))
			assert.Equal(t, wantFresh, len(retries) == 1, "muster retries only a possibly unknown key")
		})
	}
}

// TestSSOTracker_FreshTokenRefusalsDoubleWhileTheTokenIsFresh: muster's own
// retry fails just as the previous backoff expired; the count continues, so
// the retries double from 15 s to 2 min instead of repeating every 15 s.
func TestSSOTracker_FreshTokenRefusalsDoubleWhileTheTokenIsFresh(t *testing.T) {
	tr := newSSOTracker()
	tr.MarkSSOFreshTokenRefused("u", "s", "fresh token refused")
	expireSSOFailure(t, tr, "u", "s")
	require.False(t, tr.HasSSOFailed("u", "s"))

	tr.MarkSSOFreshTokenRefused("u", "s", "fresh token refused")
	assert.Equal(t, 2, tr.GetFailureCount("u", "s"))
	failure, ok := tr.activeFailure("u", "s")
	require.True(t, ok)
	assert.Equal(t, 30*time.Second, failure.backoff())
}

// TestScheduleSSORetry_RunsForASessionThatOnlySignedIn: the retry skips a
// session that ended meanwhile, which it reads from the subject tracker. A
// session joins that tracker on its first tools/list or tools/call, so a
// session that only signed in (a portal sign-in whose fan-out was refused,
// the person watching auth://status) was never retried. Scheduling the retry
// records the session as live, and the retry connects it.
func TestScheduleSSORetry_RunsForASessionThatOnlySignedIn(t *testing.T) {
	backend := newFlakyBackend(t)
	a, info, _ := newForwardingTestAggregator(t, backend.URL+"/mcp")
	a.subjectSessions = newSubjectSessionTracker()
	retries := captureSSORetries(a)
	ctx := forwardedTokenIssuedAt(t, time.Now().Add(-30*time.Second))

	backend.fail.Store(http.StatusUnauthorized)
	require.Equal(t, ssoConnectFailed, a.establishSSOConnection(ctx, info, "https://dex.example.com"))
	retry := nextRetry(t, retries)
	assert.Equal(t, "alice", a.subjectSessions.OAuthSubject("session-1"),
		"scheduling the retry records the session as live")

	backend.fail.Store(0)
	retry.run()
	_, pooled := a.connPool.Get("session-1", "backend")
	assert.True(t, pooled, "a session that signed in and listed no tool yet is retried")
}

// TestScheduleSSORetry_SkipsASessionTornDownMeanwhile: a session torn down
// between the refusal and the retry (signed out, its bearer expired) is not
// connected again.
func TestScheduleSSORetry_SkipsASessionTornDownMeanwhile(t *testing.T) {
	backend := newFlakyBackend(t)
	a, info, _ := newForwardingTestAggregator(t, backend.URL+"/mcp")
	a.subjectSessions = newSubjectSessionTracker()
	retries := captureSSORetries(a)
	ctx := forwardedTokenIssuedAt(t, time.Now().Add(-30*time.Second))

	backend.fail.Store(http.StatusUnauthorized)
	require.Equal(t, ssoConnectFailed, a.establishSSOConnection(ctx, info, "https://dex.example.com"))
	retry := nextRetry(t, retries)

	a.tearDownSession(context.Background(), "session-1")
	backend.fail.Store(0)
	retry.run()
	_, pooled := a.connPool.Get("session-1", "backend")
	assert.False(t, pooled, "a session that ended is not connected by the retry")
	assert.Empty(t, retries)
}
