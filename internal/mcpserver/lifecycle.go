package mcpserver

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/events"
)

// Lifecycle actions as CR writes (issue #1057). With writes-as-caller enabled,
// core_service_start/_stop/_restart no longer mutate orchestrator runtime
// state directly: they write the MCPServer spec lifecycle fields (issue #1055)
// with the caller's own bearer, so the apiserver authenticates the real user,
// k8s RBAC authorizes the action, and the audit log attributes it to the dex
// subject — exactly like create/update/delete. The reconciler performs the
// actual start/stop/restart from the spec fields.
//
// Each method returns handled=false when the action must fall back to the
// orchestrator's imperative path: muster runs in filesystem mode (no
// caller-identity writes), or the name is one of muster's own internal
// services (e.g. the aggregator). MCPServer
// and unknown names never fall back — a deleted CR with a lingering registry
// entry would otherwise reopen the privileged side door this feature closes.

// The compile-time pin: if these method signatures drift from the api
// contract, the orchestrator's type assertion would silently fail and
// lifecycle would fall back to the privileged imperative path. Fail the build
// instead.
var _ api.MCPServerLifecycleAsCaller = (*Adapter)(nil)

// OAuthLoginGuidance is the user-facing guidance for services that require
// session-scoped OAuth authentication: starting or restarting them cannot
// authenticate a session, only core_auth_login can. Shared with the
// imperative path's formatOAuthAuthenticationError so both paths answer the
// same tool call with the same words.
func OAuthLoginGuidance(name string) string {
	return fmt.Sprintf(
		"Service '%s' requires OAuth authentication.\n\n"+
			"To connect to this server, use the core_auth_login tool:\n"+
			"  core_auth_login(server=\"%s\")\n\n"+
			"The service start/restart command cannot be used for OAuth-protected servers "+
			"because authentication is session-scoped.",
		name, name,
	)
}

// lifecycleTarget resolves the MCPServer definition a lifecycle action targets.
// handled=false means the caller must fall back to the imperative path. A
// non-nil result is a ready-to-return tool error from the definition lookup.
func (a *Adapter) lifecycleTarget(ctx context.Context, name string) (*musterv1alpha1.MCPServer, *api.CallToolResult, bool) {
	if !a.writesAsCaller {
		return nil, nil, false
	}

	// Muster's own internal services (e.g. the aggregator) are not
	// user-mutable CR surface: they keep the imperative path and must not
	// depend on a Kubernetes read.
	if registry := api.GetServiceRegistry(); registry != nil {
		if service, exists := registry.Get(name); exists && service.GetType() != api.TypeMCPServer {
			return nil, nil, false
		}
	}

	// The read stays on the SA client — only the spec mutation itself switches
	// to the caller's identity, same as create/update/delete.
	existing, err := a.client.GetMCPServer(ctx, name, a.namespace)
	if err != nil {
		// Fail closed on every lookup error, NotFound included: MCPServer and
		// unknown names must not reach the imperative path with muster's
		// privilege, even during an apiserver hiccup.
		if errors.IsNotFound(err) {
			return nil, api.HandleErrorWithPrefix(api.NewMCPServerNotFoundError(name), "Failed to look up MCP server"), true
		}
		result, _ := simpleError(fmt.Sprintf("Failed to look up MCP server '%s': %v", name, err))
		return nil, result, true
	}
	return existing, nil, true
}

// commitLifecycleWrite is the shared tail of the three lifecycle tools: it
// resolves the caller's write identity, applies mutate to the object, retries
// the read-modify-write on conflicts (status syncs bump resourceVersion
// continuously), maps authn/authz failures exactly like create/update/delete,
// and emits the CRD events operators look for in `kubectl describe`.
func (a *Adapter) commitLifecycleWrite(ctx context.Context, existing *musterv1alpha1.MCPServer, action, okMsg string, mutate func(*musterv1alpha1.MCPServer)) (*api.CallToolResult, error) {
	writer, gateResult := a.mutationWriter(ctx)
	if gateResult != nil {
		return gateResult, nil
	}

	name := existing.Name
	mutate(existing)
	err := writer.UpdateMCPServer(ctx, existing)
	if errors.IsConflict(err) {
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			fresh, getErr := a.client.GetMCPServer(ctx, name, a.namespace)
			if getErr != nil {
				return getErr
			}
			mutate(fresh)
			return writer.UpdateMCPServer(ctx, fresh)
		})
	}
	if err != nil {
		if msg := a.describeWriteAuthError(err, action, name); msg != "" {
			return simpleError(msg)
		}
		a.generateCRDEvent(name, events.ReasonMCPServerFailed, events.EventData{
			Error:     err.Error(),
			Operation: action,
		})
		return simpleError(fmt.Sprintf("Failed to request %s of service '%s': %v", action, name, err))
	}

	a.generateCRDEvent(name, events.ReasonMCPServerUpdated, events.EventData{
		Operation: action,
	})
	return simpleOK(okMsg)
}

// requestedMessage is the success message for an accepted lifecycle request.
// OAuth-backed servers get a pointer at core_auth_login up front, because the
// reconciler treats Auth Required as a stable state and cannot report it back
// through this (asynchronous) tool result.
func requestedMessage(server *musterv1alpha1.MCPServer, base string) string {
	if server.Spec.Auth != nil && server.Spec.Auth.Type == "oauth" {
		return base + ". This server uses OAuth: if it comes up auth-required, authenticate with core_auth_login"
	}
	return base
}

