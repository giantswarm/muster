package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/callerwrite"
	"github.com/giantswarm/muster/internal/server"
)

// lifecycleCall invokes one of the three lifecycle-as-caller methods by name.
func lifecycleCall(t *testing.T, adapter *Adapter, ctx context.Context, action, name string) (*api.CallToolResult, bool, error) {
	t.Helper()
	switch action {
	case "start":
		return adapter.StartMCPServerAsCaller(ctx, name)
	case "stop":
		return adapter.StopMCPServerAsCaller(ctx, name)
	case "restart":
		return adapter.RestartMCPServerAsCaller(ctx, name)
	default:
		t.Fatalf("unknown action %q", action)
		return nil, false, nil
	}
}

func suspendedServer() *musterv1alpha1.MCPServer {
	obj := existingServer()
	obj.Spec.Suspended = true
	return obj
}

// TestLifecycleAsCaller_NotHandledWhenFlagOff pins the fallback contract: with
// writesAsCaller off the orchestrator's imperative path stays in charge.
func TestLifecycleAsCaller_NotHandledWhenFlagOff(t *testing.T) {
	adapter := NewAdapterWithClient(&stubMusterClient{existing: existingServer()}, "test-ns")
	for _, action := range []string{"start", "stop", "restart"} {
		if _, handled, _ := lifecycleCall(t, adapter, context.Background(), action, "test-server"); handled {
			t.Errorf("%s: must not be handled with the flag off", action)
		}
	}
}

// TestLifecycleAsCaller_NotHandledForInternalService pins that muster's own
// internal services (e.g. the aggregator) fall back to the imperative path —
// resolved from the registry, without any Kubernetes read.
func TestLifecycleAsCaller_NotHandledForInternalService(t *testing.T) {
	withServiceRegistry(t, &fakeServiceRegistry{services: map[string]api.ServiceInfo{
		"aggregator": &fakeServiceInfo{name: "aggregator", typ: api.ServiceType("Aggregator")},
	}})
	h := newCallerHarness(t, nil, nil)
	ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))
	for _, action := range []string{"start", "stop", "restart"} {
		if _, handled, _ := lifecycleCall(t, h.adapter, ctx, action, "aggregator"); handled {
			t.Errorf("%s: must not be handled for muster's own internal services", action)
		}
	}
	if len(h.tokensSeen) != 0 || len(h.updated) != 0 {
		t.Fatal("fallback must not touch the caller client")
	}
}

// TestLifecycleAsCaller_UnknownNameFailsClosed pins that names without an
// MCPServer definition and without an internal service do NOT fall back to the
// privileged imperative path: they get a not-found error. A deleted CR with a
// lingering registry entry would otherwise reopen the side door.
func TestLifecycleAsCaller_UnknownNameFailsClosed(t *testing.T) {
	h := newCallerHarness(t, nil, nil)
	ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))
	for _, action := range []string{"start", "stop", "restart"} {
		result, handled, err := lifecycleCall(t, h.adapter, ctx, action, "ghost")
		if err != nil || !handled {
			t.Fatalf("%s: handled=%v err=%v", action, handled, err)
		}
		if !result.IsError || !strings.Contains(resultText(t, result), "not found") {
			t.Errorf("%s: expected a not-found error, got: %s", action, resultText(t, result))
		}
	}
	if len(h.updated) != 0 {
		t.Fatal("unknown names must not write")
	}
}

// TestLifecycleAsCaller_StopWritesSuspended: core_service_stop becomes a
// spec.suspended=true write carried by the caller's own bearer.
func TestLifecycleAsCaller_StopWritesSuspended(t *testing.T) {
	token := testJWT(t, callerwrite.DefaultKubernetesAudience)
	h := newCallerHarness(t, existingServer(), nil)
	ctx := server.ContextWithIDToken(context.Background(), token)

	result, handled, err := h.adapter.StopMCPServerAsCaller(ctx, "test-server")
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", resultText(t, result))
	}
	if len(h.tokensSeen) != 1 || h.tokensSeen[0] != token {
		t.Fatalf("caller bearer did not reach the write: %v", h.tokensSeen)
	}
	if len(h.updatedObjs) != 1 || !h.updatedObjs[0].Spec.Suspended {
		t.Fatalf("expected one update writing spec.suspended=true, got %+v", h.updatedObjs)
	}
	if len(h.sa.updated) != 0 {
		t.Fatal("SA client performed the write on the caller path")
	}
}

