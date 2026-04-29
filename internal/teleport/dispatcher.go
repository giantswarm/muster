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
// "Cert-to-app binding constraint"):
//
//   - spec.transport unset                 → direct-HTTPS, no Dex client.
//   - spec.transport.type == "teleport"    → two mTLS clients derived from
//     spec.transport.teleport.cluster by the <role>-<cluster> convention
//     (mcp-kubernetes-{cluster} + dex-{cluster}, secrets
//     tbot-identity-mcp-{cluster} + tbot-identity-tx-{cluster}).
//
// The dispatcher returns a typed error on failure so the aggregator (TB-8) can
// surface a structured MCPServer.status.conditions[type=TransportReady]
// without re-parsing error strings.

// Roles in the <role>-<cluster> derivation.
const (
	// MCPRole is the Teleport-app role for MCP traffic
	// (mcp-kubernetes-{cluster}).
	MCPRole = "mcp-kubernetes"
	// DexRole is the Teleport-app role for Dex token-exchange traffic
	// (dex-{cluster}).
	DexRole = "dex"

	// secretPrefix is the tbot-output secret prefix
	// (tbot-identity-{kind}-{cluster}).
	secretPrefix = "tbot-identity"
	// secretKindMCP is the secret-name infix for the MCP role.
	secretKindMCP = "mcp"
	// secretKindTX is the secret-name infix for the token-exchange role.
	secretKindTX = "tx"

	// DefaultSecretNamespace is the default namespace for tbot-output
	// identity secrets when the dispatcher is constructed without an explicit
	// override. Matches PLAN §6 TB-4.
	DefaultSecretNamespace = "muster-system"
)

// MCPAppName returns the canonical Teleport application name for the MCP role.
// Locked convention from PLAN §6 TB-1/TB-2/TB-4.
func MCPAppName(cluster string) string { return MCPRole + "-" + cluster }

// DexAppName returns the canonical Teleport application name for the Dex
// token-exchange role. Locked convention from PLAN §6 TB-1/TB-2/TB-4.
func DexAppName(cluster string) string { return DexRole + "-" + cluster }

// MCPSecretName returns the canonical tbot-output identity secret name for the
// MCP role. Locked convention from PLAN §6 TB-4.
func MCPSecretName(cluster string) string {
	return secretPrefix + "-" + secretKindMCP + "-" + cluster
}

// DexSecretName returns the canonical tbot-output identity secret name for the
// Dex token-exchange role. Locked convention from PLAN §6 TB-4.
func DexSecretName(cluster string) string {
	return secretPrefix + "-" + secretKindTX + "-" + cluster
}

// Sentinel errors. The aggregator (TB-8) uses errors.Is to map these to the
// canonical MCPServer.status.conditions[type=TransportReady, status=False]
// reason via MapErrorToCondition.
var (
	// ErrClusterNotConfigured is returned when spec.transport.teleport.cluster
	// is not present in the dispatcher's configured known-clusters set.
	ErrClusterNotConfigured = errors.New("teleport cluster not configured in muster Deployment")
	// ErrSecretMissing is returned when an expected tbot-identity secret does
	// not exist in the configured secret namespace.
	ErrSecretMissing = errors.New("teleport identity secret not found")
	// ErrSecretInvalid is returned when an expected tbot-identity secret
	// exists but is missing required keys (tlscert, key,
	// teleport-application-ca.pem) or contains invalid PEM data.
	ErrSecretInvalid = errors.New("teleport identity secret malformed")
)

// TransportError wraps a sentinel error with structured context (cluster +
// secret) so the aggregator can produce a human-friendly status message
// without rebuilding it from the error string. Use errors.Is against the
// sentinel errors above.
type TransportError struct {
	// Sentinel is one of ErrClusterNotConfigured / ErrSecretMissing /
	// ErrSecretInvalid. Set on every TransportError.
	Sentinel error
	// Cluster is the spec.transport.teleport.cluster value that triggered
	// the error. Empty if unavailable.
	Cluster string
	// Secret is the tbot-identity secret name relevant to the error. Empty
	// when the error is cluster-scoped (ErrClusterNotConfigured) or pre-CR.
	Secret string
	// Detail carries an underlying error (e.g. a malformed PEM error) when
	// available. nil when no underlying error exists.
	Detail error
}

