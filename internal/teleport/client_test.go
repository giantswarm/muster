package teleport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// createTestCertificates creates test TLS certificates in the given directory.
// Returns the certificate expiry time for verification.
func createTestCertificates(t *testing.T, dir string) time.Time {
	t.Helper()

	// Generate CA key and certificate
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate CA key: %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("Failed to create CA certificate: %v", err)
	}

	// Write CA certificate
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})
	if err := os.WriteFile(filepath.Join(dir, DefaultCAFile), caCertPEM, 0600); err != nil {
		t.Fatalf("Failed to write CA certificate: %v", err)
	}

	// Generate client key and certificate
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate client key: %v", err)
	}

	notAfter := time.Now().Add(24 * time.Hour)
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Test Client"},
		NotBefore:    time.Now(),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	// Parse CA cert for signing
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		t.Fatalf("Failed to parse CA certificate: %v", err)
	}

	clientCertDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("Failed to create client certificate: %v", err)
	}

	// Write client certificate
	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCertDER})
	if err := os.WriteFile(filepath.Join(dir, DefaultCertFile), clientCertPEM, 0600); err != nil {
		t.Fatalf("Failed to write client certificate: %v", err)
	}

	// Write client key
	clientKeyBytes, err := x509.MarshalECPrivateKey(clientKey)
	if err != nil {
		t.Fatalf("Failed to marshal client key: %v", err)
	}
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: clientKeyBytes})
	if err := os.WriteFile(filepath.Join(dir, DefaultKeyFile), clientKeyPEM, 0600); err != nil {
		t.Fatalf("Failed to write client key: %v", err)
	}

	return notAfter
}

func TestNewClientProvider(t *testing.T) {
	// Create temporary directory with test certificates
	dir := t.TempDir()
	createTestCertificates(t, dir)

	config := TeleportConfig{
		IdentityDir: dir,
	}

	provider, err := NewClientProvider(config)
	if err != nil {
		t.Fatalf("NewClientProvider failed: %v", err)
	}
	defer provider.Close()

	if !provider.IsLoaded() {
		t.Error("Expected certificates to be loaded")
	}

	status := provider.GetStatus()
	if !status.Loaded {
		t.Error("Expected status.Loaded to be true")
	}
	if status.LastError != nil {
		t.Errorf("Expected no error, got: %v", status.LastError)
	}
}

func TestNewClientProvider_MissingCertificates(t *testing.T) {
	// Create empty temporary directory
	dir := t.TempDir()

	config := TeleportConfig{
		IdentityDir: dir,
	}

	// Should create provider but certificates won't be loaded
	provider, err := NewClientProvider(config)
	if err != nil {
		t.Fatalf("NewClientProvider failed: %v", err)
	}
	defer provider.Close()

	if provider.IsLoaded() {
		t.Error("Expected certificates to NOT be loaded")
	}

	status := provider.GetStatus()
	if status.Loaded {
		t.Error("Expected status.Loaded to be false")
	}
}

func TestClientProvider_GetHTTPClient(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	config := TeleportConfig{
		IdentityDir: dir,
	}

	provider, err := NewClientProvider(config)
	if err != nil {
		t.Fatalf("NewClientProvider failed: %v", err)
	}
	defer provider.Close()

	// Get HTTP client
	client, err := provider.GetHTTPClient()
	if err != nil {
		t.Fatalf("GetHTTPClient failed: %v", err)
	}

	if client == nil {
		t.Fatal("Expected non-nil HTTP client")
	}

	// Verify transport has TLS config
	httpTransport, ok := client.Transport.(*http.Transport)
	if ok && httpTransport != nil {
		if httpTransport.TLSClientConfig == nil {
			t.Error("Expected transport to have TLSClientConfig")
		}
	}

	// Getting the client again should return the cached instance
	client2, err := provider.GetHTTPClient()
	if err != nil {
		t.Fatalf("Second GetHTTPClient failed: %v", err)
	}

	if client != client2 {
		t.Error("Expected same cached HTTP client instance")
	}
}

func TestClientProvider_GetHTTPClient_NotLoaded(t *testing.T) {
	dir := t.TempDir()
	// Don't create certificates

	config := TeleportConfig{
		IdentityDir: dir,
	}

	provider, err := NewClientProvider(config)
	if err != nil {
		t.Fatalf("NewClientProvider failed: %v", err)
	}
	defer provider.Close()

	// Getting HTTP client should fail when certs not available
	_, err = provider.GetHTTPClient()
	if err == nil {
		t.Error("Expected error when certificates not loaded")
	}
}

func TestClientProvider_GetTLSConfig(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	config := TeleportConfig{
		IdentityDir: dir,
	}

	provider, err := NewClientProvider(config)
	if err != nil {
		t.Fatalf("NewClientProvider failed: %v", err)
	}
	defer provider.Close()

	tlsConfig, err := provider.GetTLSConfig()
	if err != nil {
		t.Fatalf("GetTLSConfig failed: %v", err)
	}

	if tlsConfig == nil {
		t.Fatal("Expected non-nil TLS config")
	}

	if len(tlsConfig.Certificates) != 1 {
		t.Errorf("Expected 1 certificate, got %d", len(tlsConfig.Certificates))
	}

	if tlsConfig.RootCAs == nil {
		t.Error("Expected non-nil RootCAs")
	}
}

