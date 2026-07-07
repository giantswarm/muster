package oauth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	oauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/providers/mock"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/giantswarm/mcp-oauth/storage/memory"
	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/require"
)

// These tests exercise the credential-less self-issued exchange
// (Server.SelfIssuedExchange) that replaced the removed standalone
// LocalMintExchanger. muster's local-mint targets now mint through this path:
// the server validates the subject (and optional actor) token against its
// trusted issuers and signs an RFC 9068 JWT whose aud is the requested RFC 8707
// resource. The tests drive a JWT-mode *oauth.Server directly, minting subject
// and actor tokens signed by a mock trusted issuer whose JWKS is served locally.

const (
	selfIssuedWorkloadIssuer = "https://workload-idp.test"
	selfIssuedMusterIssuer   = "https://muster.test"
	selfIssuedMusterResource = "https://muster.test/mcp"
	// selfIssuedJWTType is the RFC 8693 token-type URN for the subject/actor JWTs.
	selfIssuedJWTType = "urn:ietf:params:oauth:token-type:jwt" //nolint:gosec // G101: RFC 8693 token-type URN, not a credential
	// selfIssuedAllowedResource is the sole configured local-mint target.
	selfIssuedAllowedResource = "cluster-b"
)

// ecKeyWithKID generates a fresh ECDSA P-256 key and its RFC 7638 thumbprint kid.
func ecKeyWithKID(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	jwk := jose.JSONWebKey{Key: key.Public()}
	raw, err := jwk.Thumbprint(crypto.SHA256)
	require.NoError(t, err)
	return key, base64.RawURLEncoding.EncodeToString(raw)
}

// serveJWKS starts an httptest TLS server exposing a single-key JWKS for the
// given public key and kid, returning the JWKS URL and a cert pool trusting the
// server. mcp-oauth's JWKS client requires HTTPS even for private-IP issuers, so
// the pool is threaded into the trusted issuer's RootCAs.
func serveJWKS(t *testing.T, key *ecdsa.PrivateKey, kid string) (string, *x509.CertPool) {
	t.Helper()
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key:       key.Public(),
		KeyID:     kid,
		Algorithm: string(jose.ES256),
		Use:       "sig",
	}}}
	body, err := json.Marshal(set)
	require.NoError(t, err)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv.URL, pool
}

// signIssuerToken signs claims as an ES256 JWT with the issuer key and kid. No
// typ header is set, matching the AcceptedTypHeaders: [""] trusted-issuer config.
func signIssuerToken(t *testing.T, key *ecdsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: key},
		(&jose.SignerOptions{}).WithHeader("kid", kid),
	)
	require.NoError(t, err)
	token, err := josejwt.Signed(signer).Claims(claims).Serialize()
	require.NoError(t, err)
	return token
}

// decodeJWTClaims returns the unverified payload claims of a compact JWT.
func decodeJWTClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "expected a 3-part JWT")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims))
	return claims
}

// selfIssuedTestServer builds a JWT-mode *oauth.Server that trusts the workload
// issuer (via the served JWKS) and allows selfIssuedAllowedResource as a
// self-issued resource. It returns the server and the workload issuer key + kid
// used to sign subject and actor tokens.
func selfIssuedTestServer(t *testing.T) (*oauth.Server, *ecdsa.PrivateKey, string) {
	t.Helper()

	issuerKey, issuerKID := ecKeyWithKID(t)
	jwksURL, jwksPool := serveJWKS(t, issuerKey, issuerKID)

	musterKey, musterKID := ecKeyWithKID(t)
	cfg := &oauthserver.Config{
		Issuer:                        selfIssuedMusterIssuer,
		ResourceIdentifier:            selfIssuedMusterResource,
		AccessTokenFormat:             oauthserver.AccessTokenFormatJWT,
		AccessTokenSigningKey:         musterKey,
		AccessTokenSigningKeyID:       musterKID,
		AccessTokenSigningAlgorithm:   oauthserver.SigningAlgorithmES256,
		AccessTokenTTL:                600,
		TokenExchangeAllowedResources: []string{selfIssuedAllowedResource},
	}

	store := memory.New()
	t.Cleanup(store.Stop)

	ti := oauthserver.TrustedIssuer{
		Issuer:             selfIssuedWorkloadIssuer,
		JwksURL:            jwksURL,
		AllowPrivateIPJWKS: true,
		AcceptedTypHeaders: []string{""},
		RootCAs:            jwksPool,
	}
	srv, err := oauth.NewServerWithCombined(mock.NewProvider(), store, cfg, nil,
		oauthserver.WithTrustedIssuers([]oauthserver.TrustedIssuer{ti}))
	require.NoError(t, err)
	return srv, issuerKey, issuerKID
}

