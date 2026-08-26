package workflow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/callerwrite"
	"github.com/giantswarm/muster/internal/client"
	"github.com/giantswarm/muster/internal/server"
)

// stubMusterClient is the SA-backed client stand-in. Only the methods the
// mutation handlers touch are implemented; anything else panics via the
// embedded nil interface, which is exactly what we want — the caller path
// must never fall back to the SA client for writes.
type stubMusterClient struct {
	client.MusterClient
	existing *musterv1alpha1.Workflow
	created  []string
	updated  []string
	deleted  []string
}

func (s *stubMusterClient) IsKubernetesMode() bool { return true }

func (s *stubMusterClient) GetWorkflow(_ context.Context, name, namespace string) (*musterv1alpha1.Workflow, error) {
	if s.existing != nil && s.existing.Name == name {
		return s.existing.DeepCopy(), nil
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: "muster.giantswarm.io", Resource: "workflows"}, name)
}

func (s *stubMusterClient) CreateWorkflow(_ context.Context, obj *musterv1alpha1.Workflow) error {
	s.created = append(s.created, obj.Name)
	return nil
}

func (s *stubMusterClient) UpdateWorkflow(_ context.Context, obj *musterv1alpha1.Workflow) error {
	s.updated = append(s.updated, obj.Name)
	return nil
}

func (s *stubMusterClient) DeleteWorkflow(_ context.Context, name, _ string) error {
	s.deleted = append(s.deleted, name)
	return nil
}

// testJWT builds an unsigned-but-JWT-shaped token with the given aud claim
// (string or []string). The write path never verifies signatures — the
// apiserver does — so a fake signature part suffices.
func testJWT(t *testing.T, aud interface{}) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := map[string]interface{}{"sub": "alice@example.com", "iss": "https://dex.example.com"}
	if aud != nil {
		claims["aud"] = aud
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// callerHarness wires an adapter with writes-as-caller on: the factory
// records the bearer it was handed, and the returned client records writes
// through interceptors (optionally failing them with injectErr).
type callerHarness struct {
	adapter    *Adapter
	sa         *stubMusterClient
	tokensSeen []string
	created    []string
	updated    []string
	deleted    []string
}

func newCallerHarness(t *testing.T, existing *musterv1alpha1.Workflow, injectErr error) *callerHarness {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(musterv1alpha1.AddToScheme(scheme))

	h := &callerHarness{sa: &stubMusterClient{existing: existing}}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(context.Context, ctrlclient.WithWatch, ctrlclient.Object, ...ctrlclient.CreateOption) error {
			if injectErr != nil {
				return injectErr
			}
			h.created = append(h.created, "create")
			return nil
		},
		Update: func(context.Context, ctrlclient.WithWatch, ctrlclient.Object, ...ctrlclient.UpdateOption) error {
			if injectErr != nil {
				return injectErr
			}
			h.updated = append(h.updated, "update")
			return nil
		},
		Delete: func(context.Context, ctrlclient.WithWatch, ctrlclient.Object, ...ctrlclient.DeleteOption) error {
			if injectErr != nil {
				return injectErr
			}
			h.deleted = append(h.deleted, "delete")
			return nil
		},
	}).Build()

	h.adapter = NewAdapterWithClient(h.sa, "test-ns", nil, nil, "")
	t.Cleanup(h.adapter.stopGC)
	h.adapter.EnableWritesAsCaller(func(bearerToken string) (ctrlclient.Client, error) {
		h.tokensSeen = append(h.tokensSeen, bearerToken)
		return fakeClient, nil
	}, "")
	return h
}

func resultText(t *testing.T, result *api.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	return fmt.Sprintf("%v", result.Content[0])
}

func existingWorkflow() *musterv1alpha1.Workflow {
	obj := &musterv1alpha1.Workflow{}
	obj.Name = "test-workflow"
	obj.Namespace = "test-ns"
	obj.Spec.Steps = []musterv1alpha1.WorkflowStep{{ID: "s1", Tool: "core_service_list"}}
	return obj
}

// workflowArgs is a minimal valid workflow definition for create/update.
func workflowArgs(name string) map[string]interface{} {
	return map[string]interface{}{
		"name": name,
		"steps": []interface{}{
			map[string]interface{}{"id": "s1", "tool": "core_service_list"},
		},
	}
}

// TestWritesAsCaller_IdentityReachesWrite is the regression guard against the
// context.Background() pattern: the id_token on the request context must be
// the exact bearer handed to the per-call client factory, for all three
// mutations.
func TestWritesAsCaller_IdentityReachesWrite(t *testing.T) {
	token := testJWT(t, callerwrite.DefaultKubernetesAudience)

	cases := []struct {
		tool string
		args map[string]interface{}
	}{
		{"workflow_create", workflowArgs("test-workflow")},
		{"workflow_update", workflowArgs("test-workflow")},
		{"workflow_delete", map[string]interface{}{"name": "test-workflow"}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			h := newCallerHarness(t, existingWorkflow(), nil)
			ctx := server.ContextWithIDToken(context.Background(), token)

			result, err := h.adapter.ExecuteTool(ctx, tc.tool, tc.args)
			if err != nil {
				t.Fatalf("ExecuteTool: %v", err)
			}
			if result.IsError {
				t.Fatalf("unexpected tool error: %s", resultText(t, result))
			}
			if len(h.tokensSeen) != 1 || h.tokensSeen[0] != token {
				t.Fatalf("caller bearer did not reach the write: factory saw %v", h.tokensSeen)
			}
			if writes := len(h.created) + len(h.updated) + len(h.deleted); writes != 1 {
				t.Fatalf("expected exactly one caller-client write, got %d", writes)
			}
			if len(h.sa.created)+len(h.sa.updated)+len(h.sa.deleted) != 0 {
				t.Fatalf("SA client performed a write on the caller path")
			}
		})
	}
}

