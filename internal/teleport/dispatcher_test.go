package teleport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// ----- helpers -----

// newFakeK8s builds a fake controller-runtime client with the corev1 scheme
// registered and the supplied Secrets pre-loaded.
func newFakeK8s(t *testing.T, secrets ...*corev1.Secret) *fake.ClientBuilder {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	objs := make([]runtime.Object, 0, len(secrets))
	for _, s := range secrets {
		objs = append(objs, s)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...)
}

// makeIdentitySecret produces a tbot-output-shaped Secret with the keys the
// dispatcher's underlying ClientProvider requires (tlscert, key, CA).
func makeIdentitySecret(t *testing.T, name, namespace string) (*corev1.Secret, []byte) {
	t.Helper()
	certPEM, keyPEM, caPEM := generateTestCertificatePEMs(t)
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data: map[string][]byte{
			DefaultCertFile: certPEM,
			DefaultKeyFile:  keyPEM,
			DefaultCAFile:   caPEM,
		},
	}, certPEM
}

// makeMCPServer builds a minimal MCPServer carrying the requested
// MCP-endpoint transport and (optionally) a token-exchange transport.
// txTransport=nil omits the token-exchange block entirely.
func makeMCPServer(mcpTransport, txTransport *v1alpha1.MCPServerTransport) *v1alpha1.MCPServer {
	mcp := &v1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: v1alpha1.MCPServerSpec{
			Type:      "streamable-http",
			URL:       "https://example.test/mcp",
			Transport: mcpTransport,
		},
	}
	if txTransport != nil {
		mcp.Spec.Auth = &v1alpha1.MCPServerAuth{
			Type: "oauth",
			TokenExchange: &v1alpha1.TokenExchangeConfig{
				Enabled:   true,
				Transport: txTransport,
			},
		}
	}
	return mcp
}

// teleportTransport builds a single-target teleport transport. appName=""
// returns nil so callers can express "transport unset" inline.
func teleportTransport(appName, secret string) *v1alpha1.MCPServerTransport {
	if appName == "" {
		return nil
	}
	return &v1alpha1.MCPServerTransport{
		Type: "teleport",
		Teleport: &v1alpha1.TeleportTransport{
			AppName: appName,
			IdentitySecretRef: corev1.LocalObjectReference{
				Name: secret,
			},
		},
	}
}

// appNameFromTransport unwraps an http.Client's transport stack and returns
// the appName of the appNameTransport. Used to assert that the dispatcher
// wired the right Host-header rewrite onto each client.
func appNameFromTransport(t *testing.T, c *http.Client) string {
	t.Helper()
	if c == nil || c.Transport == nil {
		t.Fatal("expected non-nil transport")
	}
	ant, ok := c.Transport.(*appNameTransport)
	if !ok {
		t.Fatalf("expected *appNameTransport, got %T", c.Transport)
	}
	return ant.appName
}

// tlsLeafSerial returns the leaf-cert serial (decimal) — used to assert
// distinct certs are wired into distinct clients without depending on CN
// uniqueness.
func tlsLeafSerial(t *testing.T, c *http.Client) string {
	t.Helper()
	cert := tlsLeafCert(t, c)
	return cert.SerialNumber.String()
}

