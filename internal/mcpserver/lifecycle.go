package mcpserver

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
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
// orchestrator's imperative path: the writesAsCaller flag is off, or the name
// is not an MCPServer definition (e.g. muster's own aggregator service).

// lifecycleTarget resolves the MCPServer definition a lifecycle action targets.
// handled=false means the caller must fall back to the imperative path. A
// non-nil result is a ready-to-return tool error from the definition lookup.
func (a *Adapter) lifecycleTarget(ctx context.Context, name string) (*musterv1alpha1.MCPServer, *api.CallToolResult, bool) {
	if !a.writesAsCaller {
		return nil, nil, false
	}
	// The read stays on the SA client — only the spec mutation itself switches
	// to the caller's identity, same as create/update/delete.
	existing, err := a.client.GetMCPServer(ctx, name, a.namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, nil, false
		}
		result, _ := simpleError(fmt.Sprintf("Failed to look up MCP server '%s': %v", name, err))
		return nil, result, true
	}
	return existing, nil, true
}

// lifecycleWriteError maps a failed caller-identity lifecycle write to a tool
// error: 403 and 401 map exactly like create/update/delete, everything else
// is reported verbatim.
func (a *Adapter) lifecycleWriteError(err error, action, name string) (*api.CallToolResult, error) {
	if msg := a.describeWriteAuthError(err, action, name); msg != "" {
		return simpleError(msg)
	}
	return simpleError(fmt.Sprintf("Failed to request %s of service '%s': %v", action, name, err))
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
// spec.suspended on a suspended server, and writes spec.restartRequestedAt for
// a server that is down without being suspended (autoStart=false servers,
// failed servers) — for a down service that request is a plain start.
func (a *Adapter) StartMCPServerAsCaller(ctx context.Context, name string) (*api.CallToolResult, bool, error) {
	existing, errResult, handled := a.lifecycleTarget(ctx, name)
	if !handled {
		return nil, false, nil
	}
	if errResult != nil {
		return errResult, true, nil
	}

	// No mutation is intended for a server that is already up or on its way up,
	// so no write identity is required either.
	if state, exists := registryState(name); exists && !api.IsDownState(state) && !existing.Spec.Suspended {
		if state == api.StateAuthRequired {
			result, err := simpleError(fmt.Sprintf(
				"Service '%s' requires OAuth authentication.\n\n"+
					"To connect to this server, use the core_auth_login tool:\n"+
					"  core_auth_login(server=\"%s\")\n\n"+
					"The service start/restart command cannot be used for OAuth-protected servers "+
					"because authentication is session-scoped.",
				name, name))
			return result, true, err
		}
		if api.IsActiveState(state) {
			result, err := simpleOK(fmt.Sprintf("Service '%s' is already running", name))
			return result, true, err
		}
		result, err := simpleOK(fmt.Sprintf("Service '%s' is already starting", name))
		return result, true, err
	}

	writer, gateResult := a.mutationWriter(ctx)
	if gateResult != nil {
		return gateResult, true, nil
	}

	if existing.Spec.Suspended {
		existing.Spec.Suspended = false
	} else {
		now := metav1.Now()
		existing.Spec.RestartRequestedAt = &now
	}
	if err := writer.UpdateMCPServer(ctx, existing); err != nil {
		result, retErr := a.lifecycleWriteError(err, "start", name)
		return result, true, retErr
	}
	result, err := simpleOK(fmt.Sprintf("Start of service '%s' requested; the reconciler will start it", name))
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
		result, err := simpleOK(fmt.Sprintf("Service '%s' is already suspended", name))
		return result, true, err
	}

	writer, gateResult := a.mutationWriter(ctx)
	if gateResult != nil {
		return gateResult, true, nil
	}

	existing.Spec.Suspended = true
	if err := writer.UpdateMCPServer(ctx, existing); err != nil {
		result, retErr := a.lifecycleWriteError(err, "stop", name)
		return result, true, retErr
	}
	result, err := simpleOK(fmt.Sprintf("Stop of service '%s' requested (spec.suspended=true); the reconciler will stop it and keep it stopped", name))
	return result, true, err
}

// RestartMCPServerAsCaller handles core_service_restart as a CR write: it sets
// spec.restartRequestedAt with the caller's identity. The field itself is the
// audit record of who requested the restart and when; the reconciler processes
// it once and mirrors the value into status.lastRestartedAt.
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

	writer, gateResult := a.mutationWriter(ctx)
	if gateResult != nil {
		return gateResult, true, nil
	}

	now := metav1.Now()
	existing.Spec.RestartRequestedAt = &now
	if err := writer.UpdateMCPServer(ctx, existing); err != nil {
		result, retErr := a.lifecycleWriteError(err, "restart", name)
		return result, true, retErr
	}
	result, err := simpleOK(fmt.Sprintf("Restart of service '%s' requested; the reconciler will restart it", name))
	return result, true, err
}
