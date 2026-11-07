package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/require"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
)

// signingDex is a Dex stand-in that serves discovery and its JWKS over TLS
// and signs ID tokens the way Dex does: RS256, a kid, no typ header.
type signingDex struct {
	issuer string
	caFile string
	key    *rsa.PrivateKey
}

func startSigningDex(t *testing.T) *signingDex {
	t.Helper()

	cert, _ := localhostCert(t)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0o600))

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	issuer := "https://localhost:" + port

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                issuer,
				"authorization_endpoint":                issuer + "/auth",
				"token_endpoint":                        issuer + "/token",
				"userinfo_endpoint":                     issuer + "/userinfo",
				"jwks_uri":                              issuer + "/keys",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
				{Key: &key.PublicKey, KeyID: "dex-key", Algorithm: string(jose.RS256), Use: "sig"},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	require.NoError(t, server.Listener.Close())
	server.Listener = listener
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)

	return &signingDex{issuer: issuer, caFile: caFile, key: key}
}

// idToken signs a Dex ID token issued to the client audience; extra adds or
// overrides claims.
func (d *signingDex) idToken(t *testing.T, audience string, expiry time.Time, extra ...map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: d.key, KeyID: "dex-key"}},
		nil,
	)
	require.NoError(t, err)
	now := time.Now()
	claims := map[string]any{
		"iss":            d.issuer,
		"sub":            "CiQwOGE4Njg0Yi1kYjg4LTRiNzMtOTBhOS0zY2QxNjYxZjU0NjYSBWxvY2Fs",
		"aud":            audience,
		"azp":            audience,
		"exp":            expiry.Unix(),
		"iat":            now.Add(-time.Minute).Unix(),
		"email":          "alice@giantswarm.io",
		"email_verified": true,
		"name":           "Alice",
		"groups":         []string{"giantswarm-admins"},
	}
	for _, e := range extra {
		maps.Copy(claims, e)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	require.NoError(t, err)
	return token
}

// TestMCPAcceptsAgentgatewayForwardedDexToken pins the inbound trust muster
// grants a Dex token that agentgateway forwards unchanged on /mcp: an ID
// token whose aud is a trustedAudiences client, and a token of a
// trustedIssuers entry whose aud is one of its allowedAudiences, both
// without muster's resource identifier in aud and without an at+jwt typ.
// mcp-oauth's ValidateToken middleware rejects both ("Token validation
// failed"); muster authenticates them through the trust it configured.
func TestMCPAcceptsAgentgatewayForwardedDexToken(t *testing.T) {
	dexServer := startSigningDex(t)

	var seen *providers.UserInfo
	var seenSubject string
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = oauthhandler.UserInfoFromContext(r.Context())
		seenSubject = api.GetSubjectFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	srv, err := NewOAuthHTTPServer(config.OAuthServerConfig{
		Enabled:          true,
		BaseURL:          "https://muster.example.com",
		Provider:         OAuthProviderDex,
		ExtraCAFile:      dexServer.caFile,
		TrustedAudiences: []string{"agent-platform", "dex-k8s-authenticator"},
		TrustedIssuers: []config.TrustedIssuerConfig{{
			Issuer:             dexServer.issuer,
			JwksURL:            dexServer.issuer + "/keys",
			AllowedAudiences:   []string{"kagent"},
			AcceptedTypHeaders: []string{""},
			AllowPrivateIPJWKS: true,
		}},
		Dex: config.DexConfig{
			IssuerURL:          dexServer.issuer,
			ClientID:           "muster",
			ClientSecret:       "secret",
			AllowPrivateIPOIDC: true,
		},
	}, mcp, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.WithoutCancel(t.Context())) })
	mux := srv.CreateMux()

	call := func(t *testing.T, path, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		seen, seenSubject = nil, ""
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil)
		request.Header.Set("Authorization", "Bearer "+bearer)
		request.RemoteAddr = "100.64.212.55:41000"
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}

	t.Run("ID token issued to a trusted audience", func(t *testing.T) {
		for _, path := range []string{"/mcp", "/sse", "/message"} {
			response := call(t, path, dexServer.idToken(t, "agent-platform", time.Now().Add(10*time.Minute)))
			require.Equal(t, http.StatusOK, response.Code, "%s: %s", path, response.Body.String())
			require.NotNil(t, seen)
			require.Equal(t, "alice@giantswarm.io", seen.Email)
			require.NotEqual(t, providers.TokenSourceTrustedIssuer, seen.TokenSource)
			require.NotEmpty(t, seenSubject)
		}
	})

	t.Run("token of a trusted issuer for one of its allowed audiences", func(t *testing.T) {
		response := call(t, "/mcp", dexServer.idToken(t, "kagent", time.Now().Add(10*time.Minute)))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.NotNil(t, seen)
		require.Equal(t, providers.TokenSourceTrustedIssuer, seen.TokenSource)
		require.NotEmpty(t, seenSubject)
	})

	t.Run("delegated token only through the trusted issuer's policy", func(t *testing.T) {
		act := map[string]any{"act": map[string]any{"sub": "agent:sre-agent"}}
		response := call(t, "/mcp", dexServer.idToken(t, "kagent", time.Now().Add(10*time.Minute), act))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Equal(t, providers.TokenSourceTrustedIssuer, seen.TokenSource)
	})

	for name, bearer := range map[string]string{
		"delegated token for a trusted audience": dexServer.idToken(t, "agent-platform", time.Now().Add(10*time.Minute),
			map[string]any{"act": map[string]any{"sub": "agent:sre-agent"}}),
		"sender-constrained token without its DPoP proof": dexServer.idToken(t, "agent-platform", time.Now().Add(10*time.Minute),
			map[string]any{"cnf": map[string]any{"jkt": "0ZcOCORZNYy-DWpqq30jZyJGHTN0d2HglBV3uiguA4I"}}),
		"untrusted audience": dexServer.idToken(t, "someone-else", time.Now().Add(10*time.Minute)),
		"expired token":      dexServer.idToken(t, "agent-platform", time.Now().Add(-10*time.Minute)),
		"unknown opaque":     "opaque-access-token-muster-never-issued",
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			response := call(t, "/mcp", bearer)
			require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
			require.Nil(t, seen, "the MCP handler must not run")
		})
	}
}
