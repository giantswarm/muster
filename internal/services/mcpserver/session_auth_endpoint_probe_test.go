package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/events"
	"github.com/giantswarm/muster/v5/internal/services"
)

// stubTokenEndpointProber is an OAuth handler that can probe a token
// endpoint, answering every probe with a fixed outcome. Only the probe is
// implemented; the embedded interface panics on anything else.
type stubTokenEndpointProber struct {
	api.OAuthHandler

	mu      sync.Mutex
	answer  error
	configs []api.TokenExchangeConfig
}

func (p *stubTokenEndpointProber) ProbeTokenEndpoint(_ context.Context, config *api.TokenExchangeConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configs = append(p.configs, *config)
	return p.answer
}

func (p *stubTokenEndpointProber) answerWith(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answer = err
}

func (p *stubTokenEndpointProber) probes() []api.TokenExchangeConfig {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]api.TokenExchangeConfig(nil), p.configs...)
}

func registerProber(t *testing.T) *stubTokenEndpointProber {
	t.Helper()
	prober := &stubTokenEndpointProber{}
	prev := api.GetOAuthHandler()
	api.RegisterOAuthHandler(prober)
	t.Cleanup(func() { api.RegisterOAuthHandler(prev) })
	return prober
}

// errEndpointTimeout is how a caller's exchange fails while the token endpoint
// does not answer.
var errEndpointTimeout = fmt.Errorf("token exchange failed: %w",
	&url.Error{Op: "Post", URL: "https://dex.remote.example.test/token", Err: context.DeadlineExceeded})

// errProbeRejectedSubjectToken is the answer of a token endpoint that is up: the
// probe's subject token is no token its issuer signed.
var errProbeRejectedSubjectToken = errors.New("token exchange failed: invalid_request - Unable to verify subject token")