func TestClientProvider_GetHTTPTransport(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	config := TeleportConfig{
		IdentityDir: dir,
	}

	provider, err := NewClientProvider(config)
	if err != nil {
		t.Fatalf("NewClientProvider failed: %v", err)
	}
	defer provider.Close()

	transport, err := provider.GetHTTPTransport()
	if err != nil {
		t.Fatalf("GetHTTPTransport failed: %v", err)
	}

	if transport == nil {
		t.Fatal("Expected non-nil transport")
	}

	if transport.TLSClientConfig == nil {
		t.Error("Expected non-nil TLSClientConfig")
	}
}

func TestClientProvider_Reload(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	config := TeleportConfig{
		IdentityDir: dir,
	}

	provider, err := NewClientProvider(config)
	if err != nil {
		t.Fatalf("NewClientProvider failed: %v", err)
	}
	defer provider.Close()

	initialLoadTime := provider.GetStatus().LastLoaded

	// Wait a bit and reload
	time.Sleep(10 * time.Millisecond)

	if err := provider.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	status := provider.GetStatus()
	if !status.LastLoaded.After(initialLoadTime) {
		t.Error("Expected LastLoaded to be updated after reload")
	}
}

func TestClientProvider_OnReload(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	config := TeleportConfig{
		IdentityDir: dir,
	}

	provider, err := NewClientProvider(config)
	if err != nil {
		t.Fatalf("NewClientProvider failed: %v", err)
	}
	defer provider.Close()

	callbackCalled := false
	provider.OnReload(func(config *tls.Config, err error) {
		callbackCalled = true
		if err != nil {
			t.Errorf("Unexpected error in callback: %v", err)
		}
		if config == nil {
			t.Error("Expected non-nil TLS config in callback")
		}
	})

	if err := provider.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	if !callbackCalled {
		t.Error("Expected callback to be called")
	}
}

func TestClientProvider_IsExpiringSoon(t *testing.T) {
	dir := t.TempDir()
	expiryTime := createTestCertificates(t, dir)

	config := TeleportConfig{
		IdentityDir: dir,
	}

	provider, err := NewClientProvider(config)
	if err != nil {
		t.Fatalf("NewClientProvider failed: %v", err)
	}
	defer provider.Close()

	// Certificate expires in 24 hours, so 48 hour threshold should trigger
	if !provider.IsExpiringSoon(48 * time.Hour) {
		t.Error("Expected certificate to be expiring soon with 48h threshold")
	}

	// 1 hour threshold should not trigger
	if provider.IsExpiringSoon(1 * time.Hour) {
		t.Error("Expected certificate to NOT be expiring soon with 1h threshold")
	}

	// Verify the expiry time is set (exact match is tricky due to timezone differences)
	status := provider.GetStatus()
	if status.ExpiresAt == nil {
		t.Fatal("Expected ExpiresAt to be set")
	}
	// Verify it's approximately 24 hours from now (within a minute tolerance)
	expectedExpiry := expiryTime.UTC().Truncate(time.Minute)
	actualExpiry := status.ExpiresAt.UTC().Truncate(time.Minute)
	if !expectedExpiry.Equal(actualExpiry) {
		t.Errorf("Expected ExpiresAt to be approximately %v, got %v", expectedExpiry, actualExpiry)
	}
}

func TestClientProvider_CustomFileNames(t *testing.T) {
	dir := t.TempDir()

	// Create certificates with custom file names
	customCertFile := "client.pem"
	customKeyFile := "client-key.pem"
	customCAFile := "root-ca.pem"

	// Generate test certificates
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caCertDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caCertDER)
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})
	_ = os.WriteFile(filepath.Join(dir, customCAFile), caCertPEM, 0600)

	clientKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Test Client"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientCertDER, _ := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCertDER})
	_ = os.WriteFile(filepath.Join(dir, customCertFile), clientCertPEM, 0600)

	clientKeyBytes, _ := x509.MarshalECPrivateKey(clientKey)
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: clientKeyBytes})
	_ = os.WriteFile(filepath.Join(dir, customKeyFile), clientKeyPEM, 0600)

	config := TeleportConfig{
		IdentityDir: dir,
		CertFile:    customCertFile,
		KeyFile:     customKeyFile,
		CAFile:      customCAFile,
	}

	provider, err := NewClientProvider(config)
	if err != nil {
		t.Fatalf("NewClientProvider failed: %v", err)
	}
	defer provider.Close()

	if !provider.IsLoaded() {
		t.Error("Expected certificates to be loaded with custom file names")
	}

	status := provider.GetStatus()
	if status.CertPath != filepath.Join(dir, customCertFile) {
		t.Errorf("Expected CertPath to be %s, got %s", filepath.Join(dir, customCertFile), status.CertPath)
	}
}

