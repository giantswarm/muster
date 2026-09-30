package mcpserver

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/internal/events"
	"github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/internal/services"
)

// sessionAuthState is what a service served per session (auth.forwardToken,
// auth.tokenExchange) knows about its callers' connections beyond the service
// state itself. The aggregator reports every per-user token exchange through
// api.ReportMCPServerTokenExchange; the reconciler reads the result into the
// CR's Ready condition. Protected by Service.sessionAuthMutex.
type sessionAuthState struct {
	// failureReason is the class of the failure the service is Failed for,
	// when that failure is the token exchange's rather than the endpoint's.
	// Cleared by every attempt that reaches the endpoint and by a successful
	// exchange.
	failureReason api.TokenExchangeFailureClass
	// exchangeFailure is the exchange error that put the service in Failed,
	// nil while no such failure is in force.
	exchangeFailure error
	// lastExchangeSucceededAt is when a caller's token was last exchanged
	// successfully; nil until the first success since the process started.
	lastExchangeSucceededAt *time.Time
	// endpointProbeDue is set when an exchange failed because the token
	// endpoint did not answer. Every start then probes the token endpoint
	// before the server settles in Awaiting Session, so the reconnect backoff
	// finds out on its own when the endpoint is back. Cleared when the
	// endpoint answers, to a probe or to a caller's exchange; unlike
	// failureReason it survives the reset at the top of Start, which is what
	// the probe runs after.
	endpointProbeDue bool
}

// settleSessionAuth ends a start whose probe reached a server served per
// session: the endpoint answered (with a 401, or with an anonymous initialize
// that discardAnonymousProbe has closed), so the server is up, and every
// connection to it belongs to a session. Before the server settles in Awaiting
// Session, the one thing muster can verify without a caller is verified: the
// client credentials a token exchange needs. A Secret that is missing or
// unreadable fails every caller the same way, so the server is Failed for it,
// with the reconnect backoff so the Secret appearing later heals it without a
// restart.
//
// The returned AuthRequiredError is what the orchestrator's retry and the
// reconciler classify as "not a failed start" (api.IsAuthRequiredError).
//
// A server whose token endpoint stopped answering a caller's exchange also has
// its token endpoint probed here, see probeTokenEndpoint.
func (s *Service) settleSessionAuth(ctx context.Context, authErr *mcpserver.AuthRequiredError) error {
	credentials, err := s.loadTokenExchangeCredentials(ctx)
	if err != nil {
		s.setFailureReason(api.TokenExchangeFailureCredentials)
		return s.failStart(err)
	}
	if err := s.probeTokenEndpoint(ctx, credentials); err != nil {
		return s.failStart(err)
	}
	s.enterAwaitingSession(authErr)
	return authErr
}

// probeTokenEndpoint checks, while endpointProbeDue is set, that the token
// endpoint answers as an authorization server again, with the client
// credentials and HTTP client a caller's exchange uses. The probe's answer is
// classified like an exchange's:
//   - the endpoint rejected the probe's subject token: it is back, the probe
//     is no longer due and the server settles in Awaiting Session;
//   - no answer (transport, 5xx, a non-OAuth page from a proxy): a
//     TokenEndpointUnavailableError, which failStart puts on the reconnect
//     backoff, so the next probe follows without any caller;
//   - an OAuth error that is the server's (invalid_client, an unknown
//     connector): the endpoint answers, so the probe is no longer due, and the
//     server is Failed for that error with no retry, as an exchange that got
//     it would leave it.
//
// A muster without an OAuth handler that can probe makes no exchange either,
// so there is nothing to wait for and it returns nil.
func (s *Service) probeTokenEndpoint(ctx context.Context, credentials *api.ClientCredentials) error {
	if !s.isTokenEndpointProbeDue() {
		return nil
	}
	prober, ok := api.GetOAuthHandler().(api.TokenEndpointProber)
	if !ok {
		s.setTokenEndpointProbeDue(false)
		return nil
	}

	config := *s.definition.Auth.TokenExchange
	if credentials != nil {
		config.ClientID, config.ClientSecret = credentials.ClientID, credentials.ClientSecret
	}
	// Bounded like the initialize the start just made: a token endpoint that
	// hangs must not hold the start past the server's own timeout.
	probeCtx, cancel := s.getRemoteInitContext(ctx)
	defer cancel()
	probeErr := prober.ProbeTokenEndpoint(probeCtx, &config)

	class := api.ClassifyTokenExchangeError(probeErr)
	switch class {
	case api.TokenExchangeFailureNone:
		s.setTokenEndpointProbeDue(false)
		s.LogInfo("Token endpoint %s answers again", config.DexTokenEndpoint)
		return nil
	case api.TokenExchangeFailureEndpoint:
		s.setFailureReason(class)
		return &api.TokenEndpointUnavailableError{Endpoint: config.DexTokenEndpoint, Err: probeErr}
	default:
		s.setTokenEndpointProbeDue(false)
		// The endpoint answered: the outage's schedule ends here, or the
		// orchestrator would restart the server once more and read Awaiting
		// Session while every exchange still fails.
		s.clearRetrySchedule()
		s.sessionAuthMutex.Lock()
		s.sessionAuth.exchangeFailure = probeErr
		s.sessionAuth.failureReason = class
		s.sessionAuthMutex.Unlock()
		return fmt.Errorf("token exchange fails for every caller (%s): %w", class, probeErr)
	}
}