// subjectClaims builds a baseline subject-token claim set for the workload issuer.
func subjectClaims(sub string) map[string]any {
	return map[string]any{
		"iss": selfIssuedWorkloadIssuer,
		"sub": sub,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
}

// actorClaims builds an actor-token claim set bound to muster's issuer as its
// audience (the anti-replay default the server enforces on actor tokens).
func actorClaims(sub string) map[string]any {
	c := subjectClaims(sub)
	c["aud"] = selfIssuedMusterIssuer
	return c
}

func TestSelfIssuedExchange_DelegatedMint(t *testing.T) {
	t.Parallel()

	srv, key, kid := selfIssuedTestServer(t)

	subject := signIssuerToken(t, key, kid, subjectClaims("alice@example.com"))
	actor := signIssuerToken(t, key, kid, actorClaims("system:serviceaccount:agent-ns:kagent"))

	result, err := srv.SelfIssuedExchange(t.Context(), oauthserver.SelfIssuedExchangeRequest{
		SubjectExchange: oauthserver.SubjectExchange{
			Subject:  oauthserver.TypedToken{Token: subject, Type: selfIssuedJWTType},
			Actor:    oauthserver.TypedToken{Token: actor, Type: selfIssuedJWTType},
			Resource: selfIssuedAllowedResource,
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.AccessToken)

	claims := decodeJWTClaims(t, result.AccessToken)
	require.Equal(t, "alice@example.com", claims["sub"], "sub must be the human subject")
	require.Equal(t, selfIssuedMusterIssuer, claims["iss"])
	require.Equal(t, selfIssuedAllowedResource, claims["aud"], "aud must be the requested resource")

	act, ok := claims["act"].(map[string]any)
	require.True(t, ok, "act claim must be present for delegated exchange")
	require.Equal(t, "system:serviceaccount:agent-ns:kagent", act["sub"])
	require.Equal(t, selfIssuedWorkloadIssuer, act["iss"])
}

func TestSelfIssuedExchange_NilActor_NoActClaim(t *testing.T) {
	t.Parallel()

	srv, key, kid := selfIssuedTestServer(t)
	subject := signIssuerToken(t, key, kid, subjectClaims("alice@example.com"))

	result, err := srv.SelfIssuedExchange(t.Context(), oauthserver.SelfIssuedExchangeRequest{
		SubjectExchange: oauthserver.SubjectExchange{
			Subject:  oauthserver.TypedToken{Token: subject, Type: selfIssuedJWTType},
			Resource: selfIssuedAllowedResource,
		},
	})
	require.NoError(t, err)

	claims := decodeJWTClaims(t, result.AccessToken)
	require.Equal(t, "alice@example.com", claims["sub"])
	require.NotContains(t, claims, "act", "non-delegated exchange must not carry act claim")
}

// TestSelfIssuedExchange_ForwardsIdentityClaims verifies the minted token carries
// the subject's validated identity claims (email, groups) and preserves a
// multi-hop act chain: the new actor nested over the prior act on the subject.
func TestSelfIssuedExchange_ForwardsIdentityClaims(t *testing.T) {
	t.Parallel()

	srv, key, kid := selfIssuedTestServer(t)

	subClaims := subjectClaims("alice@example.com")
	subClaims["email"] = "alice@example.com"
	subClaims["email_verified"] = true
	subClaims["groups"] = []string{"customer:platform-team"}
	subClaims["act"] = map[string]any{"iss": selfIssuedWorkloadIssuer, "sub": "agent-a"}
	subject := signIssuerToken(t, key, kid, subClaims)
	actor := signIssuerToken(t, key, kid, actorClaims("agent-b"))

	result, err := srv.SelfIssuedExchange(t.Context(), oauthserver.SelfIssuedExchangeRequest{
		SubjectExchange: oauthserver.SubjectExchange{
			Subject:  oauthserver.TypedToken{Token: subject, Type: selfIssuedJWTType},
			Actor:    oauthserver.TypedToken{Token: actor, Type: selfIssuedJWTType},
			Resource: selfIssuedAllowedResource,
		},
	})
	require.NoError(t, err)

	claims := decodeJWTClaims(t, result.AccessToken)
	require.Equal(t, "alice@example.com", claims["sub"])
	require.Equal(t, "alice@example.com", claims["email"], "subject email must be carried")
	require.Equal(t, true, claims["email_verified"])

	groups := stringSlice(t, claims["groups"])
	require.Contains(t, groups, "customer:platform-team", "subject groups must be carried")

	act, ok := claims["act"].(map[string]any)
	require.True(t, ok, "act claim must be present")
	require.Equal(t, "agent-b", act["sub"], "leaf actor is the new acting party")

	prior, ok := act["act"].(map[string]any)
	require.True(t, ok, "prior act chain on the subject token must be preserved (not collapsed)")
	require.Equal(t, "agent-a", prior["sub"])
}

// TestSelfIssuedExchange_NoSubjectClaims verifies a workload subject with no
// identity claims mints sub+aud only: no email, groups, or act are fabricated.
func TestSelfIssuedExchange_NoSubjectClaims(t *testing.T) {
	t.Parallel()

	srv, key, kid := selfIssuedTestServer(t)
	subject := signIssuerToken(t, key, kid, subjectClaims("system:serviceaccount:agent-ns:kagent"))

	result, err := srv.SelfIssuedExchange(t.Context(), oauthserver.SelfIssuedExchangeRequest{
		SubjectExchange: oauthserver.SubjectExchange{
			Subject:  oauthserver.TypedToken{Token: subject, Type: selfIssuedJWTType},
			Resource: selfIssuedAllowedResource,
		},
	})
	require.NoError(t, err)

	claims := decodeJWTClaims(t, result.AccessToken)
	require.Equal(t, "system:serviceaccount:agent-ns:kagent", claims["sub"])
	require.Equal(t, selfIssuedAllowedResource, claims["aud"])
	require.NotContains(t, claims, "email")
	require.NotContains(t, claims, "groups")
	require.NotContains(t, claims, "act")
}

// TestSelfIssuedExchange_DefaultsAudToResourceIdentifier verifies that an
// exchange with no resource mints a token bound to muster's own resource
// identifier.
func TestSelfIssuedExchange_DefaultsAudToResourceIdentifier(t *testing.T) {
	t.Parallel()

	srv, key, kid := selfIssuedTestServer(t)
	subject := signIssuerToken(t, key, kid, subjectClaims("system:serviceaccount:agent-ns:kagent"))

	result, err := srv.SelfIssuedExchange(t.Context(), oauthserver.SelfIssuedExchangeRequest{
		SubjectExchange: oauthserver.SubjectExchange{
			Subject: oauthserver.TypedToken{Token: subject, Type: selfIssuedJWTType},
		},
	})
	require.NoError(t, err)

	claims := decodeJWTClaims(t, result.AccessToken)
	require.Equal(t, selfIssuedMusterResource, claims["aud"])
}

// TestSelfIssuedExchange_UnverifiedSubjectEmail_Refused verifies the fail-closed
// email gate: a subject asserting an email without email_verified=true is refused.
func TestSelfIssuedExchange_UnverifiedSubjectEmail_Refused(t *testing.T) {
	t.Parallel()

	srv, key, kid := selfIssuedTestServer(t)

	subClaims := subjectClaims("alice@example.com")
	subClaims["email"] = "alice@example.com"
	subClaims["email_verified"] = false
	subject := signIssuerToken(t, key, kid, subClaims)

	_, err := srv.SelfIssuedExchange(t.Context(), oauthserver.SelfIssuedExchangeRequest{
		SubjectExchange: oauthserver.SubjectExchange{
			Subject:  oauthserver.TypedToken{Token: subject, Type: selfIssuedJWTType},
			Resource: selfIssuedAllowedResource,
		},
	})
	require.ErrorIs(t, err, oauthserver.ErrUnverifiedSubjectEmail)
}

// TestSelfIssuedExchange_DisallowedResource_Refused verifies that a resource
// outside TokenExchangeAllowedResources is rejected with invalid_target.
func TestSelfIssuedExchange_DisallowedResource_Refused(t *testing.T) {
	t.Parallel()

	srv, key, kid := selfIssuedTestServer(t)
	subject := signIssuerToken(t, key, kid, subjectClaims("system:serviceaccount:agent-ns:kagent"))

	_, err := srv.SelfIssuedExchange(t.Context(), oauthserver.SelfIssuedExchangeRequest{
		SubjectExchange: oauthserver.SubjectExchange{
			Subject:  oauthserver.TypedToken{Token: subject, Type: selfIssuedJWTType},
			Resource: "cluster-c",
		},
	})
	require.ErrorIs(t, err, oauthserver.ErrInvalidTarget)
}

// stringSlice converts a decoded JSON array claim into a []string.
func stringSlice(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	require.True(t, ok, "expected a JSON array, got %T", v)
	out := make([]string, len(raw))
	for i := range raw {
		s, ok := raw[i].(string)
		require.True(t, ok, "expected string element, got %T", raw[i])
		out[i] = s
	}
	return out
}
