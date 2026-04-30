package teleport

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/muster/internal/api"
	v1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// configFor builds the api.TeleportClientConfig for a (secret, namespace,
// appName) triple. Centralizes the field shape so the dispatcher and any
// future callers stay in sync with the package-level adapter contract.
func configFor(secret, namespace, appName string) api.TeleportClientConfig {
	return api.TeleportClientConfig{
		IdentitySecretName:      secret,
		IdentitySecretNamespace: namespace,
		AppName:                 appName,
	}
}

// TB-7 — CR-driven transport dispatcher.
//
// The dispatcher takes a reconciling *v1alpha1.MCPServer and returns the HTTP
// clients to use for outbound traffic. It implements the contract documented
// in PLAN §6 TB-7 and §9 ("How to expose 2nd Teleport app/identity ...",
// revised 2026-04-29 to explicit fields):
//
//   - spec.transport unset                                    → direct-HTTPS,
//     no Dex client.
//   - spec.transport.type == "teleport", mcp only             → one mTLS
//     client; dexClient is nil (token exchange disabled, or forwarded over
//     direct-HTTPS).
//   - spec.transport.type == "teleport", mcp + dex            → two mTLS
//     clients keyed off the explicit (appName, identitySecretRef.Name)
//     pairs in the CR. No naming-convention derivation.
//
// The dispatcher returns a typed error on failure so the aggregator (TB-8) can
// surface a structured MCPServer.status.conditions[type=TransportReady]
// without re-parsing error strings.

// DefaultSecretNamespace is the default namespace for tbot-output identity
// secrets when the dispatcher is constructed without an explicit override.
// Used because MCPServer.spec.transport.teleport.{mcp,dex}.identitySecretRef
// is a corev1.LocalObjectReference and carries no namespace; the dispatcher
// constrains where secrets can be loaded from via security.AllowedNamespaces.
// Matches PLAN §6 TB-4.
const DefaultSecretNamespace = "muster-system"

// Sentinel errors. The aggregator (TB-8) uses errors.Is to map these to the
// canonical MCPServer.status.conditions[type=TransportReady, status=False]
// reason via MapErrorToCondition.
var (
	// ErrSecretMissing is returned when an expected tbot-identity secret does
	// not exist in the configured secret namespace.
	ErrSecretMissing = errors.New("teleport identity secret not found")
	// ErrSecretInvalid is returned when an expected tbot-identity secret
	// exists but is missing required keys (tlscert, key,
	// teleport-host-ca.crt) or contains invalid PEM data.
	ErrSecretInvalid = errors.New("teleport identity secret malformed")
	// ErrTransportInvalid is returned when spec.transport is structurally
	// invalid (e.g. type=teleport with no teleport block). CRD-level CEL
	// catches this at admission; the runtime check is defense-in-depth.
	ErrTransportInvalid = errors.New("teleport transport spec invalid")
)

// TransportError wraps a sentinel error with structured context (appName +
// secret) so the aggregator can produce a human-friendly status message
// without rebuilding it from the error string. Use errors.Is against the
// sentinel errors above.
type TransportError struct {
	// Sentinel is one of ErrSecretMissing / ErrSecretInvalid /
	// ErrTransportInvalid. Set on every TransportError.
	Sentinel error
	// AppName is the Teleport application name from the CR that triggered
	// the error. Empty if unavailable.
	AppName string
	// Secret is the tbot-identity secret name relevant to the error. Empty
	// when the error is config-scoped (ErrTransportInvalid).
	Secret string
	// Detail carries an underlying error (e.g. a malformed PEM error) when
	// available. nil when no underlying error exists.
	Detail error
}

// Error renders a stable, status-condition-quality message.
func (e *TransportError) Error() string {
	switch {
	case errors.Is(e.Sentinel, ErrSecretMissing):
		return fmt.Sprintf("secret %q not found (app %q)", e.Secret, e.AppName)
	case errors.Is(e.Sentinel, ErrSecretInvalid):
		if e.Detail != nil {
			return fmt.Sprintf("secret %q is invalid: %v", e.Secret, e.Detail)
		}
		return fmt.Sprintf("secret %q is invalid", e.Secret)
	case errors.Is(e.Sentinel, ErrTransportInvalid):
		if e.Detail != nil {
			return fmt.Sprintf("transport spec invalid: %v", e.Detail)
		}
		return "transport spec invalid"
	default:
		return e.Sentinel.Error()
	}
}

// Unwrap exposes both the sentinel and the underlying detail to errors.Is.
func (e *TransportError) Unwrap() []error {
	if e.Detail == nil {
		return []error{e.Sentinel}
	}
	return []error{e.Sentinel, e.Detail}
}

