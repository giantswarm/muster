package credprovider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/proto/credproviderpb"
)

const (
	testGateway     = "spiffe://cluster.local/ns/agent-platform/sa/substrate-atenet-egress"
	testIssuer      = "https://github.com/apps/giantswarm-workspaces"
	testLoginIssuer = "https://github.com/apps/giantswarm-login"
	testGitURI      = "ate-secret://muster.giantswarm.io/github/git"
	testAPIURI      = "ate-secret://muster.giantswarm.io/github/api"
)

// testPKI is a CA and leaf certificates it signs, written as PEM files.
type testPKI struct {
	dir    string
	ca     *x509.Certificate
	caKey  *ecdsa.PrivateKey
	caFile string
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	p := &testPKI{dir: t.TempDir(), ca: ca, caKey: key}
	p.caFile = filepath.Join(p.dir, "ca.pem")
	require.NoError(t, os.WriteFile(p.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	return p
}

// leaf issues a certificate for dnsName (serving) or spiffeID (client) and
// returns it with the path of its PEM bundle (key and chain).
func (p *testPKI) leaf(t *testing.T, name, dnsName, spiffeID string) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	if dnsName != "" {
		tmpl.DNSNames = []string{dnsName}
	}
	if spiffeID != "" {
		u, err := url.Parse(spiffeID)
		require.NoError(t, err)
		tmpl.URIs = []*url.URL{u}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.ca, &key.PublicKey, p.caKey)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	bundle := append(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	path := filepath.Join(p.dir, name+".pem")
	require.NoError(t, os.WriteFile(path, bundle, 0o600))
	cert, err := tls.X509KeyPair(bundle, bundle)
	require.NoError(t, err)
	return cert, path
}

// fakeGrants holds each person's grants by issuer.
type fakeGrants map[string]map[string]*api.OAuthToken

func (g fakeGrants) source(_ context.Context, subject, issuer string) *api.OAuthToken {
	return g[subject][issuer]
}

func newTestProvider(t *testing.T, grants fakeGrants) (*Provider, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	p, err := NewProvider(Options{
		GatewayIdentity: testGateway,
		Releasable:      map[string]string{"github": testIssuer},
		BindingTTL:      time.Hour,
		Store:           store,
		Grants:          grants.source,
	})
	require.NoError(t, err)
	return p, store
}

// serveTLS serves p on loopback with the production TLS configuration and
// returns a client presenting the given certificate.
func serveTLS(t *testing.T, p *Provider, pki *testPKI, client tls.Certificate) credproviderpb.CredentialProviderClient {
	t.Helper()
	_, serverBundle := pki.leaf(t, "server", "muster.muster.svc", "")
	cfg, err := serverTLSConfig(TLSFiles{ServerCredBundle: serverBundle, ClientCAFile: pki.caFile}, p.GatewayIdentity())
	require.NoError(t, err)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := Serve(lis, credentials.NewTLS(cfg), p)
	t.Cleanup(func() { srv.Stop(context.Background()) })

	roots := x509.NewCertPool()
	roots.AddCert(pki.ca)
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   "muster.muster.svc",
		Certificates: []tls.Certificate{client},
	})))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return credproviderpb.NewCredentialProviderClient(conn)
}

func TestFetchSecretAnswersTheBoundPerson(t *testing.T) {
	grants := fakeGrants{"alice": {
		testIssuer:      {AccessToken: "ghu_alice", RefreshToken: "ghr_secret", ExpiresAt: time.Now().Add(8 * time.Hour)},
		testLoginIssuer: {AccessToken: "ghu_login"},
	}}
	p, store := newTestProvider(t, grants)
	pki := newTestPKI(t)
	gateway, _ := pki.leaf(t, "gateway", "", testGateway)
	client := serveTLS(t, p, pki, gateway)
	ctx := context.Background()
	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{"github"}}, time.Hour))

	resp, err := client.FetchSecret(ctx, &credproviderpb.FetchSecretRequest{Uri: testGitURI, ActorSpiffeId: testActor})
	require.NoError(t, err)
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("x-access-token:ghu_alice")), string(resp.GetOpaqueBytes()),
		"git gets the Basic credential the gateway prefixes with \"Basic \"")

	resp, err = client.FetchSecret(ctx, &credproviderpb.FetchSecretRequest{Uri: testAPIURI, ActorSpiffeId: testActor})
	require.NoError(t, err)
	assert.Equal(t, "ghu_alice", string(resp.GetOpaqueBytes()), "the API gets the bare token for \"Bearer \"")
	assert.NotContains(t, string(resp.GetOpaqueBytes()), "ghr_secret", "the refresh token never leaves muster")
}

