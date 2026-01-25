package teleport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"muster/internal/api"
)

// testCertTemplate defines parameters for creating test certificates.
type testCertTemplate struct {
	commonName string
	isCA       bool
}

// generateECKey generates an ECDSA key for testing.
func generateECKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// createTestCert creates a test certificate.
func createTestCert(template *testCertTemplate, parentDER []byte, key *ecdsa.PrivateKey, signingKey *ecdsa.PrivateKey) ([]byte, error) {
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: template.commonName},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}

	if template.isCA {
		cert.IsCA = true
		cert.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
		cert.BasicConstraintsValid = true
	} else {
		cert.KeyUsage = x509.KeyUsageDigitalSignature
		cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}

	var parent *x509.Certificate
	if parentDER != nil {
		var err error
		parent, err = x509.ParseCertificate(parentDER)
		if err != nil {
			return nil, err
		}
	} else {
		parent = cert // Self-signed
	}

	return x509.CreateCertificate(rand.Reader, cert, parent, &key.PublicKey, signingKey)
}

// marshalECKey marshals an ECDSA private key.
func marshalECKey(key *ecdsa.PrivateKey) ([]byte, error) {
	return x509.MarshalECPrivateKey(key)
}

// pemEncode encodes data as PEM.
func pemEncode(blockType string, data []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: data})
}

func TestNewAdapter(t *testing.T) {
	adapter := NewAdapter()

	if adapter == nil {
		t.Fatal("Expected non-nil adapter")
	}

	if adapter.providers == nil {
		t.Error("Expected non-nil providers map")
	}
}

func TestNewAdapterWithDefaults(t *testing.T) {
	defaultConfig := TeleportConfig{
		CertFile:      "custom.crt",
		KeyFile:       "custom.key",
		CAFile:        "custom-ca.crt",
		WatchInterval: 60 * time.Second,
	}

	adapter := NewAdapterWithDefaults(defaultConfig)

	if adapter == nil {
		t.Fatal("Expected non-nil adapter")
	}

	if adapter.defaultConfig.CertFile != "custom.crt" {
		t.Errorf("Expected CertFile to be 'custom.crt', got %s", adapter.defaultConfig.CertFile)
	}

	if adapter.defaultConfig.WatchInterval != 60*time.Second {
		t.Errorf("Expected WatchInterval to be 60s, got %v", adapter.defaultConfig.WatchInterval)
	}
}

func TestAdapter_GetHTTPClientForIdentity(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	// Get HTTP client for the identity
	client, err := adapter.GetHTTPClientForIdentity(dir)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity failed: %v", err)
	}

	if client == nil {
		t.Fatal("Expected non-nil HTTP client")
	}

	// Getting client again should return from cache
	client2, err := adapter.GetHTTPClientForIdentity(dir)
	if err != nil {
		t.Fatalf("Second GetHTTPClientForIdentity failed: %v", err)
	}

	// The provider should be reused, so the client should be the same
	if client != client2 {
		t.Error("Expected same HTTP client from cache")
	}
}

func TestAdapter_GetHTTPClientForIdentity_MissingCerts(t *testing.T) {
	dir := t.TempDir()
	// Don't create certificates

	adapter := NewAdapter()
	defer adapter.Close()

	// Getting HTTP client should fail when certs are missing
	_, err := adapter.GetHTTPClientForIdentity(dir)
	if err == nil {
		t.Error("Expected error when certificates are missing")
	}
}

func TestAdapter_GetHTTPTransportForIdentity(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	transport, err := adapter.GetHTTPTransportForIdentity(dir)
	if err != nil {
		t.Fatalf("GetHTTPTransportForIdentity failed: %v", err)
	}

	if transport == nil {
		t.Fatal("Expected non-nil transport")
	}

	if transport.TLSClientConfig == nil {
		t.Error("Expected non-nil TLSClientConfig")
	}
}

func TestAdapter_GetClientProvider(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	provider, err := adapter.GetClientProvider(dir)
	if err != nil {
		t.Fatalf("GetClientProvider failed: %v", err)
	}

	if provider == nil {
		t.Fatal("Expected non-nil provider")
	}

	if !provider.IsLoaded() {
		t.Error("Expected provider to have loaded certificates")
	}
}

