// Package oauth provides embedded test fixtures for OAuth testing.
package oauth

import (
	_ "embed"
)

//go:embed valid_token.json
var validTokenData []byte

//go:embed expired_token.json
var expiredTokenData []byte

//go:embed metadata.json
var metadataData []byte

//go:embed www_authenticate.txt
var wwwAuthenticateData []byte

// TokenFixture represents an OAuth token fixture.
type TokenFixture struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	IssuedAt     string `json:"issued_at"`
	ExpiresAt    string `json:"expires_at"`
}

// MetadataFixture represents an OAuth server metadata fixture.
type MetadataFixture struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	UserinfoEndpoint                  string   `json:"userinfo_endpoint"`
	JwksURI                           string   `json:"jwks_uri"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []string `json:"scopes_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	SubjectTypesSupported             []string `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported  []string `json:"id_token_signing_alg_values_supported"`
}
