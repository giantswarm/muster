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

// CR-driven transport dispatcher.
//
// The dispatcher takes a reconciling *v1alpha1.MCPServer and returns the HTTP
// client to use for outbound traffic. After the May-2026 reshape the two
// transports (MCP endpoint and token-exchange endpoint) are independent and
// looked up via two separate methods; the per-method semantics are:
//
//   - transport unset / type=="http"        → direct-HTTPS client.
//   - transport.type=="teleport"            → mTLS client built from the
//     explicit (appName, identitySecretRef.Name) pair in the CR. No
//     naming-convention derivation.
//
// The dispatcher returns a typed error on failure so the aggregator can
// surface a structured MCPServer.status.conditions[type=TransportReady]
// without re-parsing error strings.

// DefaultSecretNamespace is the default namespace for tbot-output identity
// secrets when the dispatcher is constructed without an explicit override.
// Used because MCPServer transport.teleport.identitySecretRef is a
// corev1.LocalObjectReference and carries no namespace; the dispatcher
// constrains where secrets can be loaded from via security.AllowedNamespaces.
const DefaultSecretNamespace = "muster"

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
//
// The two transport lookups are split because the two endpoints (MCP and
// token-exchange) are independent and may use different transports — e.g.
// MCP via direct-HTTPS while the token endpoint goes through Teleport.
type TransportDispatcher interface {
	// MCPClientFor returns the HTTP client to use for the MCP endpoint.
	// Reads mcp.Spec.Transport.
	//
	// When the transport is unset or type=="http", returns a default
	// http.Client (direct HTTPS) and err is nil.
	//
	// When type=="teleport", returns a per-secret mTLS client configured
	// for the explicit (appName, identitySecretRef.Name) pair.
	MCPClientFor(ctx context.Context, mcp *v1alpha1.MCPServer) (*http.Client, error)

	// TokenExchangeClientFor returns the HTTP client to use for the token
	// exchange endpoint. Reads
	// mcp.Spec.Auth.TokenExchange.Transport.
	//
	// When that transport is unset / type=="http" / token exchange is not
	// enabled, returns a default http.Client (direct HTTPS).
	//
	// When type=="teleport", returns a per-secret mTLS client configured
	// for the explicit (appName, identitySecretRef.Name) pair on the
	// per-tokenExchange transport.
	TokenExchangeClientFor(ctx context.Context, mcp *v1alpha1.MCPServer) (*http.Client, error)
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
		secretNamespace = DefaultSecretNamespace
	}
	if err := ValidateNamespace(secretNamespace); err != nil {
		return nil, fmt.Errorf("dispatcher secret namespace: %w", err)
	}

	return &dispatcher{
		adapter:         NewAdapterWithClient(k8s),
		secretNamespace: secretNamespace,
	}, nil
}

// MCPClientFor implements TransportDispatcher.
func (d *dispatcher) MCPClientFor(ctx context.Context, mcp *v1alpha1.MCPServer) (*http.Client, error) {
	if mcp == nil {
		return d.directClient(), nil
	}
	return d.resolveTransport(ctx, mcp.Spec.Transport)
}

// TokenExchangeClientFor implements TransportDispatcher.
func (d *dispatcher) TokenExchangeClientFor(ctx context.Context, mcp *v1alpha1.MCPServer) (*http.Client, error) {
	if mcp == nil || mcp.Spec.Auth == nil || mcp.Spec.Auth.TokenExchange == nil {
		return d.directClient(), nil
	}
	return d.resolveTransport(ctx, mcp.Spec.Auth.TokenExchange.Transport)
}

// directClient returns a freshly constructed http.Client (rather than
// http.DefaultClient) so callers cannot accidentally mutate a process-global.
func (d *dispatcher) directClient() *http.Client { return &http.Client{} }

// resolveTransport encapsulates the per-transport lookup shared by
// MCPClientFor and TokenExchangeClientFor. nil/http transports return a
// direct-HTTPS client; teleport transports return a per-secret mTLS client.
func (d *dispatcher) resolveTransport(ctx context.Context, t *v1alpha1.MCPServerTransport) (*http.Client, error) {
	if t == nil || t.Type == "" || t.Type == "http" {
		incLookup(resultNone)
		return d.directClient(), nil
	}

	// Today the only non-http transport is "teleport"; CEL on the CRD
	// already enforces type=="teleport" ↔ teleport != nil. Defend in depth.
	if t.Type != "teleport" || t.Teleport == nil {
		incLookup(resultNone)
		return d.directClient(), nil
	}

	target := t.Teleport
	if target.AppName == "" || target.IdentitySecretRef.Name == "" {
		incLookup(resultConfigInvalid)
		return nil, &TransportError{
			Sentinel: ErrTransportInvalid,
			Detail:   fmt.Errorf("transport.teleport must declare appName and identitySecretRef"),
		}
	}

	// Pre-flight existence check so callers either get a configured client
	// or a typed error.
	if err := d.checkSecretExists(ctx, target.IdentitySecretRef.Name); err != nil {
		incLookup(resultSecretMissing)
		return nil, &TransportError{
			Sentinel: ErrSecretMissing,
			AppName:  target.AppName,
			Secret:   target.IdentitySecretRef.Name,
			Detail:   err,
		}
	}

	cli, err := d.buildClient(ctx, target.IdentitySecretRef.Name, target.AppName)
	if err != nil {
		// buildClient already incremented muster_transport_secret_load_total.
		// Map any low-level error to ErrSecretInvalid since existence was
		// proven above; PEM/CA parse failures land here.
		incLookup(resultSecretInvalid)
		return nil, &TransportError{
			Sentinel: ErrSecretInvalid,
			AppName:  target.AppName,
			Secret:   target.IdentitySecretRef.Name,
			Detail:   err,
		}
	}

	incLookup(resultResolved)
	return cli, nil
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