func TestAdapter_GetProviderStatus(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	// First, create the provider
	_, err := adapter.GetHTTPClientForIdentity(dir)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity failed: %v", err)
	}

	// Now get the status
	status, err := adapter.GetProviderStatus(dir)
	if err != nil {
		t.Fatalf("GetProviderStatus failed: %v", err)
	}

	if status == nil {
		t.Fatal("Expected non-nil status")
	}

	if !status.Loaded {
		t.Error("Expected status.Loaded to be true")
	}
}

func TestAdapter_GetProviderStatus_NotFound(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	_, err := adapter.GetProviderStatus("/nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent provider")
	}
}

func TestAdapter_ListProviders(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	createTestCertificates(t, dir1)
	createTestCertificates(t, dir2)

	adapter := NewAdapter()
	defer adapter.Close()

	// Initially no providers
	providers := adapter.ListProviders()
	if len(providers) != 0 {
		t.Errorf("Expected 0 providers initially, got %d", len(providers))
	}

	// Add providers
	_, _ = adapter.GetHTTPClientForIdentity(dir1)
	_, _ = adapter.GetHTTPClientForIdentity(dir2)

	providers = adapter.ListProviders()
	if len(providers) != 2 {
		t.Errorf("Expected 2 providers, got %d", len(providers))
	}
}

func TestAdapter_ReloadProvider(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	// First, create the provider
	_, err := adapter.GetHTTPClientForIdentity(dir)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity failed: %v", err)
	}

	// Reload should succeed
	err = adapter.ReloadProvider(dir)
	if err != nil {
		t.Fatalf("ReloadProvider failed: %v", err)
	}
}

func TestAdapter_ReloadProvider_NotFound(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	err := adapter.ReloadProvider("/nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent provider")
	}
}

func TestAdapter_RemoveProvider(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	// Create provider
	_, err := adapter.GetHTTPClientForIdentity(dir)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity failed: %v", err)
	}

	// Verify it exists
	providers := adapter.ListProviders()
	if len(providers) != 1 {
		t.Fatalf("Expected 1 provider, got %d", len(providers))
	}

	// Remove it
	err = adapter.RemoveProvider(dir)
	if err != nil {
		t.Fatalf("RemoveProvider failed: %v", err)
	}

	// Verify it's gone
	providers = adapter.ListProviders()
	if len(providers) != 0 {
		t.Errorf("Expected 0 providers after removal, got %d", len(providers))
	}
}

func TestAdapter_RemoveProvider_NotFound(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	// Removing nonexistent provider should not error
	err := adapter.RemoveProvider("/nonexistent")
	if err != nil {
		t.Errorf("RemoveProvider for nonexistent should not error: %v", err)
	}
}

func TestAdapter_Close(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	createTestCertificates(t, dir1)
	createTestCertificates(t, dir2)

	adapter := NewAdapter()

	// Create multiple providers
	_, _ = adapter.GetHTTPClientForIdentity(dir1)
	_, _ = adapter.GetHTTPClientForIdentity(dir2)

	// Close should clean up all providers
	err := adapter.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify providers are gone
	providers := adapter.ListProviders()
	if len(providers) != 0 {
		t.Errorf("Expected 0 providers after close, got %d", len(providers))
	}
}

func TestAdapter_MultipleIdentities(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	createTestCertificates(t, dir1)
	createTestCertificates(t, dir2)

	adapter := NewAdapter()
	defer adapter.Close()

	// Get clients for different identities
	client1, err := adapter.GetHTTPClientForIdentity(dir1)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity for dir1 failed: %v", err)
	}

	client2, err := adapter.GetHTTPClientForIdentity(dir2)
	if err != nil {
		t.Fatalf("GetHTTPClientForIdentity for dir2 failed: %v", err)
	}

	// Different identities should have different clients
	if client1 == client2 {
		t.Error("Expected different HTTP clients for different identities")
	}
}