// Canonical MCPServer.status.conditions[type=TransportReady, status=False]
// reasons. TB-8 sets these verbatim on the CR; TB-12 alerts and TB-13 docs
// reference them by name.
const (
	ReasonSecretMissing = "SecretMissing"
	ReasonSecretInvalid = "SecretInvalid"
	ReasonConfigInvalid = "ConfigInvalid"
	// ReasonTransportError is the catch-all for unexpected dispatcher errors;
	// TB-8 still surfaces them, just without a structured reason.
	ReasonTransportError = "TransportError"
)

// MapErrorToCondition derives the canonical condition (reason, message) for a
// dispatcher error. TB-8 uses this to write
// MCPServer.status.conditions[type=TransportReady, status=False] without
// duplicating the mapping.
//
// Returns ("", "") when err is nil so callers can early-return.
func MapErrorToCondition(err error) (reason, message string) {
	if err == nil {
		return "", ""
	}
	switch {
	case errors.Is(err, ErrSecretMissing):
		return ReasonSecretMissing, err.Error()
	case errors.Is(err, ErrSecretInvalid):
		return ReasonSecretInvalid, err.Error()
	case errors.Is(err, ErrTransportInvalid):
		return ReasonConfigInvalid, err.Error()
	default:
		return ReasonTransportError, err.Error()
	}
}

// TransportDispatcher resolves the HTTP clients to use for a reconciling
// MCPServer. See package-level docs for the contract.
type TransportDispatcher interface {
	// ClientsFor returns the HTTP clients to use for the MCP endpoint and
	// (when token-exchange-over-Teleport is enabled) the Dex token endpoint.
	//
	// When mcp.spec.transport is nil the returned mcpClient is a default
	// http.Client (direct HTTPS) and dexClient is nil; err is nil. Token
	// exchange in that case still goes through the default client.
	//
	// When mcp.spec.transport.type == "teleport" the mcpClient is configured
	// with mTLS for spec.transport.teleport.mcp; dexClient is non-nil only
	// when spec.transport.teleport.dex is also set.
	ClientsFor(ctx context.Context, mcp *v1alpha1.MCPServer) (mcpClient, dexClient *http.Client, err error)
}

// dispatcher is the default TransportDispatcher implementation backed by the
// Adapter's per-secret ClientProvider cache.
type dispatcher struct {
	adapter         *Adapter
	secretNamespace string
}

// NewTransportDispatcher constructs a TransportDispatcher. The k8s client is
// used to load tbot-identity Secrets from secretNamespace. The CR carries
// the explicit (appName, identitySecretRef.Name) pairs; the dispatcher does
// not need a knownClusters allowlist.
//
// secretNamespace must be in security.AllowedNamespaces (the dispatcher
// rejects mismatches up-front to surface the constraint at construction
// rather than at the first request).
func NewTransportDispatcher(k8s client.Client, secretNamespace string) (TransportDispatcher, error) {
	if secretNamespace == "" {
		// Prefer the muster pod's own namespace (where tbot writes identity
		// Secrets for the muster Helm chart's bundled tbot Deployment) before
		// falling back to the legacy default. This matches the autoStart
		// probe's K8S_NAMESPACE/POD_NAMESPACE resolution and lets operators
		// run muster + tbot in any namespace without configuring
		// aggregator.transportRouting.teleport.secretNamespace explicitly.
		if podNS := podNamespace(); podNS != "" {
			secretNamespace = podNS
		} else {
			secretNamespace = DefaultSecretNamespace
		}
	}
	if err := ValidateNamespace(secretNamespace); err != nil {
		return nil, fmt.Errorf("dispatcher secret namespace: %w", err)
	}

	return &dispatcher{
		adapter:         NewAdapterWithClient(k8s),
		secretNamespace: secretNamespace,
	}, nil
}