// TestTokenEndpointTimeoutRecoversWithoutASession: a caller's exchange times
// out, so the server is Failed for its token endpoint and put on the
// reconnect backoff. The orchestrator's retries probe the token endpoint;
// while it does not answer the server stays Failed (never unreachable: the
// MCP endpoint answers), and the first probe it answers returns the server to
// Awaiting Session, with no user session involved (issue #1368).
func TestTokenEndpointTimeoutRecoversWithoutASession(t *testing.T) {
	withBackoff(t, time.Second, time.Second)
	rec := recordEvents(t)
	prober := registerProber(t)
	secrets := &stubSecretHandler{}
	prevSecrets := api.GetSecretCredentialsHandler()
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prevSecrets) })

	backend := startHTTPStatusServer(t, http.StatusUnauthorized)
	svc, err := NewService(tokenExchangeDefinition(backend.URL, true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	require.True(t, api.IsAuthRequiredError(svc.Start(t.Context())))
	require.Equal(t, services.StateAwaitingSession, svc.GetState())
	assert.Empty(t, prober.probes(), "no probe while no exchange has failed on the endpoint")

	svc.RecordTokenExchangeOutcome(errEndpointTimeout)
	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.Equal(t, string(api.TokenExchangeFailureEndpoint), svc.GetServiceData()[api.ServiceDataFailureReason])
	require.NotNil(t, svc.GetNextRetryAfter(), "a token endpoint that does not answer is looked at again on the backoff")
	assert.Equal(t, 1, svc.GetConsecutiveFailures())
	failed := rec.texts(string(events.ReasonMCPServerFailed))
	require.Len(t, failed, 1)
	assert.Contains(t, failed[0], "token exchange fails for every caller (TokenExchangeEndpoint, no HTTP response from the token endpoint, next retry in 1s at ")

	// The orchestrator's retries while the endpoint is still down.
	prober.answerWith(errEndpointTimeout)
	for attempt := 2; attempt <= 4; attempt++ {
		firstAttempt := svc.GetLastAttempt()
		err = svc.Start(t.Context())
		require.Error(t, err)
		assert.False(t, api.IsAuthRequiredError(err), "attempt %d: the token endpoint does not answer: a failed start", attempt)
		assert.Equal(t, services.StateFailed, svc.GetState(), "attempt %d: Failed, never unreachable", attempt)
		assert.Equal(t, attempt, svc.GetConsecutiveFailures())
		require.NotNil(t, svc.GetNextRetryAfter())
		assert.NotEqual(t, firstAttempt, svc.GetLastAttempt(), "each probe is an attempt")
		assert.Equal(t, string(api.TokenExchangeFailureEndpoint), svc.GetServiceData()[api.ServiceDataFailureReason])
		assert.ErrorContains(t, svc.GetLastError(), "token endpoint https://dex.remote.example.test/token did not answer")
	}
	failed = rec.texts(string(events.ReasonMCPServerFailed))
	assert.Contains(t, failed[len(failed)-1], "until the token endpoint answers (no HTTP response from the token endpoint, next retry in 1s at ")

	probes := prober.probes()
	require.Len(t, probes, 3)
	assert.Equal(t, "muster-token-exchange", probes[0].ClientID, "the probe authenticates as every exchange does")
	assert.Equal(t, "https://dex.remote.example.test/token", probes[0].DexTokenEndpoint)

	// The endpoint answers again: the next retry settles the server.
	prober.answerWith(errProbeRejectedSubjectToken)
	err = svc.Start(t.Context())
	require.True(t, api.IsAuthRequiredError(err), "%v", err)
	assert.Equal(t, services.StateAwaitingSession, svc.GetState())
	assert.Equal(t, 0, svc.GetConsecutiveFailures())
	assert.Nil(t, svc.GetNextRetryAfter())
	assert.NotContains(t, svc.GetServiceData(), api.ServiceDataFailureReason)

	// Recovered: later starts do not probe.
	require.True(t, api.IsAuthRequiredError(svc.Start(t.Context())))
	assert.Len(t, prober.probes(), 4)
}

// TestTokenEndpointOAuthErrorIsNotRetried: an OAuth error from the token
// endpoint is the server's own configuration, so neither a caller's exchange
// nor a probe that gets one puts the server on the backoff: it would only
// read Awaiting Session again while every exchange still fails.
func TestTokenEndpointOAuthErrorIsNotRetried(t *testing.T) {
	withBackoff(t, time.Second, time.Second)
	recordEvents(t)
	prober := registerProber(t)

	backend := startHTTPStatusServer(t, http.StatusUnauthorized)
	svc, err := NewService(tokenExchangeDefinition(backend.URL, false))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })
	require.True(t, api.IsAuthRequiredError(svc.Start(t.Context())))

	invalidClient := errors.New("token exchange failed: invalid_client - Invalid client credentials.")
	svc.RecordTokenExchangeOutcome(invalidClient)
	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.Nil(t, svc.GetNextRetryAfter())

	// The endpoint timed out once, and the probe then finds the client
	// rejected: Failed for the credentials, and no further probes.
	svc.RecordTokenExchangeOutcome(errEndpointTimeout)
	require.NotNil(t, svc.GetNextRetryAfter())
	prober.answerWith(invalidClient)
	err = svc.Start(t.Context())
	require.Error(t, err)
	assert.False(t, api.IsAuthRequiredError(err))
	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.Equal(t, string(api.TokenExchangeFailureCredentials), svc.GetServiceData()[api.ServiceDataFailureReason])
	assert.ErrorContains(t, svc.GetLastError(), "invalid_client")

	assert.Nil(t, svc.GetNextRetryAfter(), "the endpoint answered: the outage's schedule ends, no restart reads Awaiting Session")
	assert.False(t, svc.isTokenEndpointProbeDue(), "the endpoint answered: nothing left to probe")
	assert.False(t, svc.isTransientConnectivityError(svc.GetLastError()), "an OAuth error is not retried in a loop")
}

// TestTokenEndpointProbeClearedByACallersExchange: a caller's successful
// exchange proves the endpoint answers, so the reconnect schedule and the
// probe due end with it.
func TestTokenEndpointProbeClearedByACallersExchange(t *testing.T) {
	withBackoff(t, time.Second, time.Second)
	recordEvents(t)
	prober := registerProber(t)

	backend := startHTTPStatusServer(t, http.StatusUnauthorized)
	svc, err := NewService(tokenExchangeDefinition(backend.URL, false))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })
	require.True(t, api.IsAuthRequiredError(svc.Start(t.Context())))

	svc.RecordTokenExchangeOutcome(errEndpointTimeout)
	require.NotNil(t, svc.GetNextRetryAfter())
	svc.RecordTokenExchangeOutcome(nil)
	assert.Equal(t, services.StateAwaitingSession, svc.GetState())
	assert.Nil(t, svc.GetNextRetryAfter())
	assert.Equal(t, 0, svc.GetConsecutiveFailures())

	require.True(t, api.IsAuthRequiredError(svc.Start(t.Context())))
	assert.Empty(t, prober.probes())
}