func TestAdapter_GetHTTPClientForIdentity_PathTraversal(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	// Path traversal should be rejected
	_, err := adapter.GetHTTPClientForIdentity("/var/run/../etc/passwd")
	if err == nil {
		t.Error("Expected error for path traversal attempt")
	}
}

func TestAdapter_GetHTTPClientForIdentity_RelativePath(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	// Relative paths should be rejected
	_, err := adapter.GetHTTPClientForIdentity("relative/path")
	if err == nil {
		t.Error("Expected error for relative path")
	}
}

func TestAdapter_GetHTTPClientForConfig_InvalidAppName(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	ctx := context.Background()

	tests := []struct {
		name    string
		appName string
	}{
		{"starts with hyphen", "-invalid"},
		{"contains newline", "app\nname"},
		{"contains colon", "app:name"},
		{"contains slash", "app/name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := api.TeleportClientConfig{
				IdentityDir: dir,
				AppName:     tt.appName,
			}
			_, err := adapter.GetHTTPClientForConfig(ctx, config)
			if err == nil {
				t.Errorf("Expected error for invalid app name: %s", tt.appName)
			}
		})
	}
}

func TestAdapter_GetHTTPClientForConfig_ValidAppName(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	ctx := context.Background()

	tests := []struct {
		name    string
		appName string
	}{
		{"simple", "mcp-kubernetes"},
		{"with dots", "app.name.here"},
		{"with underscores", "app_name"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := api.TeleportClientConfig{
				IdentityDir: dir,
				AppName:     tt.appName,
			}
			_, err := adapter.GetHTTPClientForConfig(ctx, config)
			if err != nil {
				t.Errorf("Unexpected error for valid app name %q: %v", tt.appName, err)
			}
		})
	}
}

func TestAdapter_GetHTTPClientForConfig_MutualExclusivity(t *testing.T) {
	dir := t.TempDir()
	createTestCertificates(t, dir)

	adapter := NewAdapter()
	defer adapter.Close()

	ctx := context.Background()

	// Both identityDir AND identitySecretName specified should fail
	config := api.TeleportClientConfig{
		IdentityDir:        dir,
		IdentitySecretName: "some-secret",
	}
	_, err := adapter.GetHTTPClientForConfig(ctx, config)
	if err == nil {
		t.Error("Expected error when both identityDir and identitySecretName are specified")
	}
	if err != nil && err.Error() != "identityDir and identitySecretName are mutually exclusive; specify only one" {
		t.Errorf("Expected mutual exclusivity error, got: %v", err)
	}
}

func TestAdapter_GetHTTPClientForConfig_NeitherSpecified(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	ctx := context.Background()

	// Neither identityDir nor identitySecretName specified should fail
	config := api.TeleportClientConfig{
		AppName: "my-app",
	}
	_, err := adapter.GetHTTPClientForConfig(ctx, config)
	if err == nil {
		t.Error("Expected error when neither identityDir nor identitySecretName is specified")
	}
}

func TestNewAdapterWithClient(t *testing.T) {
	// Create a fake Kubernetes client
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	adapter := NewAdapterWithClient(k8sClient)

	if adapter == nil {
		t.Fatal("Expected non-nil adapter")
	}

	if adapter.k8sClient == nil {
		t.Error("Expected non-nil k8sClient")
	}

	if adapter.providers == nil {
		t.Error("Expected non-nil providers map")
	}

	if adapter.secretProviders == nil {
		t.Error("Expected non-nil secretProviders map")
	}
}

func TestAdapter_GetHTTPClientForConfig_WithSecret(t *testing.T) {
	// Create test certificate data
	certPEM, keyPEM, caPEM := generateTestCertificatePEMs(t)

	// Create a secret with test certificates
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tbot-identity",
			Namespace: "teleport-system",
		},
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": keyPEM,
			"ca.crt":  caPEM,
		},
	}

	// Create a fake Kubernetes client with the secret
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	adapter := NewAdapterWithClient(k8sClient)
	defer adapter.Close()

	ctx := context.Background()

	config := api.TeleportClientConfig{
		IdentitySecretName:      "tbot-identity",
		IdentitySecretNamespace: "teleport-system",
	}

	client, err := adapter.GetHTTPClientForConfig(ctx, config)
	if err != nil {
		t.Fatalf("GetHTTPClientForConfig with secret failed: %v", err)
	}

	if client == nil {
		t.Fatal("Expected non-nil HTTP client")
	}
}

