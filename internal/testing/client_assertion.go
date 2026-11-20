package testing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func newAssertionKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

func addClientAssertion(values url.Values, key *ecdsa.PrivateKey, clientID, endpoint string) error {
	if key == nil {
		return nil
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: jose.JSONWebKey{Key: key, KeyID: "bdd-key"}}, nil)
	if err != nil {
		return fmt.Errorf("create assertion signer: %w", err)
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("create assertion ID: %w", err)
	}
	now := time.Now()
	assertion, err := jwt.Signed(signer).Claims(jwt.Claims{Issuer: clientID, Subject: clientID, Audience: jwt.Audience{endpoint}, IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(time.Minute)), ID: base64.RawURLEncoding.EncodeToString(nonce[:])}).Serialize()
	if err != nil {
		return fmt.Errorf("sign assertion: %w", err)
	}
	values.Set("client_assertion_type", "urn:ietf:params:oauth:client-assertion-type:jwt-bearer")
	values.Set("client_assertion", assertion)
	return nil
}

func checkForgedClientAssertion(ctx context.Context, endpoint string, values url.Values) (interface{}, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	var failure struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusUnauthorized || failure.Error != "invalid_client" {
		return nil, fmt.Errorf("forged assertion accepted or unexpected error: status %d", response.StatusCode)
	}
	return map[string]interface{}{"success": true, "error": "invalid_client"}, nil
}
