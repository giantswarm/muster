package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/giantswarm/mcp-oauth/providers/dex"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/config"
)

// localhostCert mints a self-signed certificate valid for the DNS name
// "localhost", so a test server bound to 127.0.0.1 can be reached through a
// hostname. An issuer URL with an IP literal never reaches the transport:
// mcp-oauth rejects it in static validation, whatever AllowPrivateIP is set to.
func localhostCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "muster-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// startLabDex serves an OIDC discovery document over TLS on a loopback
// address, under a self-signed CA. It stands in for the kind quick start's lab
// Dex: a private IP plus a certificate that the system pool does not trust.
func startLabDex(t *testing.T) (issuerURL string, caPool *x509.CertPool) {
	t.Helper()

	cert, pool := localhostCert(t)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	issuer := "https://localhost:" + port

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 issuer,
			"authorization_endpoint": issuer + "/auth",
			"token_endpoint":         issuer + "/token",
			"jwks_uri":               issuer + "/keys",
		})
	}))
	require.NoError(t, server.Listener.Close())
	server.Listener = listener
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)

	return issuer, pool
}

// TestNewDexProviderConfig_LabDexInternalCA pins how the two knobs of the lab
// Dex path combine at the pinned mcp-oauth version: the --extra-ca-file pool
// reaches the OIDC discovery client through dex.Config.RootCAs, and since
// mcp-oauth v1.3.3 (giantswarm/mcp-oauth#556) every discovery client honors
// that pool — the SSRF-safe default client included. dex.allowPrivateIPOIDC
// only selects which client performs discovery; it no longer gates whether
// the CA pool applies.
//
// The private-IP guard itself is not covered here, and no knob lifts it: the
// block on private and loopback addresses lives in mcp-oauth's static issuer
// validation, which rejects an IP-literal issuer URL whatever AllowPrivateIP
// is set to.
func TestNewDexProviderConfig_LabDexInternalCA(t *testing.T) {
	issuer, caPool := startLabDex(t)

	newProvider := func(t *testing.T, allowPrivateIP bool, pool *x509.CertPool) error {
		t.Helper()
		cfg := config.OAuthServerConfig{
			Dex: config.DexConfig{
				IssuerURL:          issuer,
				ClientID:           "muster",
				ClientSecret:       "secret",
				AllowPrivateIPOIDC: allowPrivateIP,
			},
		}
		dexConfig := newDexProviderConfig(cfg, "https://muster.example.com/oauth/callback", pool, noAudiences)
		require.Equal(t, allowPrivateIP, dexConfig.AllowPrivateIP)
		require.Same(t, pool, dexConfig.RootCAs)

		_, err := dex.NewProvider(dexConfig)
		return err
	}

	t.Run("both knobs set completes discovery", func(t *testing.T) {
		require.NoError(t, newProvider(t, true, caPool))
	})

	t.Run("without the CA pool TLS verification fails", func(t *testing.T) {
		err := newProvider(t, true, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "certificate")
	})

	t.Run("without allowPrivateIPOIDC the CA pool is still honored", func(t *testing.T) {
		require.NoError(t, newProvider(t, false, caPool))
	})
}