func TestWritesAsCaller_MissingAudience(t *testing.T) {
	for name, aud := range map[string]interface{}{
		"wrong-string-aud": "muster-client",
		"wrong-list-aud":   []string{"muster-client", "other"},
		"no-aud":           nil,
	} {
		t.Run(name, func(t *testing.T) {
			h := newCallerHarness(t, existingWorkflow(), nil)
			ctx := server.ContextWithIDToken(context.Background(), testJWT(t, aud))

			result, err := h.adapter.ExecuteTool(ctx, "workflow_create", workflowArgs("test-workflow"))
			if err != nil {
				t.Fatalf("ExecuteTool: %v", err)
			}
			if !result.IsError {
				t.Fatal("expected a tool error for a token without the kubernetes audience")
			}
			text := resultText(t, result)
			for _, want := range []string{callerwrite.DefaultKubernetesAudience, "re-login"} {
				if !strings.Contains(text, want) {
					t.Errorf("error %q does not mention %q", text, want)
				}
			}
			if len(h.tokensSeen) != 0 {
				t.Fatal("factory must not be called for a token without the audience")
			}
		})
	}
}

func TestWritesAsCaller_NoSessionToken(t *testing.T) {
	h := newCallerHarness(t, existingWorkflow(), nil)

	result, err := h.adapter.ExecuteTool(context.Background(), "workflow_delete",
		map[string]interface{}{"name": "test-workflow"})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "re-login") {
		t.Fatalf("expected a re-login error, got: %s", resultText(t, result))
	}
}

func TestWritesAsCaller_ForbiddenMapsToPermissionError(t *testing.T) {
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Group: "muster.giantswarm.io", Resource: "workflows"},
		"test-workflow", fmt.Errorf(`User "alice" cannot create resource "workflows"`))

	cases := []struct {
		tool string
		verb string
		args map[string]interface{}
	}{
		{"workflow_create", "create", workflowArgs("test-workflow")},
		{"workflow_update", "update", workflowArgs("test-workflow")},
		{"workflow_delete", "delete", map[string]interface{}{"name": "test-workflow"}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			h := newCallerHarness(t, existingWorkflow(), forbidden)
			ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))

			result, err := h.adapter.ExecuteTool(ctx, tc.tool, tc.args)
			if err != nil {
				t.Fatalf("ExecuteTool: %v", err)
			}
			if !result.IsError {
				t.Fatal("expected a tool error on 403")
			}
			text := resultText(t, result)
			for _, want := range []string{"Permission denied", tc.verb, "workflows.muster.giantswarm.io", `"test-ns"`, "workflow-editor"} {
				if !strings.Contains(text, want) {
					t.Errorf("403 mapping %q does not mention %q", text, want)
				}
			}
		})
	}
}

func TestWritesAsCaller_UnauthorizedMapsToRelogin(t *testing.T) {
	h := newCallerHarness(t, existingWorkflow(), apierrors.NewUnauthorized("token expired"))
	ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))

	result, err := h.adapter.ExecuteTool(ctx, "workflow_create", workflowArgs("test-workflow"))
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "re-login") {
		t.Fatalf("expected a re-login error on 401, got: %s", resultText(t, result))
	}
}

// TestWritesAsCaller_FlagOff pins the filesystem-mode behavior: with the gate
// off the SA client performs the write even when the session carries a token.
func TestWritesAsCaller_FlagOff(t *testing.T) {
	sa := &stubMusterClient{existing: existingWorkflow()}
	adapter := NewAdapterWithClient(sa, "test-ns", nil, nil, "")
	t.Cleanup(adapter.stopGC)
	ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))

	result, err := adapter.ExecuteTool(ctx, "workflow_create", workflowArgs("another-workflow"))
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", resultText(t, result))
	}
	if len(sa.created) != 1 || sa.created[0] != "another-workflow" {
		t.Fatalf("SA client did not perform the write with the gate off: %v", sa.created)
	}
}

// TestWritesAsCaller_ValidateUntouched pins that validation stays available to
// callers who cannot write: no token, gate on, validate still succeeds.
func TestWritesAsCaller_ValidateUntouched(t *testing.T) {
	h := newCallerHarness(t, nil, nil)

	result, err := h.adapter.ExecuteTool(context.Background(), "workflow_validate", workflowArgs("test-workflow"))
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("validate must not require a write identity: %s", resultText(t, result))
	}
}
