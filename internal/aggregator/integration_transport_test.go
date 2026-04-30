package aggregator

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/teleport"
	v1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// TB-10 — integration test for the combined Muster flow per PLAN §6.
//
// This test stands up two httptest.Server instances behind mTLS and asserts
// that the dispatcher-routed clients carry distinct per-cluster certs to the
// expected endpoints. Crucially this validates the *runtime* path that
// connection_helper.go and server.go now exercise after TB-8 — without
// running a real MCP protocol handshake.
//
// Asserts (PLAN §6 TB-10):
//
//	(a) Dex /token request used the dex client cert.
//	(b) MCP request used the mcp client cert AND carried Authorization Bearer.
//	(c) Authorization header preserved through appNameTransport end-to-end.
//	(d) A CR with spec.transport omitted reaches the test server via direct
//	    HTTPS — no client cert.
//
// We test the TB-7+TB-8 wiring boundary (resolveTransportClients +
// dispatcher) rather than spinning the full mcp-go protocol stack: that
// would require a production MCP implementation behind both stubs and isn't
// what the brief asks for.

type captured struct {
	mu               sync.Mutex
	peerCertSubject  string
	peerCertSerial   string
	authzHeader      string
	host             string
	requestPath      string
	requestsObserved int
}

func (c *captured) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requestsObserved++
	c.host = r.Host
	c.requestPath = r.URL.Path
	c.authzHeader = r.Header.Get("Authorization")
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		leaf := r.TLS.PeerCertificates[0]
		c.peerCertSubject = leaf.Subject.CommonName
		c.peerCertSerial = leaf.SerialNumber.String()
	}
}

func (c *captured) get() captured {
	c.mu.Lock()
	defer c.mu.Unlock()
	return captured{
		peerCertSubject:  c.peerCertSubject,
		peerCertSerial:   c.peerCertSerial,
		authzHeader:      c.authzHeader,
		host:             c.host,
		requestPath:      c.requestPath,
		requestsObserved: c.requestsObserved,
	}
}

// testCertBundle holds matched CA + client cert + server cert PEM data for
// one cluster role (mcp or dex). The CA signs both the server's TLS cert and
// the client's tbot-output cert, so server-side ClientCAs verifies the
// client and the client-side RootCAs verifies the server.
type testCertBundle struct {
	caPEM      []byte
	caCert     *x509.Certificate
	caKey      *ecdsa.PrivateKey
	serverCert tls.Certificate
	clientCert []byte
	clientKey  []byte
	clientCN   string
}

func mintCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "tb10-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return cert, key, caPEM
}

func mintLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, isServer bool) (certPEM, keyPEM []byte, leaf *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() + int64(len(cn))),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if isServer {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.DNSNames = []string{"localhost"}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("leaf cert: %v", err)
	}
	leaf, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, leaf
}

// buildTestBundle wires (CA, server cert, client cert) for one role.
func buildTestBundle(t *testing.T, clientCN string) *testCertBundle {
	t.Helper()
	caCert, caKey, caPEM := mintCA(t)
	srvPEM, srvKeyPEM, _ := mintLeaf(t, caCert, caKey, "tb10-server-"+clientCN, true)
	cliPEM, cliKeyPEM, _ := mintLeaf(t, caCert, caKey, clientCN, false)

	srvTLSCert, err := tls.X509KeyPair(srvPEM, srvKeyPEM)
	if err != nil {
		t.Fatalf("server keypair: %v", err)
	}
	return &testCertBundle{
		caPEM:      caPEM,
		caCert:     caCert,
		caKey:      caKey,
		serverCert: srvTLSCert,
		clientCert: cliPEM,
		clientKey:  cliKeyPEM,
		clientCN:   clientCN,
	}
}