// TestLifecycleAsCaller_StartClearsSuspended: start on a suspended server is
// the resume transition — spec.suspended back to false. When the service is
// down, the write also carries spec.restartRequestedAt so the resume survives
// a reconciler whose in-memory suspension marker was lost to a muster restart;
// when the service is still up (stop not yet processed), only the flag is
// cleared.
func TestLifecycleAsCaller_StartClearsSuspended(t *testing.T) {
	t.Run("service down", func(t *testing.T) {
		h := newCallerHarness(t, suspendedServer(), nil)
		ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))

		result, handled, err := h.adapter.StartMCPServerAsCaller(ctx, "test-server")
		if err != nil || !handled || result.IsError {
			t.Fatalf("handled=%v err=%v result=%s", handled, err, resultText(t, result))
		}
		if len(h.updatedObjs) != 1 {
			t.Fatalf("expected one caller-client update, got %d", len(h.updatedObjs))
		}
		if h.updatedObjs[0].Spec.Suspended {
			t.Fatal("start did not clear spec.suspended")
		}
		if h.updatedObjs[0].Spec.RestartRequestedAt == nil {
			t.Fatal("resume of a down service must also write spec.restartRequestedAt")
		}
	})

	t.Run("service still running", func(t *testing.T) {
		withServiceRegistry(t, &fakeServiceRegistry{services: map[string]api.ServiceInfo{
			"test-server": &fakeServiceInfo{name: "test-server", state: api.StateRunning},
		}})
		h := newCallerHarness(t, suspendedServer(), nil)
		ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))

		result, handled, err := h.adapter.StartMCPServerAsCaller(ctx, "test-server")
		if err != nil || !handled || result.IsError {
			t.Fatalf("handled=%v err=%v result=%s", handled, err, resultText(t, result))
		}
		if len(h.updatedObjs) != 1 || h.updatedObjs[0].Spec.Suspended {
			t.Fatalf("expected one update clearing spec.suspended, got %+v", h.updatedObjs)
		}
		if h.updatedObjs[0].Spec.RestartRequestedAt != nil {
			t.Fatal("canceling a not-yet-processed stop must not request a restart")
		}
	})
}

// TestLifecycleAsCaller_StartRequestsStartWhenDown: start on a server that is
// down without being suspended (autoStart=false, failed) writes
// spec.restartRequestedAt — the one-shot "make it run now" request.
func TestLifecycleAsCaller_StartRequestsStartWhenDown(t *testing.T) {
	for name, registry := range map[string]api.ServiceRegistryHandler{
		"no service registered": &fakeServiceRegistry{services: map[string]api.ServiceInfo{}},
		"service stopped": &fakeServiceRegistry{services: map[string]api.ServiceInfo{
			"test-server": &fakeServiceInfo{name: "test-server", state: api.StateStopped},
		}},
		"service failed": &fakeServiceRegistry{services: map[string]api.ServiceInfo{
			"test-server": &fakeServiceInfo{name: "test-server", state: api.StateFailed},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			withServiceRegistry(t, registry)
			h := newCallerHarness(t, existingServer(), nil)
			ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))

			result, handled, err := h.adapter.StartMCPServerAsCaller(ctx, "test-server")
			if err != nil || !handled || result.IsError {
				t.Fatalf("handled=%v err=%v result=%s", handled, err, resultText(t, result))
			}
			if len(h.updatedObjs) != 1 || h.updatedObjs[0].Spec.RestartRequestedAt == nil {
				t.Fatalf("expected one update writing spec.restartRequestedAt, got %+v", h.updatedObjs)
			}
			if h.updatedObjs[0].Spec.Suspended {
				t.Fatal("start must not suspend the server")
			}
		})
	}
}

