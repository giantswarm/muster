package oauth

import (
	"strings"
	"testing"
)

func TestLoadValidToken(t *testing.T) {
	token, err := LoadValidToken()
	if err != nil {
		t.Fatalf("Failed to load valid token fixture: %v", err)
	}

	if token.AccessToken == "" {
		t.Error("Expected non-empty access token")
	}

	if token.TokenType != "Bearer" {
		t.Errorf("Expected token type 'Bearer', got '%s'", token.TokenType)
	}

	if token.ExpiresIn != 3600 {
		t.Errorf("Expected expires_in 3600, got %d", token.ExpiresIn)
	}

	if token.Scope == "" {
		t.Error("Expected non-empty scope")
	}
}

func TestLoadExpiredToken(t *testing.T) {
	token, err := LoadExpiredToken()
	if err != nil {
		t.Fatalf("Failed to load expired token fixture: %v", err)
	}

	if token.AccessToken == "" {
		t.Error("Expected non-empty access token")
	}

	if token.TokenType != "Bearer" {
		t.Errorf("Expected token type 'Bearer', got '%s'", token.TokenType)
	}

	// Verify this is clearly an old/expired token based on dates
	if !strings.Contains(token.ExpiresAt, "2024-01-10") {
		t.Errorf("Expected expired token to have old date, got '%s'", token.ExpiresAt)
	}
}

func TestLoadMetadata(t *testing.T) {
	metadata, err := LoadMetadata()
	if err != nil {
		t.Fatalf("Failed to load metadata fixture: %v", err)
	}

	if metadata.Issuer == "" {
		t.Error("Expected non-empty issuer")
	}

	if metadata.AuthorizationEndpoint == "" {
		t.Error("Expected non-empty authorization endpoint")
	}

	if metadata.TokenEndpoint == "" {
		t.Error("Expected non-empty token endpoint")
	}

	if len(metadata.ScopesSupported) == 0 {
		t.Error("Expected non-empty scopes_supported")
	}

	// Verify expected scopes are present
	expectedScopes := []string{"openid", "profile", "email"}
	for _, expected := range expectedScopes {
		found := false
		for _, scope := range metadata.ScopesSupported {
			if scope == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected scope '%s' not found in scopes_supported", expected)
		}
	}
}

func TestLoadWWWAuthenticateExamples(t *testing.T) {
	examples := LoadWWWAuthenticateExamples()

	if examples == "" {
		t.Error("Expected non-empty WWW-Authenticate examples")
	}

	// Verify examples contain expected patterns
	expectedPatterns := []string{
		"Bearer realm=",
		"scope=",
		"authz_server=",
		"error=",
	}

	for _, pattern := range expectedPatterns {
		if !strings.Contains(examples, pattern) {
			t.Errorf("Expected WWW-Authenticate examples to contain '%s'", pattern)
		}
	}
}

func TestRawDataFunctions(t *testing.T) {
	validJSON := ValidTokenJSON()
	if len(validJSON) == 0 {
		t.Error("Expected non-empty valid token JSON")
	}

	expiredJSON := ExpiredTokenJSON()
	if len(expiredJSON) == 0 {
		t.Error("Expected non-empty expired token JSON")
	}

	metadataJSON := MetadataJSON()
	if len(metadataJSON) == 0 {
		t.Error("Expected non-empty metadata JSON")
	}
}