// Error renders a stable, status-condition-quality message.
func (e *TransportError) Error() string {
	switch {
	case errors.Is(e.Sentinel, ErrClusterNotConfigured):
		return fmt.Sprintf("cluster %q not configured", e.Cluster)
	case errors.Is(e.Sentinel, ErrSecretMissing):
		return fmt.Sprintf("secret %q not found (cluster %q)", e.Secret, e.Cluster)
	case errors.Is(e.Sentinel, ErrSecretInvalid):
		if e.Detail != nil {
			return fmt.Sprintf("secret %q is invalid: %v", e.Secret, e.Detail)
		}
		return fmt.Sprintf("secret %q is invalid", e.Secret)
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
	ReasonClusterNotConfigured = "ClusterNotConfigured"
	ReasonSecretMissing        = "SecretMissing"
	ReasonSecretInvalid        = "SecretInvalid"
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
	case errors.Is(err, ErrClusterNotConfigured):
		return ReasonClusterNotConfigured, err.Error()
	case errors.Is(err, ErrSecretMissing):
		return ReasonSecretMissing, err.Error()
	case errors.Is(err, ErrSecretInvalid):
		return ReasonSecretInvalid, err.Error()
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
	// When mcp.spec.transport.type == "teleport" both clients are configured
	// with mTLS (the matching tbot-output cert) and an appNameTransport that
	// rewrites the Host header to the derived <role>-<cluster> app name.
	ClientsFor(ctx context.Context, mcp *v1alpha1.MCPServer) (mcpClient, dexClient *http.Client, err error)
}

// dispatcher is the default TransportDispatcher implementation backed by the
// Adapter's per-secret ClientProvider cache.
type dispatcher struct {
	adapter         *Adapter
	knownClusters   map[string]struct{}
	secretNamespace string
}

// NewTransportDispatcher constructs a TransportDispatcher. The k8s client is
// used to load tbot-identity Secrets from secretNamespace. knownClusters is
// the helm-rendered set of clusters the muster Deployment has identity
// material for; an empty/nil set means "no Teleport clusters configured" —
// any CR with spec.transport.teleport will fail with ErrClusterNotConfigured.
//
// secretNamespace must be in security.AllowedNamespaces (the dispatcher
// rejects mismatches up-front to surface the constraint at construction
// rather than at the first request).
func NewTransportDispatcher(k8s client.Client, knownClusters []string, secretNamespace string) (TransportDispatcher, error) {
	if secretNamespace == "" {
		secretNamespace = DefaultSecretNamespace
	}
	if err := ValidateNamespace(secretNamespace); err != nil {
		return nil, fmt.Errorf("dispatcher secret namespace: %w", err)
	}

	known := make(map[string]struct{}, len(knownClusters))
	for _, c := range knownClusters {
		if c == "" {
			continue
		}
		known[c] = struct{}{}
	}

	return &dispatcher{
		adapter:         NewAdapterWithClient(k8s),
		knownClusters:   known,
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

	cluster := t.Teleport.Cluster
	if _, ok := d.knownClusters[cluster]; !ok {
		incLookup(resultClusterUnknown)
		return nil, nil, &TransportError{
			Sentinel: ErrClusterNotConfigured,
			Cluster:  cluster,
		}
	}

	// Pre-flight existence check on both secrets so we either return both
	// clients or none (no half-configured success surface for TB-8).
	mcpSecret := MCPSecretName(cluster)
	dexSecret := DexSecretName(cluster)
	if err := d.checkSecretExists(ctx, mcpSecret); err != nil {
		incLookup(resultSecretMissing)
		return nil, nil, &TransportError{
			Sentinel: ErrSecretMissing,
			Cluster:  cluster,
			Secret:   mcpSecret,
			Detail:   err,
		}
	}
	if err := d.checkSecretExists(ctx, dexSecret); err != nil {
		incLookup(resultSecretMissing)
		return nil, nil, &TransportError{
			Sentinel: ErrSecretMissing,
			Cluster:  cluster,
			Secret:   dexSecret,
			Detail:   err,
		}
	}

	mcpClient, err := d.buildClient(ctx, mcpSecret, MCPAppName(cluster))
	if err != nil {
		// buildClient already incremented muster_transport_secret_load_total.
		// Map any low-level error to ErrSecretInvalid since existence was
		// proven above; PEM/CA parse failures land here.
		incLookup(resultSecretMissing)
		return nil, nil, &TransportError{
			Sentinel: ErrSecretInvalid,
			Cluster:  cluster,
			Secret:   mcpSecret,
			Detail:   err,
		}
	}
	dexClient, err := d.buildClient(ctx, dexSecret, DexAppName(cluster))
	if err != nil {
		incLookup(resultSecretMissing)
		return nil, nil, &TransportError{
			Sentinel: ErrSecretInvalid,
			Cluster:  cluster,
			Secret:   dexSecret,
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
