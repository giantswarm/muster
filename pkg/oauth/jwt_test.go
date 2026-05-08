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
		sub, err := Subject(jwtFromPayload(t, `{"sub":"alice"}`))
		require.NoError(t, err)
		assert.Equal(t, "alice", sub)
	})

	t.Run("returns error for malformed token", func(t *testing.T) {
		sub, err := Subject("not-a-jwt")
		require.Error(t, err)
		assert.Equal(t, "", sub)
	})

	t.Run("returns empty without error when sub absent", func(t *testing.T) {
		sub, err := Subject(jwtFromPayload(t, `{"email":"a@b"}`))
		require.NoError(t, err)
		assert.Equal(t, "", sub)
	})

	t.Run("returns error for empty token", func(t *testing.T) {
		_, err := Subject("")
		require.Error(t, err)
	})
}

func TestEmail(t *testing.T) {
	t.Run("returns email claim", func(t *testing.T) {
		email, err := Email(jwtFromPayload(t, `{"sub":"alice","email":"alice@example.com"}`))
		require.NoError(t, err)
		assert.Equal(t, "alice@example.com", email)
	})

	t.Run("returns error for malformed token", func(t *testing.T) {
		_, err := Email("not-a-jwt")
		require.Error(t, err)
	})

	t.Run("returns empty without error when email absent", func(t *testing.T) {
		email, err := Email(jwtFromPayload(t, `{"sub":"alice"}`))
		require.NoError(t, err)
		assert.Equal(t, "", email)
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
			sub, err := Subject(token)
			require.NoError(t, err)
			assert.Equal(t, "alice", sub)
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
	_, err := Subject(twoPart)
	require.Error(t, err)
	_, err = Expiry(twoPart)
	require.Error(t, err)
	_, err = Issuer(twoPart)
	require.Error(t, err)
}

func TestIsExpired(t *testing.T) {
	t.Run("returns false for token with future exp", func(t *testing.T) {
		expired, err := IsExpired(jwtFromPayload(t, `{"exp":9999999999}`))
		require.NoError(t, err)
		assert.False(t, expired)
	})

	t.Run("returns true with no error for past exp", func(t *testing.T) {
		expired, err := IsExpired(jwtFromPayload(t, `{"exp":1}`))
		require.NoError(t, err)
		assert.True(t, expired)
	})

	t.Run("returns true with ErrTokenExpMissing when exp absent", func(t *testing.T) {
		expired, err := IsExpired(jwtFromPayload(t, `{"sub":"alice"}`))
		assert.True(t, expired)
		assert.True(t, errors.Is(err, ErrTokenExpMissing))
	})

	t.Run("returns true with decode error for malformed token", func(t *testing.T) {
		expired, err := IsExpired("not-a-jwt")
		assert.True(t, expired)
		require.Error(t, err)
		assert.False(t, errors.Is(err, ErrTokenExpMissing))
	})
}