func TestFetchSecretRefusals(t *testing.T) {
	grants := fakeGrants{
		"alice": {testIssuer: {AccessToken: "ghu_alice"}, testLoginIssuer: {AccessToken: "ghu_login"}},
		"bob":   {},
	}
	ctx := context.Background()
	other := "spiffe://substrate-actor.local/actor/kagent/session-b"
	unbound := "spiffe://substrate-actor.local/actor/kagent/session-c"
	cases := []struct {
		name  string
		uri   string
		actor string
		code  codes.Code
	}{
		{"an unbound actor", testGitURI, unbound, codes.PermissionDenied},
		{"a provider outside the binding", "ate-secret://muster.giantswarm.io/github/git", "spiffe://substrate-actor.local/actor/kagent/gitlab-only", codes.PermissionDenied},
		{"a provider off the releasable list (the login App)", "ate-secret://muster.giantswarm.io/login/api", testActor, codes.PermissionDenied},
		{"a person without the grant (disconnected)", testGitURI, other, codes.NotFound},
		{"another authority", "ate-secret://k8s.io/github/git", testActor, codes.InvalidArgument},
		{"an unknown use", "ate-secret://muster.giantswarm.io/github/token", testActor, codes.InvalidArgument},
		{"a non-actor identity", testGitURI, testGateway, codes.InvalidArgument},
	}

	p, store := newTestProvider(t, grants)
	pki := newTestPKI(t)
	gateway, _ := pki.leaf(t, "gateway", "", testGateway)
	client := serveTLS(t, p, pki, gateway)
	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Providers: []string{"github", "login"}}, time.Hour))
	require.NoError(t, store.Put(ctx, other, Binding{Subject: "bob", Providers: []string{"github"}}, time.Hour))
	require.NoError(t, store.Put(ctx, "spiffe://substrate-actor.local/actor/kagent/gitlab-only", Binding{Subject: "alice", Providers: []string{"gitlab"}}, time.Hour))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.FetchSecret(ctx, &credproviderpb.FetchSecretRequest{Uri: tc.uri, ActorSpiffeId: tc.actor})
			require.Error(t, err)
			assert.Equal(t, tc.code, status.Code(err), err.Error())
		})
	}

	t.Run("an unbound Session after unbind", func(t *testing.T) {
		_, err := client.FetchSecret(ctx, &credproviderpb.FetchSecretRequest{Uri: testGitURI, ActorSpiffeId: testActor})
		require.NoError(t, err)
		require.NoError(t, store.Delete(ctx, testActor, "alice"))
		_, err = client.FetchSecret(ctx, &credproviderpb.FetchSecretRequest{Uri: testGitURI, ActorSpiffeId: testActor})
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})
}