func TestClientProvider_Close(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	config := TeleportConfig{
		IdentityDir: dir,
	}

	provider, err := NewClientProvider(config)
	if err != nil {
		t.Fatalf("NewClientProvider failed: %v", err)
	}

	// Close should not error
	if err := provider.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Closing again should be safe
	if err := provider.Close(); err != nil {
		t.Errorf("Second Close failed: %v", err)
	}
}

// createTestCertificateData creates test certificate data in memory.
// Returns CertificateData for in-memory provider testing.
func createTestCertificateData(t *testing.T) CertificateData {
	t.Helper()

	// Generate CA key and certificate
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate CA key: %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("Failed to create CA certificate: %v", err)
	}

	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})

	// Parse CA cert for signing client cert
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		t.Fatalf("Failed to parse CA certificate: %v", err)
	}

	// Generate client key and certificate
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate client key: %v", err)
	}

	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Test Client"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	clientCertDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("Failed to create client certificate: %v", err)
	}

	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCertDER})

	clientKeyBytes, err := x509.MarshalECPrivateKey(clientKey)
	if err != nil {
		t.Fatalf("Failed to marshal client key: %v", err)
	}
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: clientKeyBytes})

	return CertificateData{
		CertPEM: clientCertPEM,
		KeyPEM:  clientKeyPEM,
		CAPEM:   caCertPEM,
	}
}

func TestNewClientProviderFromMemory(t *testing.T) {
	certData := createTestCertificateData(t)

	provider, err := NewClientProviderFromMemory(certData)
	if err != nil {
		t.Fatalf("NewClientProviderFromMemory failed: %v", err)
	}
	defer provider.Close()

	if !provider.IsLoaded() {
		t.Error("Expected certificates to be loaded")
	}

	status := provider.GetStatus()
	if !status.Loaded {
		t.Error("Expected status.Loaded to be true")
	}
	if status.CertPath != "(in-memory)" {
		t.Errorf("Expected CertPath to be '(in-memory)', got %s", status.CertPath)
	}
}

func TestNewClientProviderFromMemory_GetHTTPClient(t *testing.T) {
	certData := createTestCertificateData(t)

	provider, err := NewClientProviderFromMemory(certData)
	if err != nil {
		t.Fatalf("NewClientProviderFromMemory failed: %v", err)
	}
	defer provider.Close()

	client, err := provider.GetHTTPClient()
	if err != nil {
		t.Fatalf("GetHTTPClient failed: %v", err)
	}

	if client == nil {
		t.Fatal("Expected non-nil HTTP client")
	}

	// Verify transport has TLS config
	httpTransport, ok := client.Transport.(*http.Transport)
	if ok && httpTransport != nil {
		if httpTransport.TLSClientConfig == nil {
			t.Error("Expected transport to have TLSClientConfig")
		}
	}
}

func TestNewClientProviderFromMemory_EmptyCert(t *testing.T) {
	certData := CertificateData{
		CertPEM: []byte{},
		KeyPEM:  []byte("key"),
		CAPEM:   []byte("ca"),
	}

	_, err := NewClientProviderFromMemory(certData)
	if err == nil {
		t.Error("Expected error for empty certificate")
	}
}

func TestNewClientProviderFromMemory_EmptyKey(t *testing.T) {
	certData := CertificateData{
		CertPEM: []byte("cert"),
		KeyPEM:  []byte{},
		CAPEM:   []byte("ca"),
	}

	_, err := NewClientProviderFromMemory(certData)
	if err == nil {
		t.Error("Expected error for empty key")
	}
}

func TestNewClientProviderFromMemory_EmptyCA(t *testing.T) {
	certData := CertificateData{
		CertPEM: []byte("cert"),
		KeyPEM:  []byte("key"),
		CAPEM:   []byte{},
	}

	_, err := NewClientProviderFromMemory(certData)
	if err == nil {
		t.Error("Expected error for empty CA")
	}
}

func TestNewClientProviderFromMemory_InvalidCert(t *testing.T) {
	certData := CertificateData{
		CertPEM: []byte("invalid cert data"),
		KeyPEM:  []byte("invalid key data"),
		CAPEM:   []byte("invalid ca data"),
	}

	_, err := NewClientProviderFromMemory(certData)
	if err == nil {
		t.Error("Expected error for invalid certificate data")
	}
}

func TestNewClientProviderFromMemory_GetTLSConfig(t *testing.T) {
	certData := createTestCertificateData(t)

	provider, err := NewClientProviderFromMemory(certData)
	if err != nil {
		t.Fatalf("NewClientProviderFromMemory failed: %v", err)
	}
	defer provider.Close()

	tlsConfig, err := provider.GetTLSConfig()
	if err != nil {
		t.Fatalf("GetTLSConfig failed: %v", err)
	}

	if tlsConfig == nil {
		t.Fatal("Expected non-nil TLS config")
	}

	if len(tlsConfig.Certificates) != 1 {
		t.Errorf("Expected 1 certificate, got %d", len(tlsConfig.Certificates))
	}

	if tlsConfig.RootCAs == nil {
		t.Error("Expected non-nil RootCAs")
	}

	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("Expected MinVersion TLS 1.2, got %d", tlsConfig.MinVersion)
	}
}
