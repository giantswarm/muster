package reconciler

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/giantswarm/muster/v5/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

func exchangeAuth() *api.MCPServerAuth {
	return &api.MCPServerAuth{
		Type: "oauth",
		TokenExchange: &api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex.remote.example.test/token",
			ConnectorID:      "remote-oidc",
		},
	}
}

// TestReadyConditionExplainsEachState: the state is one word; the condition
// says what it means, and for a server served per session how a caller
// reaches it and whether the exchange has ever worked.
func TestReadyConditionExplainsEachState(t *testing.T) {
	lastExchange := time.Date(2026, 9, 21, 12, 41, 3, 0, time.UTC)

	for name, tc := range map[string]struct {
		state     musterv1alpha1.MCPServerStateValue
		suspended bool
		lastError string
		data      map[string]interface{}
		status    metav1.ConditionStatus
		reason    string
		message   []string
	}{
		"running stdio": {
			state: musterv1alpha1.MCPServerStateRunning, status: metav1.ConditionTrue, reason: "Running",
			message: []string{"process is running"},
		},
		"connected with muster's own connection": {
			state: musterv1alpha1.MCPServerStateConnected, status: metav1.ConditionTrue, reason: "Connected",
			message: []string{"Connected."},
		},
		"connected through a session": {
			state: musterv1alpha1.MCPServerStateConnected, status: metav1.ConditionTrue, reason: "Connected",
			data:    map[string]interface{}{"auth": exchangeAuth()},
			message: []string{"A session holds a live connection", "no connection of its own"},
		},
		"awaiting session, token forwarding": {
			state: musterv1alpha1.MCPServerStateAwaitingSession, status: metav1.ConditionTrue, reason: "AwaitingSession",
			data:    map[string]interface{}{"auth": &api.MCPServerAuth{Type: "oauth", ForwardToken: true}},
			message: []string{"Served per session", "own token is forwarded", "no connection of its own", "No session is connected."},
		},
		"awaiting session, exchange never made": {
			state: musterv1alpha1.MCPServerStateAwaitingSession, status: metav1.ConditionTrue, reason: "AwaitingSession",
			data: map[string]interface{}{"auth": exchangeAuth()},
			message: []string{"exchanged (RFC 8693) at https://dex.remote.example.test/token through connector \"remote-oidc\"",
				"no exchange has been made since muster started"},
		},
		"awaiting session, exchange worked": {
			state: musterv1alpha1.MCPServerStateAwaitingSession, status: metav1.ConditionTrue, reason: "AwaitingSession",
			data:    map[string]interface{}{"auth": exchangeAuth(), api.ServiceDataLastTokenExchangeSucceededAt: lastExchange},
			message: []string{"the last exchange succeeded at 2026-09-21T12:41:03Z"},
		},
		"auth required": {
			state: musterv1alpha1.MCPServerStateAuthRequired, status: metav1.ConditionTrue, reason: "AuthRequired",
			message: []string{"signed in", "core_auth_login"},
		},
		"connecting": {
			state: musterv1alpha1.MCPServerStateConnecting, status: metav1.ConditionFalse, reason: "Connecting",
			message: []string{"in progress"},
		},
		"starting": {
			state: musterv1alpha1.MCPServerStateStarting, status: metav1.ConditionFalse, reason: "Starting",
		},
		"disconnected": {
			state: musterv1alpha1.MCPServerStateDisconnected, status: metav1.ConditionFalse, reason: "Disconnected",
			message: []string{"Not connected."},
		},
		"stopped": {
			state: musterv1alpha1.MCPServerStateStopped, status: metav1.ConditionFalse, reason: "Stopped",
		},
		"suspended": {
			state: musterv1alpha1.MCPServerStateDisconnected, suspended: true, status: metav1.ConditionFalse, reason: ReasonSuspended,
			message: []string{"Deactivated (spec.suspended)"},
		},
		"failed at the endpoint": {
			state: musterv1alpha1.MCPServerStateFailed, lastError: "request failed with status 504", status: metav1.ConditionFalse, reason: "Failed",
			message: []string{"request failed with status 504"},
		},
		"failed without an error text": {
			state: musterv1alpha1.MCPServerStateFailed, status: metav1.ConditionFalse, reason: "Failed",
			message: []string{"did not come up"},
		},
		"failed by the exchange credentials": {
			state:     musterv1alpha1.MCPServerStateFailed,
			lastError: `token exchange credentials from Secret agent-platform/remote-token-exchange-credentials: secrets "remote-token-exchange-credentials" not found`,
			data:      map[string]interface{}{"auth": exchangeAuth(), api.ServiceDataFailureReason: string(api.TokenExchangeFailureCredentials)},
			status:    metav1.ConditionFalse, reason: "TokenExchangeCredentials",
			message: []string{"remote-token-exchange-credentials"},
		},
		"failed by the token endpoint": {
			state: musterv1alpha1.MCPServerStateFailed, lastError: "token exchange failed: Post https://dex.remote.example.test/token: connection refused",
			data:   map[string]interface{}{api.ServiceDataFailureReason: string(api.TokenExchangeFailureEndpoint)},
			status: metav1.ConditionFalse, reason: "TokenExchangeEndpoint",
		},
		"no state yet": {
			state: "", status: metav1.ConditionUnknown, reason: "Unknown",
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := &musterv1alpha1.MCPServer{
				Spec:   musterv1alpha1.MCPServerSpec{Suspended: tc.suspended},
				Status: musterv1alpha1.MCPServerStatus{State: tc.state, LastError: tc.lastError},
			}
			got := readyCondition(server, tc.data)

			assert.Equal(t, ConditionReady, got.Type)
			assert.Equal(t, tc.status, got.Status)
			assert.Equal(t, tc.reason, got.Reason)
			assert.NotEmpty(t, got.Message, "every condition explains itself")
			for _, want := range tc.message {
				assert.Contains(t, got.Message, want)
			}
		})
	}
}

