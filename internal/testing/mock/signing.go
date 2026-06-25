package mock

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/go-jose/go-jose/v4"
)

// newSigningKey generates an EC P-256 signing key and returns it alongside its
// RFC 7638 JWK SHA-256 thumbprint, used as the kid. The kid derivation matches
// muster's own access-token signing (internal/server/jwt_key.go) so a mock acting
// as a trusted issuer behaves like a real one.
func newSigningKey() (*ecdsa.PrivateKey, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generating EC signing key: %w", err)
	}
	jwk := jose.JSONWebKey{Key: key.Public()}
	raw, err := jwk.Thumbprint(crypto.SHA256)
	if err != nil {
		return nil, "", fmt.Errorf("computing JWK thumbprint: %w", err)
	}
	return key, base64.RawURLEncoding.EncodeToString(raw), nil
}

// MintSignedJWT signs claims into a compact ES256 JWT using the server's signing
// key. typHeader sets the JWT `typ` header; an empty string omits it (matching
// Kubernetes ServiceAccount tokens, which carry no typ). Returns an error when
// the server was not created with SignTokens enabled.
//
// Missing iss/iat/exp are filled from the server's issuer and token lifetime so
// callers only need to provide the identity-bearing claims (sub, aud, groups, act).
func (s *OAuthServer) MintSignedJWT(claims map[string]any, typHeader string) (string, error) {
	s.mu.RLock()
	key, kid := s.signingKey, s.signingKID
	issuer := s.config.Issuer
	lifetime := s.config.TokenLifetime
	now := s.clock.Now()
	s.mu.RUnlock()

	if key == nil {
		return "", fmt.Errorf("mock OAuth server %q was not created with SignTokens enabled", issuer)
	}

	full := make(map[string]any, len(claims)+3)
	maps.Copy(full, claims)
	if _, ok := full["iss"]; !ok {
		full["iss"] = issuer
	}
	if _, ok := full["iat"]; !ok {
		full["iat"] = now.Unix()
	}
	if _, ok := full["exp"]; !ok {
		full["exp"] = now.Add(lifetime).Unix()
	}

	payload, err := json.Marshal(full)
	if err != nil {
		return "", fmt.Errorf("marshaling claims: %w", err)
	}

	opts := (&jose.SignerOptions{}).WithHeader("kid", kid)
	if typHeader != "" {
		opts = opts.WithType(jose.ContentType(typHeader))
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, opts)
	if err != nil {
		return "", fmt.Errorf("creating signer: %w", err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}
	return signed.CompactSerialize()
}

// encodeIDToken serializes ID token claims as a compact JWT. When signing is
// enabled it is an ES256-signed JWT verifiable against the server's JWKS (so the
// server can act as a broker trusted issuer); otherwise it is the legacy
// alg:none token used by most scenarios.
func (s *OAuthServer) encodeIDToken(claimsJSON []byte) string {
	if s.signingKey != nil {
		opts := (&jose.SignerOptions{}).WithHeader("kid", s.signingKID).WithType("JWT")
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: s.signingKey}, opts)
		if err == nil {
			if signed, err := signer.Sign(claimsJSON); err == nil {
				if compact, err := signed.CompactSerialize(); err == nil {
					return compact
				}
			}
		}
	}
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	return fmt.Sprintf("%s.%s.%s", jwtHeader, payload, jwtDummySignature)
}

// publicJWKS returns the JWK set advertising the server's signing public key, or
// nil when signing is disabled.
func (s *OAuthServer) publicJWKS() *jose.JSONWebKeySet {
	s.mu.RLock()
	key, kid := s.signingKey, s.signingKID
	s.mu.RUnlock()

	if key == nil {
		return nil
	}
	return &jose.JSONWebKeySet{
		Keys: []jose.JSONWebKey{{
			Key:       key.Public(),
			KeyID:     kid,
			Algorithm: string(jose.ES256),
			Use:       "sig",
		}},
	}
}