func TestFetchSecretRefusesCallersOtherThanTheGateway(t *testing.T) {
	p, store := newTestProvider(t, fakeGrants{"alice": {testIssuer: {AccessToken: "ghu_alice"}}})
	require.NoError(t, store.Put(context.Background(), testActor, Binding{Subject: "alice", Providers: []string{"github"}}, time.Hour))
	pki := newTestPKI(t)
	req := &credproviderpb.FetchSecretRequest{Uri: testGitURI, ActorSpiffeId: testActor}

	t.Run("another workload of the same trust bundle", func(t *testing.T) {
		sandbox, _ := pki.leaf(t, "sandbox", "", "spiffe://cluster.local/ns/kagent/sa/agent")
		_, err := serveTLS(t, p, pki, sandbox).FetchSecret(context.Background(), req)
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err), "the TLS handshake refuses it: %v", err)
	})
	t.Run("the gateway's identity from another CA", func(t *testing.T) {
		forged, _ := newTestPKI(t).leaf(t, "forged", "", testGateway)
		_, err := serveTLS(t, p, pki, forged).FetchSecret(context.Background(), req)
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err))
	})
	t.Run("no peer identity at all", func(t *testing.T) {
		_, err := p.FetchSecret(context.Background(), req)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})
}

func TestFetchSecretRestartsTheBindingTTL(t *testing.T) {
	p, store := newTestProvider(t, fakeGrants{"alice": {testIssuer: {AccessToken: "ghu_alice"}}})
	pki := newTestPKI(t)
	gateway, _ := pki.leaf(t, "gateway", "", testGateway)
	client := serveTLS(t, p, pki, gateway)
	now := time.Now()
	store.now = func() time.Time { return now }
	ctx := context.Background()
	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Providers: []string{"github"}}, time.Hour))

	now = now.Add(50 * time.Minute)
	_, err := client.FetchSecret(ctx, &credproviderpb.FetchSecretRequest{Uri: testGitURI, ActorSpiffeId: testActor})
	require.NoError(t, err)
	now = now.Add(50 * time.Minute)
	_, err = client.FetchSecret(ctx, &credproviderpb.FetchSecretRequest{Uri: testGitURI, ActorSpiffeId: testActor})
	require.NoError(t, err, "a used binding lives on")
	now = now.Add(61 * time.Minute)
	_, err = client.FetchSecret(ctx, &credproviderpb.FetchSecretRequest{Uri: testGitURI, ActorSpiffeId: testActor})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "an idle binding expires")
}

func TestNewProviderValidates(t *testing.T) {
	ok := Options{GatewayIdentity: testGateway, Releasable: map[string]string{"github": testIssuer}, Store: NewMemoryStore(), Grants: fakeGrants{}.source}
	_, err := NewProvider(ok)
	require.NoError(t, err)

	musterOnly := ok
	musterOnly.Releasable, musterOnly.MusterBearer = nil, true
	_, err = NewProvider(musterOnly)
	require.NoError(t, err, "the actor bearer alone needs nothing releasable")

	for name, mutate := range map[string]func(*Options){
		"no gateway":          func(o *Options) { o.GatewayIdentity = "" },
		"gateway not SPIFFE":  func(o *Options) { o.GatewayIdentity = "https://gateway" },
		"nothing releasable":  func(o *Options) { o.Releasable = nil },
		"provider with slash": func(o *Options) { o.Releasable = map[string]string{"a/b": testIssuer} },
		"provider w/o issuer": func(o *Options) { o.Releasable = map[string]string{"github": ""} },
		"muster is reserved":  func(o *Options) { o.Releasable = map[string]string{MusterProvider: testIssuer} },
		"no store":            func(o *Options) { o.Store = nil },
		"no grant source":     func(o *Options) { o.Grants = nil },
	} {
		t.Run(name, func(t *testing.T) {
			o := ok
			mutate(&o)
			_, err := NewProvider(o)
			assert.Error(t, err)
		})
	}
}

func TestListenRequiresTLSFiles(t *testing.T) {
	p, _ := newTestProvider(t, fakeGrants{})
	_, err := Listen(context.Background(), "127.0.0.1:0", TLSFiles{}, p)
	assert.Error(t, err)

	pki := newTestPKI(t)
	_, bundle := pki.leaf(t, "server", "muster.muster.svc", "")
	srv, err := Listen(context.Background(), "127.0.0.1:0", TLSFiles{ServerCredBundle: bundle, ClientCAFile: pki.caFile}, p)
	require.NoError(t, err)
	srv.Stop(context.Background())
}