// TestLifecycleAsCaller_StartNoOpStates: no mutation is intended for a server
// that is up or on its way up, so nothing is written and no identity needed.
func TestLifecycleAsCaller_StartNoOpStates(t *testing.T) {
	cases := []struct {
		state   api.ServiceState
		isError bool
		want    string
	}{
		{api.StateRunning, false, "already running"},
		{api.StateConnected, false, "already running"},
		{api.StateStarting, false, `state "starting"`},
		{api.StateStopping, false, `state "stopping"`},
		{api.StateAuthRequired, true, "core_auth_login"},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			withServiceRegistry(t, &fakeServiceRegistry{services: map[string]api.ServiceInfo{
				"test-server": &fakeServiceInfo{name: "test-server", state: tc.state},
			}})
			// No token on the context: the no-op paths must not require one.
			h := newCallerHarness(t, existingServer(), nil)

			result, handled, err := h.adapter.StartMCPServerAsCaller(context.Background(), "test-server")
			if err != nil || !handled {
				t.Fatalf("handled=%v err=%v", handled, err)
			}
			if result.IsError != tc.isError || !strings.Contains(resultText(t, result), tc.want) {
				t.Fatalf("state %s: got isError=%v %q, want isError=%v containing %q",
					tc.state, result.IsError, resultText(t, result), tc.isError, tc.want)
			}
			if len(h.updated) != 0 || len(h.tokensSeen) != 0 {
				t.Fatal("no-op state must not write")
			}
		})
	}
}

// TestLifecycleAsCaller_RestartWritesTimestamp: core_service_restart becomes a
// spec.restartRequestedAt write; the field is the audit record of the request.
func TestLifecycleAsCaller_RestartWritesTimestamp(t *testing.T) {
	h := newCallerHarness(t, existingServer(), nil)
	ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))

	result, handled, err := h.adapter.RestartMCPServerAsCaller(ctx, "test-server")
	if err != nil || !handled || result.IsError {
		t.Fatalf("handled=%v err=%v result=%s", handled, err, resultText(t, result))
	}
	if len(h.updatedObjs) != 1 || h.updatedObjs[0].Spec.RestartRequestedAt == nil {
		t.Fatalf("expected one update writing spec.restartRequestedAt, got %+v", h.updatedObjs)
	}
}

// TestLifecycleAsCaller_RestartSuspendedRefused: the reconciler consumes
// restart requests on suspended servers without action, so the tool refuses
// up front and points at core_service_start.
func TestLifecycleAsCaller_RestartSuspendedRefused(t *testing.T) {
	h := newCallerHarness(t, suspendedServer(), nil)
	ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))

	result, handled, err := h.adapter.RestartMCPServerAsCaller(ctx, "test-server")
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "core_service_start") {
		t.Fatalf("expected refusal pointing at core_service_start, got: %s", resultText(t, result))
	}
	if len(h.updated) != 0 {
		t.Fatal("refused restart must not write")
	}
}

// TestLifecycleAsCaller_RestartAuthRequiredRefused: restarting cannot
// authenticate a session, so a server waiting on OAuth gets the same
// core_auth_login guidance the imperative path gave, and no write happens.
func TestLifecycleAsCaller_RestartAuthRequiredRefused(t *testing.T) {
	withServiceRegistry(t, &fakeServiceRegistry{services: map[string]api.ServiceInfo{
		"test-server": &fakeServiceInfo{name: "test-server", state: api.StateAuthRequired},
	}})
	h := newCallerHarness(t, existingServer(), nil)
	ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))

	result, handled, err := h.adapter.RestartMCPServerAsCaller(ctx, "test-server")
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "core_auth_login") {
		t.Fatalf("expected OAuth guidance, got: %s", resultText(t, result))
	}
	if len(h.updated) != 0 {
		t.Fatal("auth-required restart must not write")
	}
}

