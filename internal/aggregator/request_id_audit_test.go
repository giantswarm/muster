package aggregator

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
)

const (
	auditTestAudience = "upstream-client"
	auditTestKeyID    = "lab-dex-key"
)

// startSigningDex serves OIDC discovery and a JWKS over TLS at a localhost
// issuer under a self-signed CA, and returns the issuer, the CA as a PEM file
// and the key that signs its ID tokens.
func startSigningDex(t *testing.T) (issuer, caFile string, signingKey *ecdsa.PrivateKey) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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
	der, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caFile = filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))

	signingKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: signingKey.Public(), KeyID: auditTestKeyID, Algorithm: string(jose.ES256), Use: "sig",
	}}}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	issuer = "https://localhost:" + port

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 issuer,
				"authorization_endpoint": issuer + "/auth",
				"token_endpoint":         issuer + "/token",
				"jwks_uri":               issuer + "/keys",
			})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jwks)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	require.NoError(t, server.Listener.Close())
	server.Listener = listener
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: caKey}},
		MinVersion:   tls.VersionTLS12,
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	return issuer, caFile, signingKey
}

func signIDToken(t *testing.T, key *ecdsa.PrivateKey, issuer string) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", auditTestKeyID))
	require.NoError(t, err)
	now := time.Now()
	token, err := josejwt.Signed(signer).Claims(josejwt.Claims{
		Issuer:   issuer,
		Subject:  "person-subject",
		Audience: josejwt.Audience{auditTestAudience},
		IssuedAt: josejwt.NewNumericDate(now),
		Expiry:   josejwt.NewNumericDate(now.Add(time.Hour)),
	}).Claims(map[string]any{"email": "person@example.com", "email_verified": true}).Serialize()
	require.NoError(t, err)
	return token
}

// TestCreateHTTPMux_ForwardedIDTokenAuditCarriesRequestAndSessionID sends one
// MCP request with a forwarded Dex ID token and an X-Request-ID through the
// served handler chain, and reads mcp-oauth's forwarded_id_token_accepted
// audit lines.
func TestCreateHTTPMux_ForwardedIDTokenAuditCarriesRequestAndSessionID(t *testing.T) {
	issuer, caFile, signingKey := startSigningDex(t)

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	t.Cleanup(func() { api.RegisterBrowserIdentity(nil) })

	a := &AggregatorServer{
		ctx: t.Context(),
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config: config.OAuthServerConfig{
					Enabled:          true,
					BaseURL:          "https://muster.example.com",
					Provider:         "dex",
					ExtraCAFile:      caFile,
					TrustedAudiences: []string{auditTestAudience},
					Dex: config.DexConfig{
						IssuerURL:          issuer,
						ClientID:           "muster",
						ClientSecret:       "secret",
						AllowPrivateIPOIDC: true,
					},
				},
			},
		},
	}

	var openedSession string
	mcpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openedSession = api.GetSessionIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler, err := a.createHTTPMux(mcpHandler)
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.oauthHTTPServer.Shutdown(t.Context()) })

	const requestID = "req-1628"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+signIDToken(t, signingKey, issuer))
	req.Header.Set("X-Request-ID", requestID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, requestID, rec.Header().Get("X-Request-ID"))
	require.NotEmpty(t, openedSession)

	var accepted int
	for line := range bytes.SplitSeq(logs.Bytes(), []byte("\n")) {
		var entry struct {
			Audit struct {
				EventType string         `json:"event_type"`
				RequestID string         `json:"request_id"`
				Details   map[string]any `json:"details"`
			} `json:"audit"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Audit.EventType != "forwarded_id_token_accepted" {
			continue
		}
		accepted++
		require.Equal(t, requestID, entry.Audit.RequestID)
		require.Equal(t, openedSession, entry.Audit.Details["session_id"])
	}
	require.Positive(t, accepted, "no forwarded_id_token_accepted audit line in:\n%s", logs.String())
}
