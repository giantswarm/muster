package brokerhttp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/broker"
	"github.com/giantswarm/muster/internal/config"
)

// fakeLifecycleSink records every callback invocation. Each method
// captures the arguments so a test can assert on them without mocks.
type fakeLifecycleSink struct {
	createdCalls  []sinkCreatedCall
	refreshFailed []sinkRefreshFailedCall
	revoked       []string
}

type sinkCreatedCall struct {
	sessionID, userID, idToken string
}

type sinkRefreshFailedCall struct {
	sessionID, userID, reason string
}

func (s *fakeLifecycleSink) OnSessionCreated(_ context.Context, sessionID, userID, idToken string) {
	s.createdCalls = append(s.createdCalls, sinkCreatedCall{sessionID, userID, idToken})
}

func (s *fakeLifecycleSink) OnTokenRefreshFailed(_ context.Context, sessionID, userID, reason string) {
	s.refreshFailed = append(s.refreshFailed, sinkRefreshFailedCall{sessionID, userID, reason})
}

func (s *fakeLifecycleSink) OnSessionRevoked(_ context.Context, sessionID string) {
	s.revoked = append(s.revoked, sessionID)
}

func newManagerForTest(t *testing.T) *broker.Manager {
	t.Helper()
	m := broker.NewManager(config.OAuthMCPClientConfig{
		Enabled:      true,
		PublicURL:    "https://muster.test",
		ClientID:     "muster-test",
		CallbackPath: "/oauth/proxy/callback",
	})
	require.NotNil(t, m)
	t.Cleanup(m.Stop)
	return m
}

// TestBrokerLifecycleOptions_WiresThreeNonNilCallbacks pins the count of
// lifecycle options the broker installs on mcp-oauth. The options are
// opaque (oauth.ServerOption is a private mutator), so this is a count
// pin not a kind pin: a refactor that swaps revocation for a second
// creation handler keeps the count at three. Drop one or return nil and
// the test trips.
func TestBrokerLifecycleOptions_WiresThreeNonNilCallbacks(t *testing.T) {
	t.Parallel()

	opts := brokerLifecycleOptions(nil, nil)

	require.Len(t, opts, 3)
	for i, opt := range opts {
		require.NotNil(t, opt, "option %d is nil", i)
	}
}

func TestPersistIDToken_NilManager_Noop(t *testing.T) {
	t.Parallel()
	require.NoError(t, persistIDToken(nil, "sid", "uid", "tok"))
}

func TestPersistIDToken_EmptyToken_Noop(t *testing.T) {
	t.Parallel()
	m := newManagerForTest(t)
	require.NoError(t, persistIDToken(m, "sid", "uid", ""))
}

func TestPersistIDToken_ForwardsToManager(t *testing.T) {
	t.Parallel()
	m := newManagerForTest(t)
	m.SetMusterIssuer("https://muster.example")

	// Payload: {"sub":"alice","exp":9999999999} — far-future expiry.
	idToken := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSIsImV4cCI6OTk5OTk5OTk5OX0.sig" //nolint:gosec
	require.NoError(t, persistIDToken(m, "family-1", "alice", idToken))

	stored := m.GetTokenByIssuer("family-1", "https://muster.example")
	require.NotNil(t, stored, "expected token persisted at muster issuer")
	require.Equal(t, idToken, stored.IDToken)
}

func TestHandlePersistError_NilError_Noop(t *testing.T) {
	t.Parallel()
	sink := &fakeLifecycleSink{}
	handlePersistError(t.Context(), nil, sink, "fam", "user", "reason")
	require.Empty(t, sink.refreshFailed)
}

func TestHandlePersistError_MalformedIDToken_FiresSink(t *testing.T) {
	t.Parallel()
	sink := &fakeLifecycleSink{}
	err := errors.Join(broker.ErrMalformedIDToken, errors.New("decode failed"))

	handlePersistError(t.Context(), err, sink, "fam", "user", reasonMalformedAtCreation)

	require.Len(t, sink.refreshFailed, 1)
	require.Equal(t, "fam", sink.refreshFailed[0].sessionID)
	require.Equal(t, "user", sink.refreshFailed[0].userID)
	require.Equal(t, reasonMalformedAtCreation, sink.refreshFailed[0].reason)
}

func TestHandlePersistError_MalformedIDToken_NilSinkTolerated(t *testing.T) {
	t.Parallel()
	err := errors.Join(broker.ErrMalformedIDToken, errors.New("decode failed"))
	require.NotPanics(t, func() {
		handlePersistError(t.Context(), err, nil, "fam", "user", reasonMalformedAtCreation)
	})
}

func TestHandlePersistError_OtherError_DoesNotFireSink(t *testing.T) {
	t.Parallel()
	sink := &fakeLifecycleSink{}
	// Non-malformed error path: must be logged (we can't assert log content
	// here, but the contract is "do not surface to sink").
	handlePersistError(t.Context(), errors.New("storage backend unavailable"), sink, "fam", "user", "any")
	require.Empty(t, sink.refreshFailed, "non-malformed errors must not produce a sink event")
}
