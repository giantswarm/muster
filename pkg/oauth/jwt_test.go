package oauth

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	jwtTestHeaderRawURL = "eyJhbGciOiJSUzI1NiJ9" // {"alg":"RS256"}
	jwtTestSig          = "sig"
)

// jwtFromPayload builds a fake 3-part JWT from a payload-JSON string.
func jwtFromPayload(t *testing.T, payload string) string {
	t.Helper()
	return jwtTestHeaderRawURL + "." +
		base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		jwtTestSig
}

func TestSubject(t *testing.T) {
	t.Run("returns sub claim", func(t *testing.T) {
		assert.Equal(t, "alice", Subject(jwtFromPayload(t, `{"sub":"alice"}`)))
	})

	t.Run("returns empty for malformed token", func(t *testing.T) {
		assert.Equal(t, "", Subject("not-a-jwt"))
	})

	t.Run("returns empty when sub absent", func(t *testing.T) {
		assert.Equal(t, "", Subject(jwtFromPayload(t, `{"email":"a@b"}`)))
	})

	t.Run("returns empty for empty token", func(t *testing.T) {
		assert.Equal(t, "", Subject(""))
	})
}

func TestEmail(t *testing.T) {
	t.Run("returns email claim", func(t *testing.T) {
		assert.Equal(t, "alice@example.com",
			Email(jwtFromPayload(t, `{"sub":"alice","email":"alice@example.com"}`)))
	})

	t.Run("returns empty for malformed token", func(t *testing.T) {
		assert.Equal(t, "", Email("not-a-jwt"))
	})

	t.Run("returns empty when email absent", func(t *testing.T) {
		assert.Equal(t, "", Email(jwtFromPayload(t, `{"sub":"alice"}`)))
	})
}

func TestExpiry(t *testing.T) {
	t.Run("returns exp claim", func(t *testing.T) {
		exp, err := Expiry(jwtFromPayload(t, `{"exp":9999999999}`))
		require.NoError(t, err)
		assert.Equal(t, int64(9999999999), exp.Unix())
	})

	t.Run("returns ErrTokenExpMissing when exp absent", func(t *testing.T) {
		_, err := Expiry(jwtFromPayload(t, `{"sub":"alice"}`))
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrTokenExpMissing))
	})

	t.Run("wraps decode error for malformed token", func(t *testing.T) {
		_, err := Expiry("not-a-jwt")
		require.Error(t, err)
		assert.False(t, errors.Is(err, ErrTokenExpMissing))
	})

	t.Run("wraps decode error for empty token", func(t *testing.T) {
		_, err := Expiry("")
		require.Error(t, err)
		assert.False(t, errors.Is(err, ErrTokenExpMissing))
	})
}

func TestIssuer(t *testing.T) {
	t.Run("returns iss claim", func(t *testing.T) {
		iss, err := Issuer(jwtFromPayload(t, `{"iss":"https://dex.example"}`))
		require.NoError(t, err)
		assert.Equal(t, "https://dex.example", iss)
	})

	t.Run("returns empty without error when iss absent", func(t *testing.T) {
		iss, err := Issuer(jwtFromPayload(t, `{"sub":"alice"}`))
		require.NoError(t, err)
		assert.Equal(t, "", iss)
	})

	t.Run("returns error for malformed token", func(t *testing.T) {
		_, err := Issuer("not-a-jwt")
		require.Error(t, err)
	})
}

// TestPaddedBase64 verifies the parser accepts both padded and unpadded
// base64url payloads. RFC 7515 §2 mandates unpadded; padding tolerance is
// for non-spec IdPs.
func TestPaddedBase64(t *testing.T) {
	const payload = `{"sub":"alice","exp":9999999999}`
	cases := []struct {
		name string
		enc  *base64.Encoding
	}{
		{"unpadded url-safe (RFC 7515)", base64.RawURLEncoding},
		{"padded url-safe", base64.URLEncoding},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := jwtTestHeaderRawURL + "." +
				tc.enc.EncodeToString([]byte(payload)) + "." + jwtTestSig
			assert.Equal(t, "alice", Subject(token))
			exp, err := Expiry(token)
			require.NoError(t, err)
			assert.Equal(t, int64(9999999999), exp.Unix())
		})
	}
}

// TestRejectsNon3PartTokens locks in the contract that typed accessors
// require a 3-part JWT — they're intended for tokens minted by a real auth
// flow, not for diagnostic inspection of partial inputs.
func TestRejectsNon3PartTokens(t *testing.T) {
	twoPart := jwtTestHeaderRawURL + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"alice"}`))
	assert.Equal(t, "", Subject(twoPart))
	_, err := Expiry(twoPart)
	require.Error(t, err)
	_, err = Issuer(twoPart)
	require.Error(t, err)
}
