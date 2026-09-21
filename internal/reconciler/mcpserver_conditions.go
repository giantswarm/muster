package reconciler

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

// ConditionReady is the one condition the reconciler writes on an MCPServer:
// True while the server is reachable, False otherwise, with a reason that
// names the state in one word and a message that explains it. The state
// alone cannot: "Awaiting Session" says nothing about how a caller reaches
// the server, and "Failed" nothing about what failed.
const ConditionReady = "Ready"

// Reasons of the Ready condition that are not simply the state's name.
const (
	// ReasonSuspended: the server is Disconnected or Stopped because
	// spec.suspended keeps it so.
	ReasonSuspended = "Suspended"
)

// applyReadyCondition writes the Ready condition for the state applyStatus has
// just derived. meta.SetStatusCondition keeps lastTransitionTime where the
// status (True/False) is unchanged, so a message that merely gains the time of
// the latest exchange does not read as a transition.
func applyReadyCondition(server *musterv1alpha1.MCPServer, service api.ServiceInfo, exists bool) {
	var data map[string]interface{}
	if exists {
		data = service.GetServiceData()
	}
	condition := readyCondition(server, data)
	condition.ObservedGeneration = server.Generation
	meta.SetStatusCondition(&server.Status.Conditions, condition)
}

// readyCondition derives the Ready condition from the state written to the
// status and the service's own facts (its auth configuration, the class of a
// failure, the time of the last successful token exchange).
func readyCondition(server *musterv1alpha1.MCPServer, data map[string]interface{}) metav1.Condition {
	auth, _ := data["auth"].(*api.MCPServerAuth)
	condition := metav1.Condition{Type: ConditionReady}

	switch server.Status.State {
	case musterv1alpha1.MCPServerStateRunning:
		condition.Status, condition.Reason = metav1.ConditionTrue, "Running"
		condition.Message = "The process is running."

	case musterv1alpha1.MCPServerStateConnected:
		condition.Status, condition.Reason = metav1.ConditionTrue, "Connected"
		if auth.UsesSessionAuth() {
			condition.Message = "A session holds a live connection made with its own identity; muster holds no connection of its own."
		} else {
			condition.Message = "Connected."
		}

	case musterv1alpha1.MCPServerStateAwaitingSession:
		condition.Status, condition.Reason = metav1.ConditionTrue, "AwaitingSession"
		condition.Message = awaitingSessionMessage(auth, data)

	case musterv1alpha1.MCPServerStateAuthRequired:
		condition.Status, condition.Reason = metav1.ConditionTrue, "AuthRequired"
		condition.Message = "Reachable; it connects once a person has signed in to it through muster (core_auth_login)."

	case musterv1alpha1.MCPServerStateConnecting:
		condition.Status, condition.Reason = metav1.ConditionFalse, "Connecting"
		condition.Message = "A connection attempt is in progress."

	case musterv1alpha1.MCPServerStateStarting:
		condition.Status, condition.Reason = metav1.ConditionFalse, "Starting"
		condition.Message = "The process is starting."

	case musterv1alpha1.MCPServerStateDisconnected, musterv1alpha1.MCPServerStateStopped:
		condition.Status = metav1.ConditionFalse
		switch {
		case server.Spec.Suspended:
			condition.Reason = ReasonSuspended
			condition.Message = "Deactivated (spec.suspended): muster keeps it disconnected until it is activated again."
		case server.Status.State == musterv1alpha1.MCPServerStateStopped:
			condition.Reason, condition.Message = "Stopped", "The process is not running."
		default:
			condition.Reason, condition.Message = "Disconnected", "Not connected."
		}

	case musterv1alpha1.MCPServerStateFailed:
		condition.Status, condition.Reason = metav1.ConditionFalse, "Failed"
		if class, ok := data[api.ServiceDataFailureReason].(string); ok && class != "" {
			condition.Reason = class
		}
		condition.Message = server.Status.LastError
		if condition.Message == "" {
			condition.Message = "The server did not come up."
		}

	default:
		condition.Status, condition.Reason = metav1.ConditionUnknown, "Unknown"
		condition.Message = "The server's state is not known yet."
	}

	return condition
}

// awaitingSessionMessage explains how a server served per session is reached
// and, for token exchange, when the exchange last worked -- the facts an
// operator needs to tell a working per-session server from one nobody has
// used yet.
func awaitingSessionMessage(auth *api.MCPServerAuth, data map[string]interface{}) string {
	exchange := auth != nil && auth.TokenExchange != nil && auth.TokenExchange.Enabled
	mechanism := "each caller's own token is forwarded to the server"
	if exchange {
		mechanism = fmt.Sprintf("each caller's token is exchanged (RFC 8693) at %s through connector %q",
			auth.TokenExchange.DexTokenEndpoint, auth.TokenExchange.ConnectorID)
	}
	message := "Served per session: " + mechanism + "; muster holds no connection of its own. No session is connected"
	if !exchange {
		return message + "."
	}
	if last, ok := data[api.ServiceDataLastTokenExchangeSucceededAt].(time.Time); ok {
		return message + fmt.Sprintf("; the last exchange succeeded at %s.", last.UTC().Format(time.RFC3339))
	}
	return message + "; no exchange has been made since muster started."
}
