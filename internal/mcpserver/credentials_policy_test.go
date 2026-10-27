package mcpserver

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/giantswarm/muster/v5/internal/api"
)

const policyName = "muster-mcpserver-secret-references"

func referencePolicy(opts ...func(*admissionregistrationv1.ValidatingAdmissionPolicy)) *admissionregistrationv1.ValidatingAdmissionPolicy {
	fail := admissionregistrationv1.Fail
	p := &admissionregistrationv1.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: policyName},
		Spec:       admissionregistrationv1.ValidatingAdmissionPolicySpec{FailurePolicy: &fail},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func referenceBinding(opts ...func(*admissionregistrationv1.ValidatingAdmissionPolicyBinding)) *admissionregistrationv1.ValidatingAdmissionPolicyBinding {
	b := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: policyName},
		Spec: admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec{
			PolicyName:        policyName,
			ValidationActions: []admissionregistrationv1.ValidationAction{admissionregistrationv1.Deny},
		},
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

func policyClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func readForGithub(ctx context.Context, adapter *CredentialsAdapter) error {
	_, err := adapter.LoadMCPServerSecretKey(ctx, githubServer, api.SecretFieldHeadersFrom,
		&api.ClientCredentialsSecretRef{Name: "github-token"}, "token")
	return err
}

// TestCredentialsAdapter_SecretReferencePolicy: an MCPServer's Secret is read
// only while the admission policy is in force, and muster's configuration
// reads its Secrets either way.
func TestCredentialsAdapter_SecretReferencePolicy(t *testing.T) {
	ignore := admissionregistrationv1.Ignore
	tests := []struct {
		name    string
		binding string
		objects []client.Object
		wantErr string
	}{
		{name: "in force", binding: policyName,
			objects: []client.Object{referencePolicy(), referenceBinding()}},
		{name: "failure policy unset fails closed", binding: policyName,
			objects: []client.Object{referencePolicy(func(p *admissionregistrationv1.ValidatingAdmissionPolicy) { p.Spec.FailurePolicy = nil }), referenceBinding()}},
		{name: "deny alongside audit", binding: policyName,
			objects: []client.Object{referencePolicy(), referenceBinding(func(b *admissionregistrationv1.ValidatingAdmissionPolicyBinding) {
				b.Spec.ValidationActions = []admissionregistrationv1.ValidationAction{admissionregistrationv1.Deny, admissionregistrationv1.Audit}
			})}},
		{name: "not configured", wantErr: "no admission policy is configured",
			objects: []client.Object{referencePolicy(), referenceBinding()}},
		{name: "binding missing", binding: policyName, wantErr: "not found",
			objects: []client.Object{referencePolicy()}},
		{name: "policy missing", binding: policyName, wantErr: "ValidatingAdmissionPolicy " + policyName,
			objects: []client.Object{referenceBinding()}},
		{name: "warns only", binding: policyName, wantErr: "does not deny",
			objects: []client.Object{referencePolicy(), referenceBinding(func(b *admissionregistrationv1.ValidatingAdmissionPolicyBinding) {
				b.Spec.ValidationActions = []admissionregistrationv1.ValidationAction{admissionregistrationv1.Warn, admissionregistrationv1.Audit}
			})}},
		{name: "narrowed", binding: policyName, wantErr: "matchResources",
			objects: []client.Object{referencePolicy(), referenceBinding(func(b *admissionregistrationv1.ValidatingAdmissionPolicyBinding) {
				b.Spec.MatchResources = &admissionregistrationv1.MatchResources{NamespaceSelector: &metav1.LabelSelector{}}
			})}},
		{name: "fails open", binding: policyName, wantErr: "does not fail closed",
			objects: []client.Object{referencePolicy(func(p *admissionregistrationv1.ValidatingAdmissionPolicy) { p.Spec.FailurePolicy = &ignore }), referenceBinding()}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_ = captureBindingLogs(t)
			objects := append([]client.Object{bindingSecret("github-token")}, tc.objects...)
			adapter := NewCredentialsAdapter(policyClient(t, objects...), WithSecretReferencePolicy(tc.binding))

			err := readForGithub(t.Context(), adapter)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, api.ErrSecretReferencePolicyInactive)
				assert.ErrorContains(t, err, tc.wantErr)
				assert.ErrorContains(t, err, "MCPServer agent-platform/github may not read Secret agent-platform/github-token named in spec.headersFrom")
			}

			_, err = adapter.LoadClientCredentialsForConfig(t.Context(), &api.ClientCredentialsSecretRef{Name: "github-token"}, "agent-platform")
			require.NoError(t, err, "muster's configuration reads its Secrets whatever the policy")
		})
	}
}

// TestCredentialsAdapter_SecretReferencePolicyRechecked: a policy found in
// force is trusted for policyActiveFor; one removed stops reads after that,
// and reads resume as soon as it is back.
func TestCredentialsAdapter_SecretReferencePolicyRechecked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_ = captureBindingLogs(t)
		binding := referenceBinding()
		k8s := policyClient(t, bindingSecret("github-token"), referencePolicy(), binding)
		adapter := NewCredentialsAdapter(k8s, WithSecretReferencePolicy(policyName))
		ctx := context.Background()

		require.NoError(t, readForGithub(ctx, adapter))
		require.NoError(t, k8s.Delete(ctx, binding))
		require.NoError(t, readForGithub(ctx, adapter), "trusted within policyActiveFor")

		time.Sleep(policyActiveFor)
		require.ErrorIs(t, readForGithub(ctx, adapter), api.ErrSecretReferencePolicyInactive)

		binding.ResourceVersion = ""
		require.NoError(t, k8s.Create(ctx, binding))
		require.NoError(t, readForGithub(ctx, adapter), "no wait once it is back")
	})
}

// TestCredentialsAdapter_SecretReferencePolicyLoggedOnce: a policy out of
// force for every read is logged once per reason.
func TestCredentialsAdapter_SecretReferencePolicyLoggedOnce(t *testing.T) {
	logs := captureBindingLogs(t)
	adapter := NewCredentialsAdapter(policyClient(t, bindingSecret("github-token"), referencePolicy()), WithSecretReferencePolicy(policyName))

	for range 5 {
		require.Error(t, readForGithub(t.Context(), adapter))
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "muster reads no Secret for an MCPServer"))
}

// TestCredentialsAdapter_ProtectedBeforePolicy: a protected Secret is refused
// as protected whatever the policy, so its server never waits for a policy
// that will not let it read.
func TestCredentialsAdapter_ProtectedBeforePolicy(t *testing.T) {
	_ = captureBindingLogs(t)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "muster-oauth", Namespace: "agent-platform"}}
	adapter := NewCredentialsAdapter(policyClient(t, secret), WithSecretReferencePolicy(""),
		WithProtectedSecrets(client.ObjectKeyFromObject(secret)))

	_, err := adapter.LoadMCPServerClientCredentials(t.Context(), githubServer, api.SecretFieldAuthorizationServer,
		&api.ClientCredentialsSecretRef{Name: "muster-oauth"})
	require.ErrorIs(t, err, api.ErrSecretProtected)
}