// registryState returns the runtime state of the named service, or ok=false
// when no service is registered for it (autoStart=false and never started).
func registryState(name string) (api.ServiceState, bool) {
	registry := api.GetServiceRegistry()
	if registry == nil {
		return "", false
	}
	service, exists := registry.Get(name)
	if !exists {
		return "", false
	}
	return service.GetState(), true
}

// StartMCPServerAsCaller handles core_service_start as a CR write: it clears
// spec.suspended and, whenever the service is down, also writes
// spec.restartRequestedAt — the one-shot "make it run now" request. The
// restart request is what starts autoStart=false and failed servers, and it
// makes a resume level-reconstructable even when the reconciler's in-memory
// suspension marker was lost to a muster restart. Timestamps have second
// granularity, so repeated starts within one second collapse into one request
// — the service was just started anyway.
func (a *Adapter) StartMCPServerAsCaller(ctx context.Context, name string) (*api.CallToolResult, bool, error) {
	existing, errResult, handled := a.lifecycleTarget(ctx, name)
	if !handled {
		return nil, false, nil
	}
	if errResult != nil {
		return errResult, true, nil
	}

	state, exists := registryState(name)
	down := !exists || api.IsDownState(state)

	// No mutation is intended for an unsuspended server that is up or on its
	// way up, so no write identity is required either.
	if !down && !existing.Spec.Suspended {
		switch {
		case state == api.StateAuthRequired:
			result, err := simpleError(OAuthLoginGuidance(name))
			return result, true, err
		case api.IsActiveState(state):
			result, err := simpleOK(fmt.Sprintf("Service '%s' is already running", name))
			return result, true, err
		default:
			// Transitional states (starting, connecting, stopping, ...): acting
			// on them would interrupt an in-flight transition, so report the
			// actual state instead of pretending.
			result, err := simpleOK(fmt.Sprintf(
				"Service '%s' is in state %q; no start request was written — check core_service_status and retry if it does not come up", name, state))
			return result, true, err
		}
	}

	var restartRequestedAt *metav1.Time
	if down {
		now := metav1.Now()
		restartRequestedAt = &now
	}
	result, err := a.commitLifecycleWrite(ctx, existing, "start",
		requestedMessage(existing, fmt.Sprintf("Start of service '%s' requested; the reconciler will start it", name)),
		func(server *musterv1alpha1.MCPServer) {
			server.Spec.Suspended = false
			if restartRequestedAt != nil {
				server.Spec.RestartRequestedAt = restartRequestedAt
			}
		})
	return result, true, err
}

// StopMCPServerAsCaller handles core_service_stop as a CR write: it sets
// spec.suspended=true with the caller's identity. The reconciler stops the
// service and keeps it stopped until it is resumed.
func (a *Adapter) StopMCPServerAsCaller(ctx context.Context, name string) (*api.CallToolResult, bool, error) {
	existing, errResult, handled := a.lifecycleTarget(ctx, name)
	if !handled {
		return nil, false, nil
	}
	if errResult != nil {
		return errResult, true, nil
	}

	if existing.Spec.Suspended {
		if state, exists := registryState(name); exists && !api.IsDownState(state) {
			result, err := simpleOK(fmt.Sprintf("Service '%s' is already suspended; its service is still %q while the reconciler brings it down", name, state))
			return result, true, err
		}
		result, err := simpleOK(fmt.Sprintf("Service '%s' is already suspended", name))
		return result, true, err
	}

	result, err := a.commitLifecycleWrite(ctx, existing, "stop",
		fmt.Sprintf("Stop of service '%s' requested (spec.suspended=true); the reconciler will stop it and keep it stopped", name),
		func(server *musterv1alpha1.MCPServer) {
			server.Spec.Suspended = true
		})
	return result, true, err
}

// RestartMCPServerAsCaller handles core_service_restart as a CR write: it sets
// spec.restartRequestedAt with the caller's identity. The field itself is the
// audit record of who requested the restart and when; the reconciler processes
// it once and mirrors the value into status.lastRestartedAt. Second-granularity
// timestamps collapse repeated restarts within one second into a single one.
func (a *Adapter) RestartMCPServerAsCaller(ctx context.Context, name string) (*api.CallToolResult, bool, error) {
	existing, errResult, handled := a.lifecycleTarget(ctx, name)
	if !handled {
		return nil, false, nil
	}
	if errResult != nil {
		return errResult, true, nil
	}

	// The reconciler consumes restart requests on suspended servers without
	// action (suspension wins), so refuse up front instead of pretending.
	if existing.Spec.Suspended {
		result, err := simpleError(fmt.Sprintf("Service '%s' is suspended; use core_service_start to resume it", name))
		return result, true, err
	}

	// Restarting cannot authenticate a session: keep the imperative path's
	// guidance for servers waiting on OAuth.
	if state, exists := registryState(name); exists && state == api.StateAuthRequired {
		result, err := simpleError(OAuthLoginGuidance(name))
		return result, true, err
	}

	now := metav1.Now()
	result, err := a.commitLifecycleWrite(ctx, existing, "restart",
		requestedMessage(existing, fmt.Sprintf("Restart of service '%s' requested; the reconciler will restart it", name)),
		func(server *musterv1alpha1.MCPServer) {
			server.Spec.RestartRequestedAt = &now
		})
	return result, true, err
}