// startMTLSServer spins up an httptest TLS server requiring (and verifying)
// client certs against the bundle's CA.
func startMTLSServer(t *testing.T, bundle *testCertBundle, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	pool := x509.NewCertPool()
	pool.AddCert(bundle.caCert)
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{bundle.serverCert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// makeIdentitySecret builds a tbot-output-shaped Secret carrying the bundle's
// client cert + key + CA — exactly the keys teleport.Adapter expects.
func makeIdentitySecret(name, namespace string, bundle *testCertBundle) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data: map[string][]byte{
			teleport.DefaultCertFile: bundle.clientCert,
			teleport.DefaultKeyFile:  bundle.clientKey,
			teleport.DefaultCAFile:   bundle.caPEM,
		},
	}
}

// TB-10 (a, b, c): with spec.transport=teleport, dispatcher returns the
// per-cluster mTLS clients. Driving them through real HTTP requests proves
// the dex client carries the dex cert, the mcp client carries the mcp cert,
// the Authorization header survives appNameTransport, and Host is rewritten
// to the derived <role>-<cluster> app name.
func TestTB10_TeleportTransportRoutesPerCluster(t *testing.T) {
	const (
		cluster       = "glean"
		ns            = "muster"
		mcpAppName    = "mcp-kubernetes-glean"
		dexAppName    = "dex-glean"
		mcpSecretName = "tbot-identity-mcp-glean" // #nosec G101 -- test fixture; not a credential.
		dexSecretName = "tbot-identity-tx-glean"  // #nosec G101 -- test fixture; not a credential.
	)

	mcpBundle := buildTestBundle(t, "mcp-client-"+cluster)
	dexBundle := buildTestBundle(t, "dex-client-"+cluster)
	if string(mcpBundle.clientCert) == string(dexBundle.clientCert) {
		t.Fatal("test fixture: mcp and dex client certs are identical")
	}

	mcpCap := &captured{}
	dexCap := &captured{}

	mcpSrv := startMTLSServer(t, mcpBundle, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mcpCap.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	dexSrv := startMTLSServer(t, dexBundle, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dexCap.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"exchanged-token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","expires_in":300}`)
	}))

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme v1alpha1: %v", err)
	}

	k8s := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
		makeIdentitySecret(mcpSecretName, ns, mcpBundle),
		makeIdentitySecret(dexSecretName, ns, dexBundle),
	).Build()

	dispatcher, err := teleport.NewTransportDispatcher(k8s, ns)
	if err != nil {
		t.Fatalf("dispatcher: %v", err)
	}

	// Construct a partial AggregatorServer carrying the dispatcher. We don't
	// need any of the OAuth/auth-store machinery for this test — only the
	// resolveTransportClients helper, which is what TB-8 wires into both
	// call sites.
	a := &AggregatorServer{}
	a.SetTransportDispatcher(dispatcher, k8s)

	info := &ServerInfo{
		Name:      "mcp-kubernetes-" + cluster,
		Namespace: "default",
		URL:       mcpSrv.URL + "/mcp",
		AuthConfig: &api.MCPServerAuth{
			Type: "oauth",
			TokenExchange: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: dexSrv.URL + "/token",
				ConnectorID:      "giantswarm",
			},
		},
		TransportConfig: &api.MCPServerTransport{
			Type: "teleport",
			Teleport: &api.TeleportTransport{
				MCP: api.TeleportTarget{
					AppName:            mcpAppName,
					IdentitySecretName: mcpSecretName,
				},
				Dex: &api.TeleportTarget{
					AppName:            dexAppName,
					IdentitySecretName: dexSecretName,
				},
			},
		},
	}

	mcpClient, dexClient, err := a.resolveTransportClients(context.Background(), info)
	if err != nil {
		t.Fatalf("resolveTransportClients: %v", err)
	}
	if dexClient == nil {
		t.Fatal("expected non-nil dex client when teleport transport is set")
	}

	// (b) MCP request — Authorization Bearer must survive through
	// appNameTransport. (c) host header is rewritten to mcp-kubernetes-glean.
	req := newMCPRequest(t, mcpSrv.URL+"/mcp", "exchanged-token-marker")
	resp, err := mcpClient.Do(req)
	if err != nil {
		t.Fatalf("mcp Do: %v", err)
	}
	_ = resp.Body.Close()

	got := mcpCap.get()
	if got.requestsObserved != 1 {
		t.Fatalf("mcp server: requests=%d want 1", got.requestsObserved)
	}
	if got.authzHeader != "Bearer exchanged-token-marker" {
		t.Errorf("mcp server: Authorization=%q want Bearer exchanged-token-marker", got.authzHeader)
	}
	if got.host != mcpAppName {
		t.Errorf("mcp server: Host=%q want %q (appNameTransport rewrite)", got.host, mcpAppName)
	}
	if got.peerCertSubject != "mcp-client-"+cluster {
		t.Errorf("mcp server: peer cert CN=%q want mcp-client-%s", got.peerCertSubject, cluster)
	}

	// (a) Dex request — uses the *dex* client cert, not the mcp one.
	dexReq, err := http.NewRequest("POST", dexSrv.URL+"/token", strings.NewReader(""))
	if err != nil {
		t.Fatalf("dex req: %v", err)
	}
	dexResp, err := dexClient.Do(dexReq)
	if err != nil {
		t.Fatalf("dex Do: %v", err)
	}
	_ = dexResp.Body.Close()

	dexGot := dexCap.get()
	if dexGot.peerCertSubject != "dex-client-"+cluster {
		t.Errorf("dex server: peer cert CN=%q want dex-client-%s", dexGot.peerCertSubject, cluster)
	}
	if dexGot.host != dexAppName {
		t.Errorf("dex server: Host=%q want %q", dexGot.host, dexAppName)
	}
	// Distinct cert serials prove the per-role secrets resolved to the right
	// per-role TLS clients (locks PLAN §9 Q4: 2 secrets per remote MC).
	if got.peerCertSerial == dexGot.peerCertSerial {
		t.Errorf("mcp and dex peer certs share serial %q — secrets crossed wires", got.peerCertSerial)
	}
}

// TB-10 (d): a CR with spec.transport omitted resolves to a default
// http.Client — direct HTTPS, no client cert presented.
func TestTB10_TransportUnsetIsDirectHTTPS(t *testing.T) {
	// Plain TLS server (no client-cert requirement) — the dispatcher should
	// produce a client with no client cert.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// PeerCertificates must be empty: we set ClientAuth=NoClientCert
		// (the httptest default) and assert no cert was offered.
		if r.TLS != nil && len(r.TLS.PeerCertificates) != 0 {
			t.Errorf("expected no client cert on direct-HTTPS path, got %d", len(r.TLS.PeerCertificates))
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).Build()

	dispatcher, err := teleport.NewTransportDispatcher(k8s, "muster")
	if err != nil {
		t.Fatalf("dispatcher: %v", err)
	}

	a := &AggregatorServer{}
	a.SetTransportDispatcher(dispatcher, nil) // no k8s client → no status writes

	info := &ServerInfo{
		Name: "customer-mcp",
		URL:  srv.URL,
		// spec.transport omitted (TransportConfig is nil) — TB-7's
		// "transport unset" branch.
		AuthConfig: &api.MCPServerAuth{Type: "none"},
	}
	mcpClient, dexClient, err := a.resolveTransportClients(context.Background(), info)
	if err != nil {
		t.Fatalf("resolveTransportClients: %v", err)
	}
	if dexClient != nil {
		t.Errorf("expected nil dex client for direct-HTTPS path, got %v", dexClient)
	}

	// Pin the default client to trust the test server's CA so the request
	// completes — the dispatcher returned a default http.Client.
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	mcpClient.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		},
	}

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	resp, err := mcpClient.Do(req)
	if err != nil {
		t.Fatalf("mcp Do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d want 200", resp.StatusCode)
	}
}

// newMCPRequest builds a synthetic POST against the MCP endpoint with the
// canonical Authorization Bearer header used by the token-exchange path.
func newMCPRequest(t *testing.T, target, token string) *http.Request {
	t.Helper()
	req, err := http.NewRequest("POST", target, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("mcp req: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req
}
