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

	"github.com/giantswarm/mcp-oauth/providers/oidc"
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

// TestLocalMintProvider_ForwardsIdentityClaimsAndGroups verifies that on the OBO
// path the minted token carries the subject's validated identity claims (email,
// groups), merges the broker-granted groups, and preserves a multi-hop act chain
// (the new actor nested over the prior act on the subject token).
func TestLocalMintProvider_ForwardsIdentityClaimsAndGroups(t *testing.T) {
	t.Parallel()

	exchanger := newTestLocalMintExchanger(t)
	provider := &localMintProvider{exchanger: exchanger}

	subject := &oauthserver.SubjectIdentity{
		Subject: "alice@example.com",
		Issuer:  "https://dex.example.com",
		Claims: &oidc.IDTokenClaims{
			Email:         "alice@example.com",
			EmailVerified: true,
			Groups:        []string{"customer:platform-team"},
			Act:           &oidc.ActorClaim{Issuer: "https://dex.example.com", Subject: "agent-a"},
		},
	}

	result, err := provider.Mint(context.Background(), MintRequest{
		Subject:         subject.Subject,
		Target:          "mcp-prometheus",
		SubjectIdentity: subject,
		Actor:           &oauthserver.SubjectIdentity{Subject: "agent-b", Issuer: "https://kubernetes.default.svc"},
		GrantedGroups:   []string{"workload:granted-group"},
	})
	require.NoError(t, err)

	claims := localMintJWTClaims(t, result.AccessToken)
	require.Equal(t, "alice@example.com", claims["sub"])
	require.Equal(t, "alice@example.com", claims["email"], "subject email must be carried into the minted token")
	require.Equal(t, true, claims["email_verified"])

	groups := stringSlice(t, claims["groups"])
	require.Contains(t, groups, "customer:platform-team", "subject groups must be carried")
	require.Contains(t, groups, "workload:granted-group", "broker-granted groups must be merged")

	act, ok := claims["act"].(map[string]interface{})
	require.True(t, ok, "act claim must be present")
	require.Equal(t, "agent-b", act["sub"], "leaf actor is the new acting party")
	require.Equal(t, "https://kubernetes.default.svc", act["iss"])

	prior, ok := act["act"].(map[string]interface{})
	require.True(t, ok, "prior act chain on the subject token must be preserved (not collapsed)")
	require.Equal(t, "agent-a", prior["sub"])
}

// TestLocalMintProvider_M2M_NoSubjectClaims verifies that an M2M exchange whose
// subject carries no identity claims mints sub+aud only: no email, no groups,
// no act are fabricated.
func TestLocalMintProvider_M2M_NoSubjectClaims(t *testing.T) {
	t.Parallel()

	exchanger := newTestLocalMintExchanger(t)
	provider := &localMintProvider{exchanger: exchanger}

	subject := &oauthserver.SubjectIdentity{Subject: "system:serviceaccount:agent-ns:kagent"}

	result, err := provider.Mint(context.Background(), MintRequest{
		Subject:         subject.Subject,
		Target:          "mcp-kubernetes",
		SubjectIdentity: subject,
	})
	require.NoError(t, err)

	claims := localMintJWTClaims(t, result.AccessToken)
	require.Equal(t, "system:serviceaccount:agent-ns:kagent", claims["sub"])
	require.Equal(t, "mcp-kubernetes", claims["aud"])
	require.NotContains(t, claims, "email")
	require.NotContains(t, claims, "groups")
	require.NotContains(t, claims, "act")
}

// stringSlice converts a decoded JSON array claim into a []string.
func stringSlice(t *testing.T, v interface{}) []string {
	t.Helper()
	raw, ok := v.([]interface{})
	require.True(t, ok, "expected a JSON array, got %T", v)
	out := make([]string, len(raw))
	for i := range raw {
		s, ok := raw[i].(string)
		require.True(t, ok, "expected string element, got %T", raw[i])
		out[i] = s
	}
	return out
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
