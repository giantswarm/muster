package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	oidcpkg "github.com/giantswarm/mcp-oauth/providers/oidc"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
)

// testRSAKey generates a throwaway 2048-bit RSA key for use in tests.
func testRSAKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return key, pemBytes
}

// stubSecretKeyHandler is a SecretCredentialsHandler test double that serves
// a fixed PEM for LoadSecretKey and stubs out LoadClientCredentials.
type stubSecretKeyHandler struct {
	pemBytes []byte
	err      error
}

func (s *stubSecretKeyHandler) LoadClientCredentials(_ context.Context, _ *api.ClientCredentialsSecretRef, _ string) (*api.ClientCredentials, error) {
	return nil, fmt.Errorf("LoadClientCredentials not implemented in stubSecretKeyHandler")
}

func (s *stubSecretKeyHandler) LoadSecretKey(_ context.Context, _ *api.ClientCredentialsSecretRef, _ string, _ string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.pemBytes, nil
}

// jwtPayloadClaims decodes the payload section of a compact JWT without
// signature verification, returning the raw claims map. Panics on malformed input.
func jwtPayloadClaims(t *testing.T, compact string) map[string]any {
	t.Helper()
	parts := strings.Split(compact, ".")
	require.Len(t, parts, 3, "expected compact JWT (3 dot-separated parts)")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err, "decoding JWT payload")
	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims), "unmarshaling JWT claims")
	return claims
}

// newGithubTestProvider builds a githubAppProvider pointing at the given test
// server URL.
func newGithubTestProvider(t *testing.T, cfg *config.GithubAppTargetConfig, serverURL string, pemBytes []byte) *githubAppProvider {
	t.Helper()
	cfg.BaseURL = serverURL
	withCredentialsHandler(t, &stubSecretKeyHandler{pemBytes: pemBytes})
	return &githubAppProvider{
		target:     config.BrokerTargetConfig{GithubApp: cfg},
		cache:      oidcpkg.NewTokenExchangeCache(),
		defaultNS:  "muster-system",
		httpClient: &http.Client{},
	}
}

// okTokenHandler returns an httptest handler that always serves a 201 token
// response and records the decoded request body into *body.
func okTokenHandler(t *testing.T, tokenValue string, body *githubInstallationTokenRequest) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(body)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      tokenValue,
				"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			})
		}
	}
}

// TestGithubAppProvider_AppJWTClaims verifies that the App JWT sent to the
// GitHub API has a correct iss, a backdated iat, and exp-iat ≤ 10 minutes.
func TestGithubAppProvider_AppJWTClaims(t *testing.T) {
	_, pemBytes := testRSAKey(t)

	var gotAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42})
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_test_token",
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}))
	t.Cleanup(server.Close)

	provider := newGithubTestProvider(t, &config.GithubAppTargetConfig{
		AppID:         "99999",
		Owner:         "my-org",
		Repo:          "my-repo",
		PrivateKeyRef: &config.BrokerSecretRefConfig{Name: "pem"},
	}, server.URL, pemBytes)

	_, err := provider.Mint(t.Context(), MintRequest{Target: "t"})
	require.NoError(t, err)

	require.NotEmpty(t, gotAuthHeader)
	require.True(t, strings.HasPrefix(gotAuthHeader, "Bearer "), "Authorization must be Bearer")
	tokenStr := strings.TrimPrefix(gotAuthHeader, "Bearer ")

	claims := jwtPayloadClaims(t, tokenStr)
	assert.Equal(t, "99999", claims["iss"], "iss must be AppID")

	iat := time.Unix(int64(claims["iat"].(float64)), 0)
	exp := time.Unix(int64(claims["exp"].(float64)), 0)
	now := time.Now()

	assert.True(t, iat.Before(now), "iat must be backdated (iat=%v, now=%v)", iat, now)
	assert.LessOrEqual(t, exp.Sub(iat), 10*time.Minute, "exp-iat must be ≤ 10 minutes")
}

// TestGithubAppProvider_RequestBody verifies that repositories and permissions
// from the config are forwarded in the access_tokens request body.
func TestGithubAppProvider_RequestBody(t *testing.T) {
	_, pemBytes := testRSAKey(t)
	var gotBody githubInstallationTokenRequest
	server := httptest.NewServer(okTokenHandler(t, "ghs_scoped", &gotBody))
	t.Cleanup(server.Close)

	provider := newGithubTestProvider(t, &config.GithubAppTargetConfig{
		AppID:          "99999",
		InstallationID: "12345",
		Repositories:   []string{"infra"},
		Permissions:    map[string]string{"contents": "read"},
		PrivateKeyRef:  &config.BrokerSecretRefConfig{Name: "pem"},
	}, server.URL, pemBytes)

	_, err := provider.Mint(t.Context(), MintRequest{Target: "scoped"})
	require.NoError(t, err)
	assert.Equal(t, []string{"infra"}, gotBody.Repositories)
	assert.Equal(t, map[string]string{"contents": "read"}, gotBody.Permissions)
}

