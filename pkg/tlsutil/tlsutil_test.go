package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeCAFile(t *testing.T) (path string, caPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "tlsutil-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	path = filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, caPEM, 0o600))
	return path, caPEM
}

// TestLoadCAPool_KeepsSystemRoots pins that the pool is the system roots plus
// the operator's CA: the mcp-oauth clients it is handed to (JWKS, CIMD) use
// it in place of the system roots, so public endpoints must stay trusted.
func TestLoadCAPool_KeepsSystemRoots(t *testing.T) {
	path, caPEM := writeCAFile(t)

	got, err := LoadCAPool(path)
	require.NoError(t, err)

	want, err := x509.SystemCertPool()
	require.NoError(t, err)
	require.True(t, want.AppendCertsFromPEM(caPEM))
	require.True(t, want.Equal(got), "pool must be the system roots plus the extra CA")
}

func TestLoadCAPool_Errors(t *testing.T) {
	_, err := LoadCAPool(filepath.Join(t.TempDir(), "missing.pem"))
	require.ErrorContains(t, err, "read extra CA file")

	empty := filepath.Join(t.TempDir(), "empty.pem")
	require.NoError(t, os.WriteFile(empty, []byte("not a certificate"), 0o600))
	_, err = LoadCAPool(empty)
	require.ErrorContains(t, err, "no PEM certificates parsed")
}
