package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// jwksValidator verifies ES256/RS256 JWTs against a remote JWKS endpoint. It is
// used by an OAuth-protected mock backend configured to trust muster's own
// access-token signing key, so scenarios can prove a downstream MCP server
// accepts a broker-minted token end-to-end.
type jwksValidator struct {
	jwksURL          string
	expectedAudience string
	expectedIssuer   string
	httpClient       *http.Client

	mu     sync.Mutex
	cached *jose.JSONWebKeySet
}

func newJWKSValidator(jwksURL, expectedAudience, expectedIssuer string) *jwksValidator {
	return &jwksValidator{
		jwksURL:          jwksURL,
		expectedAudience: expectedAudience,
		expectedIssuer:   expectedIssuer,
		httpClient:       &http.Client{Timeout: 5 * time.Second},
	}
}

// tokenClaims are the broker-minted claims a backend asserts on.
type tokenClaims struct {
	Sub    string         `json:"sub"`
	Iss    string         `json:"iss"`
	Aud    audience       `json:"aud"`
	Groups []string       `json:"groups,omitempty"`
	Act    map[string]any `json:"act,omitempty"`
	Exp    int64          `json:"exp"`
}

// audience accepts both the string and []string JSON forms of the aud claim.
type audience []string

func (a *audience) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = []string{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// validate verifies the token signature against the JWKS and checks exp, audience
// and (when configured) issuer. It returns the decoded claims on success.
func (v *jwksValidator) validate(ctx context.Context, token string) (*tokenClaims, error) {
	parsed, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.ES256, jose.RS256})
	if err != nil {
		return nil, fmt.Errorf("parsing JWT: %w", err)
	}

	keys, err := v.keys(ctx, false)
	if err != nil {
		return nil, err
	}
	payload, verr := verifyAgainst(parsed, keys)
	if verr != nil {
		// Refresh once: muster may have started/rotated after the first fetch.
		keys, err = v.keys(ctx, true)
		if err != nil {
			return nil, err
		}
		payload, verr = verifyAgainst(parsed, keys)
		if verr != nil {
			return nil, fmt.Errorf("signature verification failed: %w", verr)
		}
	}

	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("decoding claims: %w", err)
	}
	if claims.Exp > 0 && time.Now().After(time.Unix(claims.Exp, 0)) {
		return nil, fmt.Errorf("token expired")
	}
	if v.expectedAudience != "" && !slices.Contains([]string(claims.Aud), v.expectedAudience) {
		return nil, fmt.Errorf("audience %v does not include %q", []string(claims.Aud), v.expectedAudience)
	}
	if v.expectedIssuer != "" && claims.Iss != v.expectedIssuer {
		return nil, fmt.Errorf("issuer %q does not match expected %q", claims.Iss, v.expectedIssuer)
	}
	return &claims, nil
}

func verifyAgainst(parsed *jose.JSONWebSignature, set *jose.JSONWebKeySet) ([]byte, error) {
	var lastErr error
	for _, key := range set.Keys {
		if payload, err := parsed.Verify(key); err == nil {
			return payload, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no keys in JWKS")
	}
	return nil, lastErr
}

func (v *jwksValidator) keys(ctx context.Context, forceRefresh bool) (*jose.JSONWebKeySet, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cached != nil && !forceRefresh {
		return v.cached, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building JWKS request: %w", err)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching JWKS from %s: %w", v.jwksURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching JWKS from %s: status %d", v.jwksURL, resp.StatusCode)
	}

	var set jose.JSONWebKeySet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("decoding JWKS: %w", err)
	}
	v.cached = &set
	return &set, nil
}