// TestGithubAppProvider_EmptyReposPermissions verifies that nil repositories
// and permissions are omitted from the request body (omitempty).
func TestGithubAppProvider_EmptyReposPermissions(t *testing.T) {
	_, pemBytes := testRSAKey(t)
	var gotBody githubInstallationTokenRequest
	server := httptest.NewServer(okTokenHandler(t, "ghs_wide", &gotBody))
	t.Cleanup(server.Close)

	provider := newGithubTestProvider(t, &config.GithubAppTargetConfig{
		AppID:          "99999",
		InstallationID: "12345",
		PrivateKeyRef:  &config.BrokerSecretRefConfig{Name: "pem"},
	}, server.URL, pemBytes)

	_, err := provider.Mint(t.Context(), MintRequest{Target: "wide"})
	require.NoError(t, err)
	assert.Nil(t, gotBody.Repositories)
	assert.Nil(t, gotBody.Permissions)
}

// TestGithubAppProvider_InstallationDiscovery verifies that when InstallationID
// is empty the provider performs GET /repos/{owner}/{repo}/installation and
// uses the returned ID in the token POST path.
func TestGithubAppProvider_InstallationDiscovery(t *testing.T) {
	_, pemBytes := testRSAKey(t)
	var discoveryPath, tokenPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			discoveryPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 777})
			return
		}
		tokenPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_discovered",
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}))
	t.Cleanup(server.Close)

	provider := newGithubTestProvider(t, &config.GithubAppTargetConfig{
		AppID:         "99999",
		Owner:         "acme-org",
		Repo:          "platform",
		PrivateKeyRef: &config.BrokerSecretRefConfig{Name: "pem"},
	}, server.URL, pemBytes)

	result, err := provider.Mint(t.Context(), MintRequest{Target: "disc"})
	require.NoError(t, err)
	assert.Equal(t, "ghs_discovered", result.AccessToken)
	assert.Equal(t, "/repos/acme-org/platform/installation", discoveryPath)
	assert.Equal(t, "/app/installations/777/access_tokens", tokenPath)
}

// TestGithubAppProvider_Cache verifies that the second Mint for the same
// (installationID, permissions) is served from cache without a second API call.
func TestGithubAppProvider_Cache(t *testing.T) {
	_, pemBytes := testRSAKey(t)
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			callCount.Add(1)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "ghs_cached",
				"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			})
		}
	}))
	t.Cleanup(server.Close)

	provider := newGithubTestProvider(t, &config.GithubAppTargetConfig{
		AppID:          "99999",
		InstallationID: "42",
		PrivateKeyRef:  &config.BrokerSecretRefConfig{Name: "pem"},
	}, server.URL, pemBytes)

	first, err := provider.Mint(t.Context(), MintRequest{Target: "t"})
	require.NoError(t, err)
	assert.False(t, first.FromCache)

	second, err := provider.Mint(t.Context(), MintRequest{Target: "t"})
	require.NoError(t, err)
	assert.True(t, second.FromCache, "second Mint must be served from cache")
	assert.Equal(t, "ghs_cached", second.AccessToken)
	assert.Equal(t, int32(1), callCount.Load(), "GitHub API must be called only once")
}

// TestGithubAppProvider_NoSecretHandler verifies that a missing secret handler
// returns an appropriate error (not ErrInvalidTarget).
func TestGithubAppProvider_NoSecretHandler(t *testing.T) {
	withCredentialsHandler(t, nil)

	provider := &githubAppProvider{
		target: config.BrokerTargetConfig{
			GithubApp: &config.GithubAppTargetConfig{
				AppID:          "99999",
				InstallationID: "42",
				PrivateKeyRef:  &config.BrokerSecretRefConfig{Name: "pem"},
				BaseURL:        "https://api.github.com",
			},
		},
		cache:      oidcpkg.NewTokenExchangeCache(),
		defaultNS:  "muster-system",
		httpClient: http.DefaultClient,
	}

	_, err := provider.Mint(t.Context(), MintRequest{Target: "t"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no secret credentials handler registered")
}

// TestGithubAppProvider_MissingConfig verifies that a target configured with
// type github-app but no githubApp block returns a clear error.
func TestGithubAppProvider_MissingConfig(t *testing.T) {
	provider := &githubAppProvider{
		target:     config.BrokerTargetConfig{},
		cache:      oidcpkg.NewTokenExchangeCache(),
		defaultNS:  "muster-system",
		httpClient: http.DefaultClient,
	}

	_, err := provider.Mint(t.Context(), MintRequest{Target: "t"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no githubApp config")
}

// TestProviderRegistry_GithubAppType verifies that targets with type "github-app"
// resolve to the github-app provider and do NOT return ErrInvalidTarget.
func TestProviderRegistry_GithubAppType(t *testing.T) {
	withCredentialsHandler(t, nil)

	broker := &BrokerExchanger{
		cfg: config.TokenExchangeBrokerConfig{
			Targets: map[string]config.BrokerTargetConfig{
				"github": {
					Type: config.TargetTypeGithubApp,
					GithubApp: &config.GithubAppTargetConfig{
						AppID:          "99999",
						InstallationID: "42",
						PrivateKeyRef:  &config.BrokerSecretRefConfig{Name: "pem"},
						BaseURL:        "https://api.github.com",
					},
				},
			},
		},
		registry:    defaultProviderRegistry(),
		githubCache: oidcpkg.NewTokenExchangeCache(),
		httpClient:  http.DefaultClient,
	}

	_, err := broker.Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:     "github",
		Subject:      &oauthserver.SubjectIdentity{Subject: "user-1"},
		SubjectToken: "subject-token",
	})
	require.Error(t, err)
	assert.False(t, errors.Is(err, oauthserver.ErrInvalidTarget),
		"github-app type must resolve to a provider, not ErrInvalidTarget, got: %v", err)
}
