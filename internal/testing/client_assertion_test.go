package testing

import (
	"net/url"
	"testing"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/require"
)

func TestAddClientAssertionIssuerAudienceAndFreshIdentity(t *testing.T) {
	key, err := newAssertionKey()
	require.NoError(t, err)
	values := url.Values{}
	identifiers := map[string]bool{}
	for range 2 {
		require.NoError(t, addClientAssertion(values, key, "client", "https://issuer.example.com"))
		parsed, err := jwt.ParseSigned(values.Get("client_assertion"), []jose.SignatureAlgorithm{jose.ES256})
		require.NoError(t, err)
		var claims jwt.Claims
		require.NoError(t, parsed.Claims(&key.PublicKey, &claims))
		require.Equal(t, jwt.Audience{"https://issuer.example.com"}, claims.Audience)
		require.Equal(t, "client", claims.Issuer)
		require.Equal(t, "client", claims.Subject)
		require.Equal(t, "client-authentication+jwt", parsed.Headers[0].ExtraHeaders[jose.HeaderType])
		require.NotEmpty(t, claims.ID)
		require.False(t, identifiers[claims.ID])
		identifiers[claims.ID] = true
	}
	values = url.Values{"grant_type": {"refresh_token"}}
	require.NoError(t, addClientAssertion(values, nil, "client", "https://issuer.example.com"))
	require.Equal(t, url.Values{"grant_type": {"refresh_token"}}, values)
}
