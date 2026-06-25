package mock

import (
	"crypto/ecdsa"
	"testing"

	"github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/require"
)

func TestMintSignedJWT_VerifiesAgainstPublicJWKS(t *testing.T) {
	srv := NewOAuthServer(OAuthServerConfig{
		Issuer:     "https://idp.example.com",
		SignTokens: true,
	})

	token, err := srv.MintSignedJWT(map[string]any{
		"sub":    "system:serviceaccount:kagent:sre-agent",
		"aud":    "cluster-b",
		"groups": []string{"team:sre"},
	}, "")
	require.NoError(t, err)

	jwks := srv.publicJWKS()
	require.NotNil(t, jwks)
	require.Len(t, jwks.Keys, 1)
	require.IsType(t, &ecdsa.PublicKey{}, jwks.Keys[0].Key)

	parsed, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.ES256})
	require.NoError(t, err)

	payload, err := parsed.Verify(jwks.Keys[0])
	require.NoError(t, err, "minted token must verify against the published JWKS")
	require.Contains(t, string(payload), "sre-agent")
	require.Contains(t, string(payload), "https://idp.example.com") // iss defaulted
}

func TestMintSignedJWT_DisabledWithoutSignTokens(t *testing.T) {
	srv := NewOAuthServer(OAuthServerConfig{Issuer: "https://idp.example.com"})
	_, err := srv.MintSignedJWT(map[string]any{"sub": "x"}, "")
	require.Error(t, err)
	require.Nil(t, srv.publicJWKS())
}

func TestMintSignedJWT_SetsTypHeader(t *testing.T) {
	srv := NewOAuthServer(OAuthServerConfig{Issuer: "https://idp.example.com", SignTokens: true})

	withTyp, err := srv.MintSignedJWT(map[string]any{"sub": "u"}, "at+jwt")
	require.NoError(t, err)
	parsed, err := jose.ParseSigned(withTyp, []jose.SignatureAlgorithm{jose.ES256})
	require.NoError(t, err)
	require.Equal(t, "at+jwt", parsed.Signatures[0].Header.ExtraHeaders[jose.HeaderType])

	noTyp, err := srv.MintSignedJWT(map[string]any{"sub": "u"}, "")
	require.NoError(t, err)
	parsedNoTyp, err := jose.ParseSigned(noTyp, []jose.SignatureAlgorithm{jose.ES256})
	require.NoError(t, err)
	_, hasTyp := parsedNoTyp.Signatures[0].Header.ExtraHeaders[jose.HeaderType]
	require.False(t, hasTyp, "empty typ must omit the header (SA-token shape)")
}