func tlsLeafCert(t *testing.T, c *http.Client) *x509.Certificate {
	t.Helper()
	if c == nil || c.Transport == nil {
		t.Fatal("expected non-nil transport")
	}
	rt := http.RoundTripper(c.Transport)
	if ant, ok := rt.(*appNameTransport); ok {
		rt = ant.base
	}
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", rt)
	}
	if tr.TLSClientConfig == nil || len(tr.TLSClientConfig.Certificates) == 0 {
		t.Fatal("expected TLS client cert configured")
	}
	der := tr.TLSClientConfig.Certificates[0].Certificate
	if len(der) == 0 {
		t.Fatal("TLS Certificate has no DER bytes")
	}
	cert, err := x509.ParseCertificate(der[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return cert
}

// silence "unused" on the typed tls import when only x509 is referenced via
// the helpers; tls.Config is part of the http.Transport field.
var _ = tls.VersionTLS12

// ----- tests -----

// (a) — transport unset → plain client; result="none". The token-exchange
// lookup also returns a direct-HTTPS client when the CR omits the
// per-tokenExchange transport.
func TestDispatcher_TransportUnset(t *testing.T) {
	resetMetricsForTest()
	t.Cleanup(resetMetricsForTest)

	k8s := newFakeK8s(t).Build()
	d, err := NewTransportDispatcher(k8s, "muster")
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}

	mcp, err := d.MCPClientFor(context.Background(), makeMCPServer(nil, nil))
	if err != nil {
		t.Fatalf("MCPClientFor: %v", err)
	}
	if mcp == nil {
		t.Fatal("expected non-nil mcp client for direct-HTTPS path")
	}

	tx, err := d.TokenExchangeClientFor(context.Background(), makeMCPServer(nil, nil))
	if err != nil {
		t.Fatalf("TokenExchangeClientFor: %v", err)
	}
	if tx == nil {
		t.Fatal("expected non-nil token-exchange client for direct-HTTPS path")
	}

	// Only the MCP-side path increments lookup metrics; the
	// TokenExchangeClientFor early-returns when Auth.TokenExchange is nil
	// without entering the resolver, so it stays at one bump for the MCP
	// path alone.
	expected := `
# HELP muster_transport_lookup_total Number of CR-driven transport-dispatcher lookups, by outcome.
# TYPE muster_transport_lookup_total counter
muster_transport_lookup_total{result="none"} 1
`
	if err := testutil.CollectAndCompare(transportLookupTotal, strings.NewReader(expected)); err != nil {
		t.Fatalf("metric mismatch: %v", err)
	}
}

// (b) — both transports set + both secrets present → two distinct
// configured clients. This is the canonical "Teleport-routed CR with
// token exchange" path, now with the two transports declared independently.
func TestDispatcher_ResolvedBothTransports(t *testing.T) {
	resetMetricsForTest()
	t.Cleanup(resetMetricsForTest)

	const ns = "muster"
	const mcpApp = "mcp-kubernetes-glean"
	const dexApp = "dex-glean"
	const mcpSecretName = "tbot-identity-mcp-glean" // #nosec G101 -- test fixture; not a credential.
	const dexSecretName = "tbot-identity-tx-glean"  // #nosec G101 -- test fixture; not a credential.
	mcpSecret, mcpCertPEM := makeIdentitySecret(t, mcpSecretName, ns)
	dexSecret, dexCertPEM := makeIdentitySecret(t, dexSecretName, ns)
	if string(mcpCertPEM) == string(dexCertPEM) {
		t.Fatal("test fixture: mcp and dex secrets accidentally got the same cert")
	}

	k8s := newFakeK8s(t, mcpSecret, dexSecret).Build()
	d, err := NewTransportDispatcher(k8s, ns)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}

	cr := makeMCPServer(
		teleportTransport(mcpApp, mcpSecretName),
		teleportTransport(dexApp, dexSecretName),
	)

	mcp, err := d.MCPClientFor(context.Background(), cr)
	if err != nil {
		t.Fatalf("MCPClientFor: %v", err)
	}
	tx, err := d.TokenExchangeClientFor(context.Background(), cr)
	if err != nil {
		t.Fatalf("TokenExchangeClientFor: %v", err)
	}
	if mcp == nil || tx == nil {
		t.Fatalf("expected both clients, got mcp=%v tx=%v", mcp, tx)
	}
	if mcp == tx {
		t.Fatal("expected distinct clients (one per transport)")
	}

	if got := appNameFromTransport(t, mcp); got != mcpApp {
		t.Errorf("mcp client Host=%q, want %q", got, mcpApp)
	}
	if got := appNameFromTransport(t, tx); got != dexApp {
		t.Errorf("token-exchange client Host=%q, want %q", got, dexApp)
	}

	mcpSerial := tlsLeafSerial(t, mcp)
	txSerial := tlsLeafSerial(t, tx)
	if mcpSerial == "" || txSerial == "" {
		t.Fatal("expected non-empty leaf serials on both clients")
	}
	if mcpSerial == txSerial {
		t.Errorf("mcp and token-exchange clients carry the same cert serial %q — secrets not distinct", mcpSerial)
	}

	expectedLookup := `
# HELP muster_transport_lookup_total Number of CR-driven transport-dispatcher lookups, by outcome.
# TYPE muster_transport_lookup_total counter
muster_transport_lookup_total{result="resolved"} 2
`
	if err := testutil.CollectAndCompare(transportLookupTotal, strings.NewReader(expectedLookup)); err != nil {
		t.Fatalf("lookup metric mismatch: %v", err)
	}
	expectedLoad := `
# HELP muster_transport_secret_load_total Number of tbot-identity Secret load attempts, by secret name and outcome.
# TYPE muster_transport_secret_load_total counter
muster_transport_secret_load_total{result="ok",secret="tbot-identity-mcp-glean"} 1
muster_transport_secret_load_total{result="ok",secret="tbot-identity-tx-glean"} 1
`
	if err := testutil.CollectAndCompare(transportSecretLoadTotal, strings.NewReader(expectedLoad)); err != nil {
		t.Fatalf("load metric mismatch: %v", err)
	}
}