// TestLifecycleAsCaller_StopAlreadySuspended: stop is idempotent — an already
// suspended server needs no write and therefore no write identity.
func TestLifecycleAsCaller_StopAlreadySuspended(t *testing.T) {
	h := newCallerHarness(t, suspendedServer(), nil)

	result, handled, err := h.adapter.StopMCPServerAsCaller(context.Background(), "test-server")
	if err != nil || !handled || result.IsError {
		t.Fatalf("handled=%v err=%v result=%s", handled, err, resultText(t, result))
	}
	if !strings.Contains(resultText(t, result), "already suspended") {
		t.Fatalf("unexpected message: %s", resultText(t, result))
	}
	if len(h.updated) != 0 || len(h.tokensSeen) != 0 {
		t.Fatal("idempotent stop must not write")
	}
}

// TestLifecycleAsCaller_DeniedBeforeWrite: sessions without a usable bearer
// get the same re-login errors as create/update/delete, before any write.
func TestLifecycleAsCaller_DeniedBeforeWrite(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		t.Run(action+" no token", func(t *testing.T) {
			h := newCallerHarness(t, existingServer(), nil)
			result, handled, err := lifecycleCall(t, h.adapter, context.Background(), action, "test-server")
			if err != nil || !handled {
				t.Fatalf("handled=%v err=%v", handled, err)
			}
			if !result.IsError || !strings.Contains(resultText(t, result), "re-login") {
				t.Fatalf("expected re-login error, got: %s", resultText(t, result))
			}
			if len(h.tokensSeen) != 0 || len(h.updated) != 0 {
				t.Fatal("denied call must not write")
			}
		})
		t.Run(action+" missing audience", func(t *testing.T) {
			h := newCallerHarness(t, existingServer(), nil)
			ctx := server.ContextWithIDToken(context.Background(), testJWT(t, "muster-client"))
			result, handled, err := lifecycleCall(t, h.adapter, ctx, action, "test-server")
			if err != nil || !handled {
				t.Fatalf("handled=%v err=%v", handled, err)
			}
			text := resultText(t, result)
			if !result.IsError || !strings.Contains(text, callerwrite.DefaultKubernetesAudience) || !strings.Contains(text, "re-login") {
				t.Fatalf("expected audience re-login error, got: %s", text)
			}
			if len(h.tokensSeen) != 0 {
				t.Fatal("factory must not be called for a token without the audience")
			}
		})
	}
}

// TestLifecycleAsCaller_ForbiddenMapsToPermissionError: a 403 on the lifecycle
// write maps to the same actionable permission error as create/update/delete,
// naming the action, resource, namespace, and role.
func TestLifecycleAsCaller_ForbiddenMapsToPermissionError(t *testing.T) {
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
		"test-server", fmt.Errorf(`User "alice" cannot update resource "mcpservers"`))

	for _, action := range []string{"start", "stop", "restart"} {
		t.Run(action, func(t *testing.T) {
			existing := existingServer()
			if action == "start" {
				// Route start through the resume transition so a write happens.
				existing.Spec.Suspended = true
			}
			h := newCallerHarness(t, existing, forbidden)
			ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))

			result, handled, err := lifecycleCall(t, h.adapter, ctx, action, "test-server")
			if err != nil || !handled {
				t.Fatalf("handled=%v err=%v", handled, err)
			}
			if !result.IsError {
				t.Fatal("expected a tool error on 403")
			}
			text := resultText(t, result)
			for _, want := range []string{"Permission denied", action, "mcpservers.muster.giantswarm.io", `"test-ns"`, "mcpserver-editor"} {
				if !strings.Contains(text, want) {
					t.Errorf("403 mapping %q does not mention %q", text, want)
				}
			}
		})
	}
}
