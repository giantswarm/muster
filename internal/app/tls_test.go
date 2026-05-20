package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// generateSelfSignedCert mints an in-memory leaf certificate signed by itself,
// used as a stand-in for an internal CA. Returns the leaf certificate (DER-
// encoded for tls.Certificate), the matching private key, and the PEM-encoded
// CA bundle (which is just the leaf, since it self-signs).
func generateSelfSignedCert(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "muster-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	tlsCert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}
	return tlsCert, certPEM
}

// TestInstallExtraCAFile verifies that, after installExtraCAFile runs,
// http.DefaultClient successfully validates a TLS server certificate signed
// by the supplied CA — proving the augmented pool is the default for
// http.DefaultTransport.
func TestInstallExtraCAFile(t *testing.T) {
	// Snapshot DefaultTransport so other tests aren't affected.
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })

	tlsCert, caPEM := generateSelfSignedCert(t)

	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write ca pem: %v", err)
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	server.StartTLS()
	defer server.Close()

	// Sanity: without the augmented pool, the default client must reject the
	// server's self-signed cert. (httptest.NewTLSServer would set a per-server
	// pool on its client; we want the *default* client behavior.)
	if _, err := http.Get(server.URL); err == nil {
		t.Fatal("expected TLS verify failure before installExtraCAFile, got success")
	}

	if err := installExtraCAFile(caPath); err != nil {
		t.Fatalf("installExtraCAFile: %v", err)
	}

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("after install, http.Get: %v", err)
	}
	_ = resp.Body.Close()
}

func TestInstallExtraCAFile_MissingFile(t *testing.T) {
	if err := installExtraCAFile(filepath.Join(t.TempDir(), "nope.pem")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestInstallExtraCAFile_NoCerts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.pem")
	if err := os.WriteFile(path, []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := installExtraCAFile(path); err == nil {
		t.Fatal("expected error for non-PEM file")
	}
}