// enterAwaitingSession runs the auth-required hook -- the aggregator's
// pending-auth registration, which is the only registry entry a per-session
// server may have -- and then publishes the StateAwaitingSession transition,
// in that order, so the entry exists before any subscriber sees the event.
func (s *Service) enterAwaitingSession(authErr *mcpserver.AuthRequiredError) {
	// The endpoint answered: the reconnect schedule from an outage before it
	// must not linger in the status next to "Awaiting Session".
	s.resetFailureTracking()
	if s.onAuthRequired != nil {
		s.onAuthRequired(s.definition, authErr)
	}
	s.UpdateState(services.StateAwaitingSession, services.HealthUnknown, nil)
	s.LogInfo("MCP server is served per session with the caller's identity; no session is connected")
	s.generateEvent(events.ReasonMCPServerAwaitingSession, events.EventData{})
}

// loadTokenExchangeCredentials loads the client credentials a token exchange
// is configured with, as every exchange for a caller will, and returns a
// TokenExchangeCredentialsError when it cannot. A server without a
// credentials Secret reference has nothing to load and gets nil credentials.
func (s *Service) loadTokenExchangeCredentials(ctx context.Context) (*api.ClientCredentials, error) {
	auth := s.definition.Auth
	if auth == nil || auth.TokenExchange == nil || !auth.TokenExchange.Enabled || auth.TokenExchange.ClientCredentialsSecretRef == nil {
		return nil, nil
	}
	ref := auth.TokenExchange.ClientCredentialsSecretRef
	namespace := s.credentialsNamespace()

	handler := api.GetSecretCredentialsHandler()
	if handler == nil {
		return nil, api.NewTokenExchangeCredentialsError(ref, namespace, fmt.Errorf("secret credentials handler not registered"))
	}
	credentials, err := handler.LoadClientCredentials(ctx, ref, namespace)
	if err != nil {
		return nil, api.NewTokenExchangeCredentialsError(ref, namespace, err)
	}
	return credentials, nil
}

// credentialsNamespace is the namespace a Secret reference without one
// resolves to: the server's own, "default" when the definition carries none
// (filesystem mode).
func (s *Service) credentialsNamespace() string {
	if s.definition.Namespace != "" {
		return s.definition.Namespace
	}
	return "default"
}

// RecordTokenExchangeOutcome implements api.TokenExchangeOutcomeRecorder.
//
// A failure that is the server's -- the credentials, the token endpoint, the
// connector -- fails every caller the same way and is reported the same way,
// as the server's state: Failed, with the exchange error as the last error
// and its class as the failure reason.
//
// A token endpoint that did not answer is put on the reconnect backoff with a
// token endpoint probe due: each retry probes the endpoint (probeTokenEndpoint)
// and the server returns to Awaiting Session once it answers, with no caller
// involved. An OAuth error from the endpoint (the credentials, the connector)
// is not retried: a retry would read Awaiting Session again while every
// exchange still fails. The server leaves Failed for it on the next successful
// exchange (recorded here), on a restart (Start clears the failure) or on a
// spec change. A failure specific to the caller's token is the session's and
// changes nothing here.
func (s *Service) RecordTokenExchangeOutcome(err error) {
	if err == nil {
		s.recordTokenExchangeSuccess()
		return
	}

	class := api.ClassifyTokenExchangeError(err)
	if class == api.TokenExchangeFailureNone {
		s.LogDebug("Token exchange failed for one caller; the server's state is unchanged: %v", err)
		return
	}

	s.sessionAuthMutex.Lock()
	s.sessionAuth.exchangeFailure = err
	s.sessionAuth.failureReason = class
	s.sessionAuth.endpointProbeDue = class == api.TokenExchangeFailureEndpoint
	s.sessionAuthMutex.Unlock()

	detail := string(class)
	if class == api.TokenExchangeFailureEndpoint {
		detail += ", " + s.scheduleTokenEndpointProbe(err)
	}

	s.LogWarn("Token exchange for MCP server %s fails for every caller (%s): %v", s.GetName(), detail, err)
	s.UpdateState(services.StateFailed, services.HealthUnhealthy, err)
	s.generateEvent(events.ReasonMCPServerFailed, events.EventData{
		Error: fmt.Sprintf("token exchange fails for every caller (%s): %s", detail, err.Error()),
	})
}