// ClientsFor implements TransportDispatcher.
func (d *dispatcher) ClientsFor(ctx context.Context, mcp *v1alpha1.MCPServer) (*http.Client, *http.Client, error) {
	// Direct-HTTPS path: spec.transport unset.
	if mcp == nil || mcp.Spec.Transport == nil {
		incLookup(resultNone)
		// A freshly constructed http.Client (rather than http.DefaultClient)
		// keeps the dispatcher's caller from accidentally mutating a
		// process-global. Zero-value Client uses http.DefaultTransport.
		return &http.Client{}, nil, nil
	}

	// Today the only transport type is "teleport"; CEL on the CRD already
	// enforces type=="teleport" ↔ teleport != nil. Defend in depth here.
	t := mcp.Spec.Transport
	if t.Type != "teleport" || t.Teleport == nil {
		incLookup(resultNone)
		return &http.Client{}, nil, nil
	}

	mcpTarget := t.Teleport.MCP
	if mcpTarget.AppName == "" || mcpTarget.IdentitySecretRef.Name == "" {
		incLookup(resultConfigInvalid)
		return nil, nil, &TransportError{
			Sentinel: ErrTransportInvalid,
			Detail:   fmt.Errorf("transport.teleport.mcp must declare appName and identitySecretRef"),
		}
	}

	// Pre-flight existence check on the MCP secret so callers either get a
	// configured client or a typed error. The dex secret is checked below
	// only when the dex target is set.
	if err := d.checkSecretExists(ctx, mcpTarget.IdentitySecretRef.Name); err != nil {
		incLookup(resultSecretMissing)
		return nil, nil, &TransportError{
			Sentinel: ErrSecretMissing,
			AppName:  mcpTarget.AppName,
			Secret:   mcpTarget.IdentitySecretRef.Name,
			Detail:   err,
		}
	}

	mcpClient, err := d.buildClient(ctx, mcpTarget.IdentitySecretRef.Name, mcpTarget.AppName)
	if err != nil {
		// buildClient already incremented muster_transport_secret_load_total.
		// Map any low-level error to ErrSecretInvalid since existence was
		// proven above; PEM/CA parse failures land here.
		incLookup(resultSecretInvalid)
		return nil, nil, &TransportError{
			Sentinel: ErrSecretInvalid,
			AppName:  mcpTarget.AppName,
			Secret:   mcpTarget.IdentitySecretRef.Name,
			Detail:   err,
		}
	}

	// Dex target is optional; CEL guards that it is set when
	// auth.tokenExchange.enabled=true. The dispatcher returns dexClient=nil
	// when the CR omits the dex target.
	if t.Teleport.Dex == nil {
		incLookup(resultResolved)
		return mcpClient, nil, nil
	}

	dexTarget := *t.Teleport.Dex
	if dexTarget.AppName == "" || dexTarget.IdentitySecretRef.Name == "" {
		incLookup(resultConfigInvalid)
		return nil, nil, &TransportError{
			Sentinel: ErrTransportInvalid,
			Detail:   fmt.Errorf("transport.teleport.dex must declare appName and identitySecretRef"),
		}
	}
	if err := d.checkSecretExists(ctx, dexTarget.IdentitySecretRef.Name); err != nil {
		incLookup(resultSecretMissing)
		return nil, nil, &TransportError{
			Sentinel: ErrSecretMissing,
			AppName:  dexTarget.AppName,
			Secret:   dexTarget.IdentitySecretRef.Name,
			Detail:   err,
		}
	}
	dexClient, err := d.buildClient(ctx, dexTarget.IdentitySecretRef.Name, dexTarget.AppName)
	if err != nil {
		incLookup(resultSecretInvalid)
		return nil, nil, &TransportError{
			Sentinel: ErrSecretInvalid,
			AppName:  dexTarget.AppName,
			Secret:   dexTarget.IdentitySecretRef.Name,
			Detail:   err,
		}
	}

	incLookup(resultResolved)
	return mcpClient, dexClient, nil
}

// checkSecretExists returns ErrSecretMissing-grade details when the secret is
// absent. Other errors (RBAC, transport) bubble unchanged.
func (d *dispatcher) checkSecretExists(ctx context.Context, name string) error {
	if d.adapter.k8sClient == nil {
		return fmt.Errorf("kubernetes client unavailable")
	}
	var s corev1.Secret
	err := d.adapter.k8sClient.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: d.secretNamespace,
	}, &s)
	if err == nil {
		return nil
	}
	if apierrors.IsNotFound(err) {
		incSecretLoad(name, resultLoadError)
		return err
	}
	incSecretLoad(name, resultLoadError)
	return err
}

// buildClient produces an mTLS-configured http.Client wrapped with the
// appNameTransport for Teleport's Host-header routing. Reuses the Adapter's
// per-secret ClientProvider cache so cert reload semantics from PR #307 stay
// in effect — TB-7 doesn't reinvent cert handling.
func (d *dispatcher) buildClient(ctx context.Context, secretName, appName string) (*http.Client, error) {
	cli, err := d.adapter.GetHTTPClientForConfig(ctx, configFor(secretName, d.secretNamespace, appName))
	if err != nil {
		incSecretLoad(secretName, resultLoadError)
		return nil, err
	}
	incSecretLoad(secretName, resultLoadOK)
	return cli, nil
}