func TestAdapter_GetHTTPClientForConfig_SecretMissingCert(t *testing.T) {
	// Create a secret missing the certificate file
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "incomplete-secret",
			Namespace: "teleport-system",
		},
		Data: map[string][]byte{
			"tls.key": []byte("key-data"),
			"ca.crt":  []byte("ca-data"),
			// Missing tls.crt
		},
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	adapter := NewAdapterWithClient(k8sClient)
	defer adapter.Close()

	ctx := context.Background()

	config := api.TeleportClientConfig{
		IdentitySecretName:      "incomplete-secret",
		IdentitySecretNamespace: "teleport-system",
	}

	_, err := adapter.GetHTTPClientForConfig(ctx, config)
	if err == nil {
		t.Error("Expected error when secret is missing tls.crt")
	}
}

func TestAdapter_GetHTTPClientForConfig_SecretNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	adapter := NewAdapterWithClient(k8sClient)
	defer adapter.Close()

	ctx := context.Background()

	config := api.TeleportClientConfig{
		IdentitySecretName:      "nonexistent-secret",
		IdentitySecretNamespace: "teleport-system",
	}

	_, err := adapter.GetHTTPClientForConfig(ctx, config)
	if err == nil {
		t.Error("Expected error when secret does not exist")
	}
}

func TestAdapter_GetHTTPClientForConfig_InvalidNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	adapter := NewAdapterWithClient(k8sClient)
	defer adapter.Close()

	ctx := context.Background()

	// Namespace not in allowed list
	config := api.TeleportClientConfig{
		IdentitySecretName:      "some-secret",
		IdentitySecretNamespace: "unauthorized-namespace",
	}

	_, err := adapter.GetHTTPClientForConfig(ctx, config)
	if err == nil {
		t.Error("Expected error for unauthorized namespace")
	}
}

func TestAdapter_GetHTTPClientForConfig_InvalidSecretName(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	adapter := NewAdapterWithClient(k8sClient)
	defer adapter.Close()

	ctx := context.Background()

	config := api.TeleportClientConfig{
		IdentitySecretName:      "INVALID_NAME",
		IdentitySecretNamespace: "teleport-system",
	}

	_, err := adapter.GetHTTPClientForConfig(ctx, config)
	if err == nil {
		t.Error("Expected error for invalid secret name")
	}
}

func TestAdapter_GetHTTPClientForConfig_NoK8sClient(t *testing.T) {
	adapter := NewAdapter() // No k8s client
	defer adapter.Close()

	ctx := context.Background()

	config := api.TeleportClientConfig{
		IdentitySecretName:      "some-secret",
		IdentitySecretNamespace: "teleport-system",
	}

	_, err := adapter.GetHTTPClientForConfig(ctx, config)
	if err == nil {
		t.Error("Expected error when K8s client is not available")
	}
}

func TestAppNameTransport_RoundTrip(t *testing.T) {
	// Create a test server that checks the Host header
	var receivedHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create the transport with an app name
	transport := &appNameTransport{
		base:    http.DefaultTransport,
		appName: "my-teleport-app",
	}

	client := &http.Client{Transport: transport}

	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if receivedHost != "my-teleport-app" {
		t.Errorf("Expected Host header to be 'my-teleport-app', got '%s'", receivedHost)
	}
}

func TestAppNameTransport_RoundTrip_EmptyAppName(t *testing.T) {
	// Create a test server that checks the Host header
	var receivedHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create the transport with an empty app name (should not modify Host)
	transport := &appNameTransport{
		base:    http.DefaultTransport,
		appName: "",
	}

	client := &http.Client{Transport: transport}

	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Host should be the original server host (from URL)
	if receivedHost == "my-teleport-app" {
		t.Error("Host should not be modified when appName is empty")
	}
}

