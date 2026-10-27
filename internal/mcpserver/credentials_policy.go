package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// policyActiveFor is how long a check that found the policy in force is
// trusted. A policy removed or weakened stops reads within this long.
const policyActiveFor = time.Minute

// WithSecretReferencePolicy makes every read on behalf of an MCPServer depend
// on the chart's admission policy: the ValidatingAdmissionPolicyBinding named
// bindingName must exist, deny (validationActions holds Deny) for every
// MCPServer (no matchResources), and bind a policy that exists and fails
// closed. Otherwise the read is refused with
// api.ErrSecretReferencePolicyInactive. An empty bindingName refuses every
// such read: no policy is configured.
func WithSecretReferencePolicy(bindingName string) CredentialsOption {
	return func(a *CredentialsAdapter) {
		a.policy = &secretReferencePolicy{binding: bindingName}
	}
}

// secretReferencePolicy checks that the admission policy is in force.
type secretReferencePolicy struct {
	binding string

	mu          sync.Mutex
	activeUntil time.Time
	// inactive is the last refusal, logged once until the policy is found in
	// force again.
	inactive string
}

// check returns nil when the policy is in force, read with r.
func (p *secretReferencePolicy) check(ctx context.Context, r client.Reader) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().Before(p.activeUntil) {
		return nil
	}
	if err := p.verify(ctx, r); err != nil {
		if msg := err.Error(); msg != p.inactive {
			p.inactive = msg
			logging.Warn("SecretCredentials", "muster reads no Secret for an MCPServer: %v", err)
		}
		return fmt.Errorf("%w: %w", api.ErrSecretReferencePolicyInactive, err)
	}
	if p.inactive != "" {
		logging.Info("SecretCredentials", "ValidatingAdmissionPolicyBinding %s is in force again", p.binding)
		p.inactive = ""
	}
	p.activeUntil = time.Now().Add(policyActiveFor)
	return nil
}

func (p *secretReferencePolicy) verify(ctx context.Context, r client.Reader) error {
	if p.binding == "" {
		return fmt.Errorf("no admission policy is configured (secretBinding.admissionPolicyBinding)")
	}
	binding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
	if err := r.Get(ctx, client.ObjectKey{Name: p.binding}, binding); err != nil {
		return fmt.Errorf("ValidatingAdmissionPolicyBinding %s: %w", p.binding, err)
	}
	if !slices.Contains(binding.Spec.ValidationActions, admissionregistrationv1.Deny) {
		return fmt.Errorf("ValidatingAdmissionPolicyBinding %s does not deny (validationActions %v)",
			p.binding, binding.Spec.ValidationActions)
	}
	if binding.Spec.MatchResources != nil {
		return fmt.Errorf("ValidatingAdmissionPolicyBinding %s narrows the MCPServers it applies to (matchResources)", p.binding)
	}
	policy := &admissionregistrationv1.ValidatingAdmissionPolicy{}
	if err := r.Get(ctx, client.ObjectKey{Name: binding.Spec.PolicyName}, policy); err != nil {
		return fmt.Errorf("ValidatingAdmissionPolicy %s: %w", binding.Spec.PolicyName, err)
	}
	if fp := policy.Spec.FailurePolicy; fp != nil && *fp != admissionregistrationv1.Fail {
		return fmt.Errorf("ValidatingAdmissionPolicy %s does not fail closed (failurePolicy %s)", policy.Name, *fp)
	}
	return nil
}
