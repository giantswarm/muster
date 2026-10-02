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
// listed private host passes the SSRF guard, and the fetch trusts the
// --extra-ca-file CA.
func TestCreateOAuthServer_CIMDPrivateHostAndCA(t *testing.T) {
	clientID, redirectURI, cimdCA := startPrivateCIMDClient(t)

	resolve := func(t *testing.T, hosts []string, withCA bool) error {
		t.Helper()
		cfg := config.OAuthServerConfig{
			BaseURL:                           "https://muster.example.com",
			Provider:                          OAuthProviderGoogle,
			Google:                            config.GoogleConfig{ClientID: "muster", ClientSecret: "secret"},
			EnableCIMD:                        true,
			AllowPrivateIPClientMetadataHosts: hosts,
		}
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

	t.Run("listed private host under the extra CA resolves", func(t *testing.T) {
		require.NoError(t, resolve(t, []string{"localhost"}, true))
	})
	t.Run("unlisted private host is refused", func(t *testing.T) {
		err := resolve(t, []string{"gateway.internal.example"}, true)
		require.ErrorContains(t, err, "private")
	})
	t.Run("no listed host is refused", func(t *testing.T) {
		err := resolve(t, nil, true)
		require.ErrorContains(t, err, "private")
	})
	t.Run("without the extra CA TLS verification fails", func(t *testing.T) {
		err := resolve(t, []string{"localhost"}, false)
		require.ErrorContains(t, err, "certificate")
	})
}