func TestAdapter_Register(t *testing.T) {
	adapter := NewAdapter()
	defer adapter.Close()

	// Register should not panic
	adapter.Register()

	// Verify the adapter is registered
	handler := api.GetTeleportClient()
	if handler == nil {
		t.Error("Expected TeleportClient handler to be registered")
	}
}

func TestAdapter_CloseWithSecretProviders(t *testing.T) {
	// Create test certificate data
	certPEM, keyPEM, caPEM := generateTestCertificatePEMs(t)

	// Create a secret with test certificates
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tbot-identity",
			Namespace: "teleport-system",
		},
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": keyPEM,
			"ca.crt":  caPEM,
		},
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	adapter := NewAdapterWithClient(k8sClient)

	ctx := context.Background()

	// Create a secret-based provider
	config := api.TeleportClientConfig{
		IdentitySecretName:      "tbot-identity",
		IdentitySecretNamespace: "teleport-system",
	}
	_, err := adapter.GetHTTPClientForConfig(ctx, config)
	if err != nil {
		t.Fatalf("GetHTTPClientForConfig failed: %v", err)
	}

	// Close should clean up secret providers too
	err = adapter.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify both maps are cleared
	if len(adapter.providers) != 0 {
		t.Errorf("Expected providers map to be empty after close, got %d", len(adapter.providers))
	}
	if len(adapter.secretProviders) != 0 {
		t.Errorf("Expected secretProviders map to be empty after close, got %d", len(adapter.secretProviders))
	}
}

func TestAdapter_SecretProviderCaching(t *testing.T) {
	// Create test certificate data
	certPEM, keyPEM, caPEM := generateTestCertificatePEMs(t)

	// Create a secret with test certificates
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tbot-identity",
			Namespace: "teleport-system",
		},
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": keyPEM,
			"ca.crt":  caPEM,
		},
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	adapter := NewAdapterWithClient(k8sClient)
	defer adapter.Close()

	ctx := context.Background()

	config := api.TeleportClientConfig{
		IdentitySecretName:      "tbot-identity",
		IdentitySecretNamespace: "teleport-system",
	}

	// Get client first time
	client1, err := adapter.GetHTTPClientForConfig(ctx, config)
	if err != nil {
		t.Fatalf("First GetHTTPClientForConfig failed: %v", err)
	}

	// Get client second time - should be cached
	client2, err := adapter.GetHTTPClientForConfig(ctx, config)
	if err != nil {
		t.Fatalf("Second GetHTTPClientForConfig failed: %v", err)
	}

	// Should be the same client (cached)
	if client1 != client2 {
		t.Error("Expected same client from cache")
	}
}

// generateTestCertificatePEMs generates test TLS certificates and returns them as PEM-encoded bytes.
func generateTestCertificatePEMs(t *testing.T) (certPEM, keyPEM, caPEM []byte) {
	t.Helper()

	// Import required packages for certificate generation
	// These are already imported in the package test file

	// Generate CA key and certificate
	caKey, err := generateECKey()
	if err != nil {
		t.Fatalf("Failed to generate CA key: %v", err)
	}

	caTemplate := &testCertTemplate{
		commonName: "Test CA",
		isCA:       true,
	}
	caCertDER, err := createTestCert(caTemplate, nil, caKey, caKey)
	if err != nil {
		t.Fatalf("Failed to create CA certificate: %v", err)
	}
	caPEM = pemEncode("CERTIFICATE", caCertDER)

	// Generate client key and certificate
	clientKey, err := generateECKey()
	if err != nil {
		t.Fatalf("Failed to generate client key: %v", err)
	}

	clientTemplate := &testCertTemplate{
		commonName: "Test Client",
		isCA:       false,
	}
	clientCertDER, err := createTestCert(clientTemplate, caCertDER, clientKey, caKey)
	if err != nil {
		t.Fatalf("Failed to create client certificate: %v", err)
	}
	certPEM = pemEncode("CERTIFICATE", clientCertDER)

	// Marshal client key
	keyBytes, err := marshalECKey(clientKey)
	if err != nil {
		t.Fatalf("Failed to marshal client key: %v", err)
	}
	keyPEM = pemEncode("EC PRIVATE KEY", keyBytes)

	return certPEM, keyPEM, caPEM
}
