package oauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/config"
)

// newTestLocalMintExchanger constructs a LocalMintExchanger backed by a fresh
// ECDSA P-256 key for use in tests.
func newTestLocalMintExchanger(t *testing.T) *oauthserver.LocalMintExchanger {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	jwkKey := jose.JSONWebKey{Key: key.Public()}
	raw, err := jwkKey.Thumbprint(crypto.SHA256)
	require.NoError(t, err)
	kid := base64.RawURLEncoding.EncodeToString(raw)

	cfg := &oauthserver.Config{
		Issuer:                      "https://muster.test",
		AccessTokenFormat:           oauthserver.AccessTokenFormatJWT,
		AccessTokenSigningKey:       key,
		AccessTokenSigningKeyID:     kid,
		AccessTokenSigningAlgorithm: oauthserver.SigningAlgorithmES256,
		AccessTokenTTL:              600,
	}
	exchanger, err := oauthserver.NewLocalMintExchanger(cfg)
	require.NoError(t, err)
	return exchanger
}

// localMintJWTClaims decodes the payload of a dot-separated JWT into a map
// without verifying the signature.
func localMintJWTClaims(t *testing.T, token string) map[string]interface{} {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "expected 3-part JWT")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &claims))
	return claims
}

func TestLocalMintProvider_DelegatedMint(t *testing.T) {
	t.Parallel()

	exchanger := newTestLocalMintExchanger(t)
	provider := &localMintProvider{exchanger: exchanger}

	result, err := provider.Mint(context.Background(), MintRequest{
		Subject: "alice@example.com",
		Target:  "mcp-kubernetes",
		Actor: &oauthserver.SubjectIdentity{
			Subject: "system:serviceaccount:agent-ns:kagent",
			Issuer:  "https://kubernetes.default.svc",
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, result.AccessToken)

	claims := localMintJWTClaims(t, result.AccessToken)
	require.Equal(t, "alice@example.com", claims["sub"], "sub must be the human subject")
	require.Equal(t, "https://muster.test", claims["iss"])
	require.Equal(t, "mcp-kubernetes", claims["aud"])

	act, ok := claims["act"].(map[string]interface{})
	require.True(t, ok, "act claim must be present for delegated exchange")
	require.Equal(t, "system:serviceaccount:agent-ns:kagent", act["sub"])
	require.Equal(t, "https://kubernetes.default.svc", act["iss"])
}

func TestLocalMintProvider_NilActor_NoActClaim(t *testing.T) {
	t.Parallel()

	exchanger := newTestLocalMintExchanger(t)
	provider := &localMintProvider{exchanger: exchanger}

	result, err := provider.Mint(context.Background(), MintRequest{
		Subject: "alice@example.com",
		Target:  "mcp-kubernetes",
		Actor:   nil,
	})

	require.NoError(t, err)
	claims := localMintJWTClaims(t, result.AccessToken)
	require.Equal(t, "alice@example.com", claims["sub"])
	_, hasAct := claims["act"]
	require.False(t, hasAct, "non-delegated exchange must not carry act claim")
}

func TestLocalMintProvider_NilExchanger_ReturnsConfigError(t *testing.T) {
	t.Parallel()

	provider := &localMintProvider{exchanger: nil}
	_, err := provider.Mint(context.Background(), MintRequest{
		Subject: "alice@example.com",
		Target:  "mcp-kubernetes",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "enableJWTMode")
}

func TestRegistry_LocalMintTargetResolvesToLocalMintProvider(t *testing.T) {
	t.Parallel()

	exchanger := newTestLocalMintExchanger(t)
	registry := defaultProviderRegistry()

	provider, err := registry.forTarget("mcp-kubernetes", config.BrokerTargetConfig{
		Type: config.TargetTypeLocalMint,
	}, providerDeps{localMint: exchanger})

	require.NoError(t, err)
	_, ok := provider.(*localMintProvider)
	require.True(t, ok, "TargetTypeLocalMint must resolve to localMintProvider")
}

func TestRegistry_LocalMintWithNilExchanger_ProviderCreated(t *testing.T) {
	t.Parallel()

	// Registry construction succeeds when localMint is nil; the error is
	// deferred to Mint time so the broker can start without JWT mode.
	registry := defaultProviderRegistry()

	provider, err := registry.forTarget("mcp-kubernetes", config.BrokerTargetConfig{
		Type: config.TargetTypeLocalMint,
	}, providerDeps{localMint: nil})

	require.NoError(t, err)
	lp, ok := provider.(*localMintProvider)
	require.True(t, ok)
	require.Nil(t, lp.exchanger)
}
