package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

// startPrivateCIMDClient serves a Client ID Metadata Document over TLS on a
// loopback address under a self-signed CA: a CIMD client on a private
// hostname whose certificate the system pool does not trust. It returns the
// client_id URL, its redirect URI and the CA certificate in PEM.
func startPrivateCIMDClient(t *testing.T) (clientID, redirectURI string, caPEM []byte) {
	t.Helper()

	cert, _ := localhostCert(t)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	clientID = "https://localhost:" + port + "/client.json"
	redirectURI = "https://localhost:" + port + "/callback"

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/client.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  clientID,
			"client_name":                "private CIMD client",
			"redirect_uris":              []string{redirectURI},
			"grant_types":                []string{"authorization_code", "refresh_token"},
			"response_types":             []string{"code"},
			"token_endpoint_auth_method": "none",
		})
	}))
	require.NoError(t, server.Listener.Close())
	server.Listener = listener
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)

	return clientID, redirectURI, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
}

// TestCreateOAuthServer_CIMDPrivateHostAndCA resolves a CIMD client the way
// /oauth/authorize does, through the server createOAuthServer builds: only a
// listed host may resolve to a private address, never to loopback, and the
// fetch trusts the --extra-ca-file CA. The test client listens on loopback, so
// the CA cases lift the guard with the blanket.
func TestCreateOAuthServer_CIMDPrivateHostAndCA(t *testing.T) {
	clientID, redirectURI, cimdCA := startPrivateCIMDClient(t)

	resolve := func(t *testing.T, cfg config.OAuthServerConfig, withCA bool) error {
		t.Helper()
		cfg.BaseURL = "https://muster.example.com"
		cfg.Provider = OAuthProviderGoogle
		cfg.Google = config.GoogleConfig{ClientID: "muster", ClientSecret: "secret"}
		cfg.EnableCIMD = true
		if withCA {
			caFile := filepath.Join(t.TempDir(), "ca.pem")
			require.NoError(t, os.WriteFile(caFile, cimdCA, 0o600))
			cfg.ExtraCAFile = caFile
		}
		components, err := createOAuthServer(cfg, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = components.server.Shutdown(context.Background()) })

		_, err = components.server.ValidateRedirectURIForAuthorization(context.Background(), clientID, redirectURI)
		return err
	}

	t.Run("a private client under the extra CA resolves", func(t *testing.T) {
		require.NoError(t, resolve(t, config.OAuthServerConfig{AllowPrivateIPClientMetadata: true}, true)) //nolint:staticcheck // SA1019: covers the deprecated setting
	})
	t.Run("without the extra CA TLS verification fails", func(t *testing.T) {
		err := resolve(t, config.OAuthServerConfig{AllowPrivateIPClientMetadata: true}, false) //nolint:staticcheck // SA1019: covers the deprecated setting
		require.ErrorContains(t, err, "certificate")
	})
	t.Run("a listed host resolving to loopback is refused", func(t *testing.T) {
		err := resolve(t, config.OAuthServerConfig{AllowPrivateIPClientMetadataHosts: []string{"localhost"}}, true) //nolint:staticcheck // SA1019: covers the deprecated setting
		require.ErrorContains(t, err, "only RFC 1918 and RFC 4193 are lifted")
	})
	t.Run("unlisted private host is refused", func(t *testing.T) {
		err := resolve(t, config.OAuthServerConfig{AllowPrivateIPClientMetadataHosts: []string{"gateway.internal.example"}}, true) //nolint:staticcheck // SA1019: covers the deprecated setting
		require.ErrorContains(t, err, "private")
	})
	t.Run("no listed host is refused", func(t *testing.T) {
		err := resolve(t, config.OAuthServerConfig{}, true)
		require.ErrorContains(t, err, "private")
	})
}