// (c) — token-exchange transport omitted (token exchange disabled or
// forwarded) → MCPClientFor returns the configured teleport client;
// TokenExchangeClientFor returns a direct-HTTPS client.
func TestDispatcher_ResolvedMCPOnly(t *testing.T) {
	resetMetricsForTest()
	t.Cleanup(resetMetricsForTest)

	const ns = "muster"
	const mcpApp = "mcp-kubernetes-glean"
	const mcpSecretName = "tbot-identity-mcp-glean" // #nosec G101 -- test fixture; not a credential.
	mcpSecret, _ := makeIdentitySecret(t, mcpSecretName, ns)

	k8s := newFakeK8s(t, mcpSecret).Build()
	d, err := NewTransportDispatcher(k8s, ns)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}

	cr := makeMCPServer(teleportTransport(mcpApp, mcpSecretName), nil)
	mcp, err := d.MCPClientFor(context.Background(), cr)
	if err != nil {
		t.Fatalf("MCPClientFor: %v", err)
	}
	if mcp == nil {
		t.Fatal("expected non-nil mcp client")
	}
	if got := appNameFromTransport(t, mcp); got != mcpApp {
		t.Errorf("mcp client Host=%q, want %q", got, mcpApp)
	}

	tx, err := d.TokenExchangeClientFor(context.Background(), cr)
	if err != nil {
		t.Fatalf("TokenExchangeClientFor: %v", err)
	}
	if tx == nil || tx.Transport != nil {
		t.Errorf("expected default direct-HTTPS token-exchange client, got %+v", tx)
	}
}

// (d) — MCP secret missing → ErrSecretMissing + result="secret_missing".
// Asserts the secret name from the CR appears in the error.
func TestDispatcher_MCPSecretMissing(t *testing.T) {
	resetMetricsForTest()
	t.Cleanup(resetMetricsForTest)

	const ns = "muster"
	const mcpApp = "mcp-kubernetes-glean"
	const mcpSecretName = "tbot-identity-mcp-glean" // #nosec G101 -- test fixture; not a credential.

	k8s := newFakeK8s(t).Build()
	d, err := NewTransportDispatcher(k8s, ns)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}

	cr := makeMCPServer(teleportTransport(mcpApp, mcpSecretName), nil)
	_, err = d.MCPClientFor(context.Background(), cr)
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
	if !errors.Is(err, ErrSecretMissing) {
		t.Fatalf("expected ErrSecretMissing, got %v", err)
	}
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("expected *TransportError, got %T", err)
	}
	if te.Secret != mcpSecretName {
		t.Errorf("TransportError.Secret=%q want %q", te.Secret, mcpSecretName)
	}
	if te.AppName != mcpApp {
		t.Errorf("TransportError.AppName=%q want %q", te.AppName, mcpApp)
	}

	reason, _ := MapErrorToCondition(err)
	if reason != ReasonSecretMissing {
		t.Errorf("reason=%q want %q", reason, ReasonSecretMissing)
	}

	expectedLookup := `
# HELP muster_transport_lookup_total Number of CR-driven transport-dispatcher lookups, by outcome.
# TYPE muster_transport_lookup_total counter
muster_transport_lookup_total{result="secret_missing"} 1
`
	if err := testutil.CollectAndCompare(transportLookupTotal, strings.NewReader(expectedLookup)); err != nil {
		t.Fatalf("lookup metric mismatch: %v", err)
	}
	expectedLoad := `
# HELP muster_transport_secret_load_total Number of tbot-identity Secret load attempts, by secret name and outcome.
# TYPE muster_transport_secret_load_total counter
muster_transport_secret_load_total{result="error",secret="tbot-identity-mcp-glean"} 1
`
	if err := testutil.CollectAndCompare(transportSecretLoadTotal, strings.NewReader(expectedLoad)); err != nil {
		t.Fatalf("load metric mismatch: %v", err)
	}
}

