package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writePEM(t *testing.T, blockType string, der []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key.pem")
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}))
	require.NoError(t, f.Close())
	return path
}

func TestLoadSigningKey_ECp256(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	path := writePEM(t, "EC PRIVATE KEY", der)

	signer, kid, alg, err := loadSigningKey(path)
	require.NoError(t, err)
	require.NotNil(t, signer)
	require.NotEmpty(t, kid)
	require.Equal(t, "ES256", alg)
}

func TestLoadSigningKey_PKCS8_EC(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	path := writePEM(t, "PRIVATE KEY", der)

	_, kid, alg, err := loadSigningKey(path)
	require.NoError(t, err)
	require.NotEmpty(t, kid)
	require.Equal(t, "ES256", alg)
}

func TestLoadSigningKey_RSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	path := writePEM(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))

	_, kid, alg, err := loadSigningKey(path)
	require.NoError(t, err)
	require.NotEmpty(t, kid)
	require.Equal(t, "RS256", alg)
}

func TestLoadSigningKey_WrongCurve(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	path := writePEM(t, "EC PRIVATE KEY", der)

	_, _, _, err = loadSigningKey(path)
	require.ErrorContains(t, err, "P-256")
}

func TestLoadSigningKey_PKCS8_WrongCurve(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	path := writePEM(t, "PRIVATE KEY", der)

	_, _, _, err = loadSigningKey(path)
	require.ErrorContains(t, err, "P-256")
}

func TestLoadSigningKey_MissingFile(t *testing.T) {
	_, _, _, err := loadSigningKey("/nonexistent/key.pem")
	require.ErrorContains(t, err, "reading JWT signing key")
}

func TestLoadSigningKey_InvalidPEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pem")
	require.NoError(t, os.WriteFile(path, []byte("not pem data"), 0600))

	_, _, _, err := loadSigningKey(path)
	require.ErrorContains(t, err, "no valid PEM block")
}

func TestLoadSigningKey_KidIsDeterministic(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	path := writePEM(t, "EC PRIVATE KEY", der)

	_, kid1, _, err := loadSigningKey(path)
	require.NoError(t, err)
	_, kid2, _, err := loadSigningKey(path)
	require.NoError(t, err)
	require.Equal(t, kid1, kid2)
}