// TestApplyStatusFromServiceWritesReadyCondition: the status sync writes the
// condition next to the state, from the service's own facts, and keeps the
// transition time while only the message changes.
func TestApplyStatusFromServiceWritesReadyCondition(t *testing.T) {
	registry := NewMockServiceRegistry()
	r := &MCPServerReconciler{serviceRegistry: registry}
	server := &musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Generation: 3},
		Spec:       musterv1alpha1.MCPServerSpec{Type: "streamable-http", Auth: &musterv1alpha1.MCPServerAuth{Type: "oauth"}},
	}

	// The server waits for a caller; nobody has used it yet.
	registry.AddService("remote-exchange", &MockServiceInfo{
		Name: "remote-exchange", ServiceType: api.TypeMCPServer, State: api.StateAwaitingSession,
		ServiceData: map[string]interface{}{"auth": exchangeAuth()},
	})
	r.applyStatusFromService(server, "remote-exchange", nil, nil)

	assert.Equal(t, musterv1alpha1.MCPServerStateAwaitingSession, server.Status.State)
	assert.Empty(t, server.Status.LastError)
	assert.Nil(t, server.Status.LastConnected, "no session has connected; nothing was connected")
	ready := meta.FindStatusCondition(server.Status.Conditions, ConditionReady)
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionTrue, ready.Status)
	assert.Equal(t, "AwaitingSession", ready.Reason)
	assert.Contains(t, ready.Message, "no exchange has been made since muster started")
	assert.Equal(t, int64(3), ready.ObservedGeneration)
	firstTransition := ready.LastTransitionTime

	// A caller's exchange succeeded and the session is gone again: the same
	// status with a richer message is not a transition.
	lastExchange := time.Now().Add(-time.Minute)
	registry.AddService("remote-exchange", &MockServiceInfo{
		Name: "remote-exchange", ServiceType: api.TypeMCPServer, State: api.StateAwaitingSession,
		ServiceData: map[string]interface{}{"auth": exchangeAuth(), api.ServiceDataLastTokenExchangeSucceededAt: lastExchange},
	})
	r.applyStatusFromService(server, "remote-exchange", nil, nil)
	ready = meta.FindStatusCondition(server.Status.Conditions, ConditionReady)
	require.NotNil(t, ready)
	assert.Contains(t, ready.Message, "the last exchange succeeded at "+lastExchange.UTC().Format(time.RFC3339))
	assert.Equal(t, firstTransition, ready.LastTransitionTime)

	// The credentials Secret is gone: Failed, and the reason names the class.
	registry.AddService("remote-exchange", &MockServiceInfo{
		Name: "remote-exchange", ServiceType: api.TypeMCPServer, State: api.StateFailed,
		LastError: errors.New(`token exchange credentials from Secret agent-platform/remote-token-exchange-credentials: secrets "remote-token-exchange-credentials" not found`),
		ServiceData: map[string]interface{}{
			"auth":                       exchangeAuth(),
			api.ServiceDataFailureReason: string(api.TokenExchangeFailureCredentials),
		},
	})
	r.applyStatusFromService(server, "remote-exchange", nil, nil)

	assert.Equal(t, musterv1alpha1.MCPServerStateFailed, server.Status.State)
	ready = meta.FindStatusCondition(server.Status.Conditions, ConditionReady)
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionFalse, ready.Status)
	assert.Equal(t, "TokenExchangeCredentials", ready.Reason)
	assert.Contains(t, ready.Message, "remote-token-exchange-credentials")
	assert.Len(t, server.Status.Conditions, 1, "one Ready condition, replaced in place")

	// No service at all: the definition was never started.
	fresh := &musterv1alpha1.MCPServer{Spec: musterv1alpha1.MCPServerSpec{Type: "streamable-http"}}
	r.applyStatusFromService(fresh, "never-started", nil, nil)
	ready = meta.FindStatusCondition(fresh.Status.Conditions, ConditionReady)
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionFalse, ready.Status)
	assert.Equal(t, "Disconnected", ready.Reason)
}

// TestDetermineStateAwaitingSession: the service state reads as the CRD
// state for a remote server; a stdio server cannot be served per session and
// keeps its process reading.
func TestDetermineStateAwaitingSession(t *testing.T) {
	r := &MCPServerReconciler{}
	assert.Equal(t, musterv1alpha1.MCPServerStateAwaitingSession, r.determineState(api.StateAwaitingSession, "streamable-http"))
	assert.Equal(t, musterv1alpha1.MCPServerStateAwaitingSession, r.determineState(api.StateAwaitingSession, "sse"))
	assert.Equal(t, musterv1alpha1.MCPServerStateRunning, r.determineState(api.StateAwaitingSession, "stdio"))
}
