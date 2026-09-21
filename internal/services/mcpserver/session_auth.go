package mcpserver

import (
	"context"
	"fmt"
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
func (s *Service) settleSessionAuth(ctx context.Context, authErr *mcpserver.AuthRequiredError) error {
	if err := s.verifyTokenExchangeCredentials(ctx); err != nil {
		s.setFailureReason(api.TokenExchangeFailureCredentials)
		return s.failStart(err)
	}
	s.enterAwaitingSession(authErr)
	return authErr
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

// verifyTokenExchangeCredentials loads the client credentials a token exchange
// is configured with, as every exchange for a caller will, and returns a
// TokenExchangeCredentialsError when it cannot. A server without a
// credentials Secret reference has nothing to verify.
func (s *Service) verifyTokenExchangeCredentials(ctx context.Context) error {
	auth := s.definition.Auth
	if auth == nil || auth.TokenExchange == nil || !auth.TokenExchange.Enabled || auth.TokenExchange.ClientCredentialsSecretRef == nil {
		return nil
	}
	ref := auth.TokenExchange.ClientCredentialsSecretRef
	namespace := s.credentialsNamespace()

	handler := api.GetSecretCredentialsHandler()
	if handler == nil {
		return api.NewTokenExchangeCredentialsError(ref, namespace, fmt.Errorf("secret credentials handler not registered"))
	}
	if _, err := handler.LoadClientCredentials(ctx, ref, namespace); err != nil {
		return api.NewTokenExchangeCredentialsError(ref, namespace, err)
	}
	return nil
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
// and its class as the failure reason. No reconnect is scheduled for it: a
// retry would re-probe the endpoint, which is fine, and read Awaiting Session
// again while the exchange is still broken. The server leaves Failed on the
// next successful exchange (recorded here), on a restart (Start clears the
// failure) or on a spec change. A failure specific to the caller's token is
// the session's and changes nothing here.
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
	s.sessionAuthMutex.Unlock()

	s.LogWarn("Token exchange for MCP server %s fails for every caller (%s): %v", s.GetName(), class, err)
	s.UpdateState(services.StateFailed, services.HealthUnhealthy, err)
	s.generateEvent(events.ReasonMCPServerFailed, events.EventData{
		Error: fmt.Sprintf("token exchange fails for every caller (%s): %s", class, err.Error()),
	})
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
	s.sessionAuth.exchangeFailure = nil
	s.sessionAuth.failureReason = api.TokenExchangeFailureNone
	s.sessionAuthMutex.Unlock()

	if hadFailure && s.GetState() == services.StateFailed {
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
