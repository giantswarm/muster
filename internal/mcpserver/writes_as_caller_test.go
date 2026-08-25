package mcpserver

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
	"github.com/giantswarm/muster/internal/client"
	"github.com/giantswarm/muster/internal/server"
)

// stubMusterClient is the SA-backed client stand-in. Only the methods the
// mutation handlers touch are implemented; anything else panics via the
// embedded nil interface, which is exactly what we want — the caller path
// must never fall back to the SA client for writes.
type stubMusterClient struct {
	client.MusterClient
	existing *musterv1alpha1.MCPServer
	created  []string
	updated  []string
	deleted  []string
}

func (s *stubMusterClient) GetMCPServer(_ context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error) {
	if s.existing != nil && s.existing.Name == name {
		return s.existing.DeepCopy(), nil
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"}, name)
}

func (s *stubMusterClient) CreateMCPServer(_ context.Context, obj *musterv1alpha1.MCPServer) error {
	s.created = append(s.created, obj.Name)
	return nil
}

func (s *stubMusterClient) UpdateMCPServer(_ context.Context, obj *musterv1alpha1.MCPServer) error {
	s.updated = append(s.updated, obj.Name)
	return nil
}

func (s *stubMusterClient) DeleteMCPServer(_ context.Context, name, _ string) error {
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

func newCallerHarness(t *testing.T, existing *musterv1alpha1.MCPServer, injectErr error) *callerHarness {
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

	h.adapter = NewAdapterWithClient(h.sa, "test-ns")
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

func existingServer() *musterv1alpha1.MCPServer {
	obj := &musterv1alpha1.MCPServer{}
	obj.Name = "test-server"
	obj.Namespace = "test-ns"
	obj.Spec.Type = "stdio"
	obj.Spec.Command = "echo"
	return obj
}

// TestWritesAsCaller_IdentityReachesWrite is the regression guard against the
// context.Background() pattern: the id_token on the request context must be
// the exact bearer handed to the per-call client factory, for all three
// mutations.
func TestWritesAsCaller_IdentityReachesWrite(t *testing.T) {
	token := testJWT(t, DefaultKubernetesAudience)

	cases := []struct {
		tool string
		args map[string]interface{}
	}{
		{"mcpserver_create", map[string]interface{}{"name": "test-server", "type": "stdio", "command": "echo"}},
		{"mcpserver_update", map[string]interface{}{"name": "test-server", "command": "echo2"}},
		{"mcpserver_delete", map[string]interface{}{"name": "test-server"}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			h := newCallerHarness(t, existingServer(), nil)
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
			h := newCallerHarness(t, existingServer(), nil)
			ctx := server.ContextWithIDToken(context.Background(), testJWT(t, aud))

			result, err := h.adapter.ExecuteTool(ctx, "mcpserver_create",
				map[string]interface{}{"name": "test-server", "type": "stdio", "command": "echo"})
			if err != nil {
				t.Fatalf("ExecuteTool: %v", err)
			}
			if !result.IsError {
				t.Fatal("expected a tool error for a token without the kubernetes audience")
			}
			text := resultText(t, result)
			for _, want := range []string{DefaultKubernetesAudience, "re-login"} {
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
	h := newCallerHarness(t, existingServer(), nil)

	result, err := h.adapter.ExecuteTool(context.Background(), "mcpserver_delete",
		map[string]interface{}{"name": "test-server"})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "re-login") {
		t.Fatalf("expected a re-login error, got: %s", resultText(t, result))
	}
}

func TestWritesAsCaller_ForbiddenMapsToPermissionError(t *testing.T) {
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
		"test-server", fmt.Errorf(`User "alice" cannot create resource "mcpservers"`))

	cases := []struct {
		tool string
		verb string
		args map[string]interface{}
	}{
		{"mcpserver_create", "create", map[string]interface{}{"name": "test-server", "type": "stdio", "command": "echo"}},
		{"mcpserver_update", "update", map[string]interface{}{"name": "test-server", "command": "echo2"}},
		{"mcpserver_delete", "delete", map[string]interface{}{"name": "test-server"}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			h := newCallerHarness(t, existingServer(), forbidden)
			ctx := server.ContextWithIDToken(context.Background(), testJWT(t, DefaultKubernetesAudience))

			result, err := h.adapter.ExecuteTool(ctx, tc.tool, tc.args)
			if err != nil {
				t.Fatalf("ExecuteTool: %v", err)
			}
			if !result.IsError {
				t.Fatal("expected a tool error on 403")
			}
			text := resultText(t, result)
			for _, want := range []string{"Permission denied", tc.verb, "mcpservers.muster.giantswarm.io", `"test-ns"`, "mcpserver-editor"} {
				if !strings.Contains(text, want) {
					t.Errorf("403 mapping %q does not mention %q", text, want)
				}
			}
		})
	}
}

func TestWritesAsCaller_UnauthorizedMapsToRelogin(t *testing.T) {
	h := newCallerHarness(t, existingServer(), apierrors.NewUnauthorized("token expired"))
	ctx := server.ContextWithIDToken(context.Background(), testJWT(t, DefaultKubernetesAudience))

	result, err := h.adapter.ExecuteTool(ctx, "mcpserver_create",
		map[string]interface{}{"name": "test-server", "type": "stdio", "command": "echo"})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "re-login") {
		t.Fatalf("expected a re-login error on 401, got: %s", resultText(t, result))
	}
}

// TestWritesAsCaller_FlagOff pins the legacy behavior: with the flag off the
// SA client performs the write even when the session carries a token.
func TestWritesAsCaller_FlagOff(t *testing.T) {
	sa := &stubMusterClient{existing: existingServer()}
	adapter := NewAdapterWithClient(sa, "test-ns")
	ctx := server.ContextWithIDToken(context.Background(), testJWT(t, DefaultKubernetesAudience))

	result, err := adapter.ExecuteTool(ctx, "mcpserver_create",
		map[string]interface{}{"name": "another-server", "type": "stdio", "command": "echo"})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", resultText(t, result))
	}
	if len(sa.created) != 1 || sa.created[0] != "another-server" {
		t.Fatalf("SA client did not perform the write with the flag off: %v", sa.created)
	}
}

// TestWritesAsCaller_ValidateUntouched pins that validation stays available to
// callers who cannot write: no token, flag on, validate still succeeds.
func TestWritesAsCaller_ValidateUntouched(t *testing.T) {
	h := newCallerHarness(t, nil, nil)

	result, err := h.adapter.ExecuteTool(context.Background(), "mcpserver_validate",
		map[string]interface{}{"name": "test-server", "type": "stdio", "command": "echo"})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("validate must not require a write identity: %s", resultText(t, result))
	}
}

func TestTokenMissingAudience(t *testing.T) {
	jwtWith := func(aud interface{}) string { return testJWT(t, aud) }
	cases := []struct {
		name    string
		token   string
		missing bool
	}{
		{"string-match", jwtWith(DefaultKubernetesAudience), false},
		{"list-match", jwtWith([]string{"x", DefaultKubernetesAudience}), false},
		{"string-mismatch", jwtWith("other"), true},
		{"list-mismatch", jwtWith([]string{"a", "b"}), true},
		{"absent", jwtWith(nil), true},
		{"opaque-token-passes-through", "not-a-jwt", false},
	}
	for _, tc := range cases {
		if got := tokenMissingAudience(tc.token, DefaultKubernetesAudience); got != tc.missing {
			t.Errorf("%s: tokenMissingAudience=%v, want %v", tc.name, got, tc.missing)
		}
	}
}
