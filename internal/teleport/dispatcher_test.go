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

// makeMCPServer builds a minimal MCPServer with the requested transport.
func makeMCPServer(transport *v1alpha1.MCPServerTransport) *v1alpha1.MCPServer {
	return &v1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: v1alpha1.MCPServerSpec{
			Type:      "streamable-http",
			URL:       "https://example.test/mcp",
			Transport: transport,
		},
	}
}

// teleportTransport is a constructor shortcut for cleaner test cases.
func teleportTransport(cluster string) *v1alpha1.MCPServerTransport {
	return &v1alpha1.MCPServerTransport{
		Type: "teleport",
		Teleport: &v1alpha1.TeleportTransport{
			Cluster: cluster,
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

// tlsLeafSubject returns the leaf-cert Subject CN that the http.Client's
// underlying *http.Transport carries. Walks past the appNameTransport wrapper.
func tlsLeafSubject(t *testing.T, c *http.Client) string {
	t.Helper()
	cert := tlsLeafCert(t, c)
	return cert.Subject.CommonName
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
	var rt http.RoundTripper = c.Transport
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

// (e) — locks the <role>-<cluster> derivation convention. PLAN §6 TB-1/2/4.
func TestDispatcher_NameHelpers(t *testing.T) {
	cases := []struct {
		cluster                              string
		mcpApp, dexApp, mcpSecret, dexSecret string
	}{
		{"glean", "mcp-kubernetes-glean", "dex-glean", "tbot-identity-mcp-glean", "tbot-identity-tx-glean"},
		{"finch", "mcp-kubernetes-finch", "dex-finch", "tbot-identity-mcp-finch", "tbot-identity-tx-finch"},
	}
	for _, tc := range cases {
		if got := MCPAppName(tc.cluster); got != tc.mcpApp {
			t.Errorf("MCPAppName(%q)=%q want %q", tc.cluster, got, tc.mcpApp)
		}
		if got := DexAppName(tc.cluster); got != tc.dexApp {
			t.Errorf("DexAppName(%q)=%q want %q", tc.cluster, got, tc.dexApp)
		}
		if got := MCPSecretName(tc.cluster); got != tc.mcpSecret {
			t.Errorf("MCPSecretName(%q)=%q want %q", tc.cluster, got, tc.mcpSecret)
		}
		if got := DexSecretName(tc.cluster); got != tc.dexSecret {
			t.Errorf("DexSecretName(%q)=%q want %q", tc.cluster, got, tc.dexSecret)
		}
	}
}

// (a) — transport unset → plain client; result="none".
func TestDispatcher_TransportUnset(t *testing.T) {
	resetMetricsForTest()
	t.Cleanup(resetMetricsForTest)

	k8s := newFakeK8s(t).Build()
	d, err := NewTransportDispatcher(k8s, []string{"glean"}, "muster-system")
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}

	mcp, dex, err := d.ClientsFor(context.Background(), makeMCPServer(nil))
	if err != nil {
		t.Fatalf("ClientsFor: %v", err)
	}
	if mcp == nil {
		t.Fatal("expected non-nil mcp client for direct-HTTPS path")
	}
	if dex != nil {
		t.Fatal("expected nil dex client when transport is unset")
	}

	expected := `
# HELP muster_transport_lookup_total Number of CR-driven transport-dispatcher lookups, by outcome.
# TYPE muster_transport_lookup_total counter
muster_transport_lookup_total{result="none"} 1
`
	if err := testutil.CollectAndCompare(transportLookupTotal, strings.NewReader(expected)); err != nil {
		t.Fatalf("metric mismatch: %v", err)
	}
}

// (b) — cluster known + both secrets present → two configured clients.
func TestDispatcher_ResolvedBothSecrets(t *testing.T) {
	resetMetricsForTest()
	t.Cleanup(resetMetricsForTest)

	const cluster = "glean"
	const ns = "muster-system"
	mcpSecret, mcpCertPEM := makeIdentitySecret(t, MCPSecretName(cluster), ns)
	dexSecret, dexCertPEM := makeIdentitySecret(t, DexSecretName(cluster), ns)
	if string(mcpCertPEM) == string(dexCertPEM) {
		t.Fatal("test fixture: mcp and dex secrets accidentally got the same cert")
	}

	k8s := newFakeK8s(t, mcpSecret, dexSecret).Build()
	d, err := NewTransportDispatcher(k8s, []string{cluster, "finch"}, ns)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}

	mcp, dex, err := d.ClientsFor(context.Background(), makeMCPServer(teleportTransport(cluster)))
	if err != nil {
		t.Fatalf("ClientsFor: %v", err)
	}
	if mcp == nil || dex == nil {
		t.Fatalf("expected both clients, got mcp=%v dex=%v", mcp, dex)
	}
	if mcp == dex {
		t.Fatal("expected distinct clients (per Q4: 2 secrets per remote MC)")
	}

	// Each client is wrapped with appNameTransport; verify the Host header
	// was set to the derived app name on each.
	mcpHost := appNameFromTransport(t, mcp)
	if mcpHost != MCPAppName(cluster) {
		t.Errorf("mcp client Host=%q, want %q", mcpHost, MCPAppName(cluster))
	}
	dexHost := appNameFromTransport(t, dex)
	if dexHost != DexAppName(cluster) {
		t.Errorf("dex client Host=%q, want %q", dexHost, DexAppName(cluster))
	}

	// Each client's underlying TLS config carries the matching cert: extract
	// the leaf cert subject via the underlying transport.
	mcpSubject := tlsLeafSubject(t, mcp)
	dexSubject := tlsLeafSubject(t, dex)
	if mcpSubject == "" || dexSubject == "" {
		t.Fatal("expected non-empty leaf subjects on both clients")
	}
	// Subjects come from createTestCertificates' "Test Client" CN — both
	// fixtures share the CN, so we instead assert distinctness via cert
	// serial (regenerated per call).
	mcpSerial := tlsLeafSerial(t, mcp)
	dexSerial := tlsLeafSerial(t, dex)
	if mcpSerial == "" || dexSerial == "" {
		t.Fatal("expected non-empty leaf serials on both clients")
	}
	if mcpSerial == dexSerial {
		t.Errorf("mcp and dex clients carry the same cert serial %q — secrets not distinct", mcpSerial)
	}

	expectedLookup := `
# HELP muster_transport_lookup_total Number of CR-driven transport-dispatcher lookups, by outcome.
# TYPE muster_transport_lookup_total counter
muster_transport_lookup_total{result="resolved"} 1
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

// (c) — cluster=unknown → ErrClusterNotConfigured + result="cluster_unknown".
func TestDispatcher_ClusterUnknown(t *testing.T) {
	resetMetricsForTest()
	t.Cleanup(resetMetricsForTest)

	k8s := newFakeK8s(t).Build()
	d, err := NewTransportDispatcher(k8s, []string{"glean"}, "muster-system")
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}

	_, _, err = d.ClientsFor(context.Background(), makeMCPServer(teleportTransport("unknown")))
	if err == nil {
		t.Fatal("expected error for unknown cluster")
	}
	if !errors.Is(err, ErrClusterNotConfigured) {
		t.Fatalf("expected ErrClusterNotConfigured, got %v", err)
	}

	// Status condition mapping is wired (the helper TB-8 will call).
	reason, msg := MapErrorToCondition(err)
	if reason != ReasonClusterNotConfigured {
		t.Errorf("reason=%q want %q", reason, ReasonClusterNotConfigured)
	}
	if !strings.Contains(msg, `cluster "unknown" not configured`) {
		t.Errorf("message=%q does not mention cluster", msg)
	}

	expected := `
# HELP muster_transport_lookup_total Number of CR-driven transport-dispatcher lookups, by outcome.
# TYPE muster_transport_lookup_total counter
muster_transport_lookup_total{result="cluster_unknown"} 1
`
	if err := testutil.CollectAndCompare(transportLookupTotal, strings.NewReader(expected)); err != nil {
		t.Fatalf("metric mismatch: %v", err)
	}
}

// (d) — cluster known but mcp secret missing → ErrSecretMissing +
// result="secret_missing". Asserts the derived secret name is the one named
// in the error.
func TestDispatcher_SecretMissing(t *testing.T) {
	resetMetricsForTest()
	t.Cleanup(resetMetricsForTest)

	const cluster = "glean"
	const ns = "muster-system"
	// Only the dex secret exists; mcp secret is missing.
	dexSecret, _ := makeIdentitySecret(t, DexSecretName(cluster), ns)

	k8s := newFakeK8s(t, dexSecret).Build()
	d, err := NewTransportDispatcher(k8s, []string{cluster}, ns)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}

	_, _, err = d.ClientsFor(context.Background(), makeMCPServer(teleportTransport(cluster)))
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
	if te.Secret != MCPSecretName(cluster) {
		t.Errorf("TransportError.Secret=%q want %q", te.Secret, MCPSecretName(cluster))
	}
	if te.Cluster != cluster {
		t.Errorf("TransportError.Cluster=%q want %q", te.Cluster, cluster)
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

// Disallowed namespace at construction is rejected up-front (preserves the
// security.go allow-list constraint called out in the brief).
func TestDispatcher_RejectsDisallowedNamespace(t *testing.T) {
	k8s := newFakeK8s(t).Build()
	_, err := NewTransportDispatcher(k8s, []string{"glean"}, "kube-system")
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