// (e) — token-exchange secret invalid (exists but has malformed PEM) →
// ErrSecretInvalid + result="secret_invalid" surfacing on the
// TokenExchangeClientFor call. MCPClientFor remains successful.
func TestDispatcher_TokenExchangeSecretInvalid(t *testing.T) {
	resetMetricsForTest()
	t.Cleanup(resetMetricsForTest)

	const ns = "muster"
	const mcpApp = "mcp-kubernetes-glean"
	const dexApp = "dex-glean"
	const mcpSecretName = "tbot-identity-mcp-glean" // #nosec G101 -- test fixture; not a credential.
	const dexSecretName = "tbot-identity-tx-glean"  // #nosec G101 -- test fixture; not a credential.
	mcpSecret, _ := makeIdentitySecret(t, mcpSecretName, ns)
	// Malformed dex secret — exists but contents fail PEM/cert load.
	badDex := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: dexSecretName, Namespace: ns},
		Data: map[string][]byte{
			DefaultCertFile: []byte("not a real PEM"),
			DefaultKeyFile:  []byte("not a real key"),
			DefaultCAFile:   []byte("not a real CA"),
		},
	}

	k8s := newFakeK8s(t, mcpSecret, badDex).Build()
	d, err := NewTransportDispatcher(k8s, ns)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}

	cr := makeMCPServer(
		teleportTransport(mcpApp, mcpSecretName),
		teleportTransport(dexApp, dexSecretName),
	)

	if _, err := d.MCPClientFor(context.Background(), cr); err != nil {
		t.Fatalf("MCPClientFor unexpected error: %v", err)
	}

	_, err = d.TokenExchangeClientFor(context.Background(), cr)
	if err == nil {
		t.Fatal("expected error for invalid token-exchange secret")
	}
	if !errors.Is(err, ErrSecretInvalid) {
		t.Fatalf("expected ErrSecretInvalid, got %v", err)
	}
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("expected *TransportError, got %T", err)
	}
	if te.Secret != dexSecretName {
		t.Errorf("TransportError.Secret=%q want %q", te.Secret, dexSecretName)
	}
	if te.AppName != dexApp {
		t.Errorf("TransportError.AppName=%q want %q", te.AppName, dexApp)
	}

	reason, _ := MapErrorToCondition(err)
	if reason != ReasonSecretInvalid {
		t.Errorf("reason=%q want %q", reason, ReasonSecretInvalid)
	}
}

// Disallowed namespace at construction is rejected up-front (preserves the
// security.go allow-list constraint called out in the brief).
func TestDispatcher_RejectsDisallowedNamespace(t *testing.T) {
	k8s := newFakeK8s(t).Build()
	_, err := NewTransportDispatcher(k8s, "kube-system")
	if err == nil {
		t.Fatal("expected error when secretNamespace is outside the allow-list")
	}
}

// MapErrorToCondition is total — nil in, ("","") out.
func TestMapErrorToCondition_Nil(t *testing.T) {
	r, m := MapErrorToCondition(nil)
	if r != "" || m != "" {
		t.Errorf("MapErrorToCondition(nil)=(%q,%q) want empty", r, m)
	}
}