// scheduleTokenEndpointProbe puts a server whose token endpoint did not answer
// a caller's exchange on the reconnect backoff, as a failed start would, and
// describes the schedule. The orchestrator's retry restarts the server, and
// the start probes the token endpoint.
func (s *Service) scheduleTokenEndpointProbe(err error) string {
	s.failureMutex.Lock()
	defer s.failureMutex.Unlock()
	s.consecutiveFailures++
	s.lastFailureHTTPStatus = httpStatusFromError(err)
	s.calculateNextRetryTimeLocked(0)
	return s.retryScheduleLocked(s.tokenEndpointOutcomeLocked())
}

// recordTokenExchangeSuccess notes the time and, when an exchange failure had
// put the server in Failed, returns it to Awaiting Session: the exchange works
// again, and whether a session then holds a live connection is synced
// separately by the aggregator (notifyMCPServerConnected).
func (s *Service) recordTokenExchangeSuccess() {
	now := clock.Now()
	s.sessionAuthMutex.Lock()
	s.sessionAuth.lastExchangeSucceededAt = &now
	hadFailure := s.sessionAuth.exchangeFailure != nil
	s.sessionAuth.endpointProbeDue = false
	s.sessionAuth.exchangeFailure = nil
	s.sessionAuth.failureReason = api.TokenExchangeFailureNone
	s.sessionAuthMutex.Unlock()

	if hadFailure && s.GetState() == services.StateFailed {
		s.resetFailureTracking()
		s.LogInfo("Token exchange for MCP server %s succeeded again; the server is served per session", s.GetName())
		s.UpdateState(services.StateAwaitingSession, services.HealthUnknown, nil)
		s.generateEvent(events.ReasonMCPServerAwaitingSession, events.EventData{})
	}
}

// setFailureReason records why a start failed when the reason is not the
// endpoint itself, for the CR's Ready condition.
func (s *Service) setFailureReason(class api.TokenExchangeFailureClass) {
	s.sessionAuthMutex.Lock()
	s.sessionAuth.failureReason = class
	s.sessionAuthMutex.Unlock()
}

// tokenEndpointOutcomeLocked names the HTTP outcome of a failed token
// endpoint probe or exchange, as httpOutcomeLocked does for the MCP endpoint.
// MUST be called with failureMutex held.
func (s *Service) tokenEndpointOutcomeLocked() string {
	if s.lastFailureHTTPStatus != 0 {
		return "token endpoint answered HTTP " + strconv.Itoa(s.lastFailureHTTPStatus)
	}
	return "no HTTP response from the token endpoint"
}

// clearRetrySchedule ends the reconnect schedule without the reset of an
// attempt that reached a usable server: the failure count and the last HTTP
// status stay for diagnostics.
func (s *Service) clearRetrySchedule() {
	s.failureMutex.Lock()
	s.nextRetryAfter = nil
	s.retryBackoff = 0
	s.failureMutex.Unlock()
}

// isTokenEndpointProbeDue reports whether a start probes the token endpoint,
// see sessionAuthState.endpointProbeDue.
func (s *Service) isTokenEndpointProbeDue() bool {
	s.sessionAuthMutex.Lock()
	defer s.sessionAuthMutex.Unlock()
	return s.sessionAuth.endpointProbeDue
}

func (s *Service) setTokenEndpointProbeDue(due bool) {
	s.sessionAuthMutex.Lock()
	s.sessionAuth.endpointProbeDue = due
	s.sessionAuthMutex.Unlock()
}

// clearSessionAuthFailure forgets a failure reason and the exchange failure
// behind it. Called by every start (an operator's restart is an explicit
// reset; a retry re-verifies what it can) and by an attempt that reaches the
// endpoint.
func (s *Service) clearSessionAuthFailure() {
	s.sessionAuthMutex.Lock()
	s.sessionAuth.failureReason = api.TokenExchangeFailureNone
	s.sessionAuth.exchangeFailure = nil
	s.sessionAuthMutex.Unlock()
}

// sessionAuthServiceData adds the per-session facts to the service data the
// reconciler and core_service_status read.
func (s *Service) sessionAuthServiceData(data map[string]interface{}) {
	s.sessionAuthMutex.Lock()
	defer s.sessionAuthMutex.Unlock()
	if s.sessionAuth.failureReason != api.TokenExchangeFailureNone {
		data[api.ServiceDataFailureReason] = string(s.sessionAuth.failureReason)
	}
	if s.sessionAuth.lastExchangeSucceededAt != nil {
		data[api.ServiceDataLastTokenExchangeSucceededAt] = *s.sessionAuth.lastExchangeSucceededAt
	}
}
