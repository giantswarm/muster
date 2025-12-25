package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// DefaultTokenStorageDir is the default directory for storing OAuth tokens.
const DefaultTokenStorageDir = ".config/muster/tokens"

// TokenStore provides secure storage for OAuth tokens.
// It supports both file-based (XDG-compliant) and in-memory storage.
//
// SECURITY: This store handles sensitive OAuth credentials. The following
// security measures are implemented:
//   - Files are created with 0600 permissions (owner read/write only)
//   - Storage directory is created with 0700 permissions (owner only)
//   - Token values are NEVER logged (only server URLs and issuers)
//   - Expired tokens are automatically rejected
//   - Token expiry includes a 60-second buffer for safety
type TokenStore struct {
	mu         sync.RWMutex
	storageDir string
	tokens     map[string]*StoredToken // In-memory cache
	fileMode   bool                    // Whether to persist to files
}

// StoredToken represents a stored OAuth token with metadata.
type StoredToken struct {
	// AccessToken is the OAuth access token.
	AccessToken string `json:"access_token"`

	// RefreshToken is the OAuth refresh token (if available).
	RefreshToken string `json:"refresh_token,omitempty"`

	// TokenType is typically "Bearer".
	TokenType string `json:"token_type"`

	// Expiry is when the access token expires.
	Expiry time.Time `json:"expiry,omitempty"`

	// IDToken is the OIDC ID token (if available).
	IDToken string `json:"id_token,omitempty"`

	// ServerURL is the URL of the server this token authenticates to.
	ServerURL string `json:"server_url"`

	// IssuerURL is the OAuth issuer that issued this token.
	IssuerURL string `json:"issuer_url"`

	// CreatedAt is when the token was stored.
	CreatedAt time.Time `json:"created_at"`
}

// TokenStoreConfig configures the token store.
type TokenStoreConfig struct {
	// StorageDir is the directory for storing token files.
	// Defaults to ~/.config/muster/tokens
	StorageDir string

	// FileMode enables file-based persistence. If false, tokens are in-memory only.
	FileMode bool
}

// NewTokenStore creates a new token store with the specified configuration.
func NewTokenStore(cfg TokenStoreConfig) (*TokenStore, error) {
	storageDir := cfg.StorageDir
	if storageDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		storageDir = filepath.Join(homeDir, DefaultTokenStorageDir)
	}

	store := &TokenStore{
		storageDir: storageDir,
		tokens:     make(map[string]*StoredToken),
		fileMode:   cfg.FileMode,
	}

	// Create storage directory if file mode is enabled
	if cfg.FileMode {
		if err := os.MkdirAll(storageDir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create token storage directory: %w", err)
		}
	}

	return store, nil
}

// StoreToken stores an OAuth token for a specific server.
// SECURITY: Token values are never logged. Only server/issuer URLs are logged for audit purposes.
func (s *TokenStore) StoreToken(serverURL, issuerURL string, token *oauth2.Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	storedToken := &StoredToken{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		Expiry:       token.Expiry,
		ServerURL:    serverURL,
		IssuerURL:    issuerURL,
		CreatedAt:    time.Now(),
	}

	// Extract ID token from extra data if available
	if idToken := token.Extra("id_token"); idToken != nil {
		if idTokenStr, ok := idToken.(string); ok {
			storedToken.IDToken = idTokenStr
		}
	}

	// Store in memory cache
	key := s.tokenKey(serverURL)
	s.tokens[key] = storedToken

	// Persist to file if file mode is enabled
	if s.fileMode {
		if err := s.writeTokenFile(key, storedToken); err != nil {
			// SECURITY AUDIT: Token storage failed
			slog.Warn("SECURITY_AUDIT: OAuth token storage failed",
				"event", "token_store_failed",
				"server_url", serverURL,
				"issuer_url", issuerURL,
				"error", err.Error(),
			)
			return fmt.Errorf("failed to persist token: %w", err)
		}
		// SECURITY AUDIT: Token successfully stored
		slog.Info("SECURITY_AUDIT: OAuth token stored",
			"event", "token_stored",
			"server_url", serverURL,
			"issuer_url", issuerURL,
			"expiry", storedToken.Expiry.Format(time.RFC3339),
			"has_refresh_token", storedToken.RefreshToken != "",
		)
	}

	return nil
}

// GetToken retrieves a stored token for a specific server.
// Returns nil if no token exists or the token has expired.
func (s *TokenStore) GetToken(serverURL string) *StoredToken {
	key := s.tokenKey(serverURL)

	// Fast path with read lock - check memory cache
	s.mu.RLock()
	if token, ok := s.tokens[key]; ok {
		if s.isTokenValid(token) {
			s.mu.RUnlock()
			return token
		}
	}
	s.mu.RUnlock()

	// Slow path with write lock for cache population/cleanup
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check in case another goroutine populated it
	if token, ok := s.tokens[key]; ok {
		if s.isTokenValid(token) {
			return token
		}
		// Token expired, remove from cache
		delete(s.tokens, key)
		return nil
	}

	// Try loading from file if file mode is enabled
	if s.fileMode {
		token, err := s.readTokenFile(key)
		if err == nil && s.isTokenValid(token) {
			s.tokens[key] = token
			return token
		}
	}

	return nil
}

// DeleteToken removes a stored token for a specific server.
// SECURITY: Logs token deletion for audit trail without logging token values.
func (s *TokenStore) DeleteToken(serverURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.tokenKey(serverURL)
	delete(s.tokens, key)

	if s.fileMode {
		if err := s.deleteTokenFile(key); err != nil {
			slog.Warn("SECURITY_AUDIT: OAuth token deletion failed",
				"event", "token_delete_failed",
				"server_url", serverURL,
				"error", err.Error(),
			)
			return err
		}
	}

	// SECURITY AUDIT: Token deleted
	slog.Info("SECURITY_AUDIT: OAuth token deleted",
		"event", "token_deleted",
		"server_url", serverURL,
	)
	return nil
}

// ToOAuth2Token converts a StoredToken to an oauth2.Token.
func (t *StoredToken) ToOAuth2Token() *oauth2.Token {
	token := &oauth2.Token{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		TokenType:    t.TokenType,
		Expiry:       t.Expiry,
	}

	// Add ID token to extra data
	if t.IDToken != "" {
		token = token.WithExtra(map[string]interface{}{
			"id_token": t.IDToken,
		})
	}

	return token
}

// tokenKey generates a unique key for a server URL.
// Uses SHA256 hash to create filesystem-safe identifiers.
func (s *TokenStore) tokenKey(serverURL string) string {
	hash := sha256.Sum256([]byte(serverURL))
	return hex.EncodeToString(hash[:16]) // Use first 16 bytes (32 hex chars)
}

// tokenExpiryBuffer is the margin added when checking token validity.
// This accounts for clock skew, network latency, and long-running operations.
const tokenExpiryBuffer = 60 * time.Second

// isTokenValid checks if a token is still valid (not expired).
// Adds a 60-second margin to account for clock skew, network latency,
// and to provide buffer for long-running operations.
func (s *TokenStore) isTokenValid(token *StoredToken) bool {
	if token == nil {
		return false
	}

	// If no expiry is set, consider the token valid
	if token.Expiry.IsZero() {
		return true
	}

	// Add margin for safety (clock skew, network latency, long operations)
	return time.Now().Add(tokenExpiryBuffer).Before(token.Expiry)
}

// writeTokenFile persists a token to a JSON file.
func (s *TokenStore) writeTokenFile(key string, token *StoredToken) error {
	filePath := filepath.Join(s.storageDir, key+".json")

	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal token: %w", err)
	}

	// Write with restricted permissions (owner read/write only)
	if err := os.WriteFile(filePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write token file: %w", err)
	}

	return nil
}

// readTokenFile reads a token from a JSON file.
func (s *TokenStore) readTokenFile(key string) (*StoredToken, error) {
	filePath := filepath.Join(s.storageDir, key+".json")

	// #nosec G304 -- filePath is constructed from internal key, not user input
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var token StoredToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("failed to unmarshal token: %w", err)
	}

	return &token, nil
}

// deleteTokenFile removes a token file.
func (s *TokenStore) deleteTokenFile(key string) error {
	filePath := filepath.Join(s.storageDir, key+".json")
	err := os.Remove(filePath)
	if os.IsNotExist(err) {
		return nil // Already deleted
	}
	return err
}

// HasValidToken checks if a valid (non-expired) token exists for a server.
func (s *TokenStore) HasValidToken(serverURL string) bool {
	return s.GetToken(serverURL) != nil
}

// GetByIssuer retrieves a stored token for a specific issuer.
// This enables SSO by allowing token lookup by issuer URL rather than server URL.
// Returns nil if no token exists for the issuer or the token has expired.
func (s *TokenStore) GetByIssuer(issuerURL string) *StoredToken {
	// Fast path with read lock - check memory cache
	s.mu.RLock()
	for _, token := range s.tokens {
		if token.IssuerURL == issuerURL && s.isTokenValid(token) {
			s.mu.RUnlock()
			return token
		}
	}
	s.mu.RUnlock()

	// Slow path: if file mode is enabled, scan files for issuer match
	// This requires a write lock because we cache the found token
	if s.fileMode {
		s.mu.Lock()
		defer s.mu.Unlock()

		// Double-check cache in case another goroutine populated it
		for _, token := range s.tokens {
			if token.IssuerURL == issuerURL && s.isTokenValid(token) {
				return token
			}
		}

		// Scan files for matching issuer
		token := s.findTokenByIssuerFromFilesLocked(issuerURL)
		if token != nil && s.isTokenValid(token) {
			return token
		}
	}

	return nil
}

// findTokenByIssuerFromFilesLocked scans token files to find one matching the issuer.
// This is a slower path used when the token is not in the memory cache.
// REQUIRES: s.mu must be held (write lock) by the caller.
func (s *TokenStore) findTokenByIssuerFromFilesLocked(issuerURL string) *StoredToken {
	entries, err := os.ReadDir(s.storageDir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		key := strings.TrimSuffix(entry.Name(), ".json")
		token, err := s.readTokenFile(key)
		if err != nil {
			continue
		}

		if token.IssuerURL == issuerURL {
			// Cache the token for future lookups (safe: we hold the write lock)
			s.tokens[key] = token
			return token
		}
	}

	return nil
}

// HasValidTokenForIssuer checks if a valid token exists for a specific issuer.
func (s *TokenStore) HasValidTokenForIssuer(issuerURL string) bool {
	return s.GetByIssuer(issuerURL) != nil
}

// Clear removes all stored tokens (both in-memory and file-based).
// SECURITY: Logs bulk token clearing for audit trail.
func (s *TokenStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokenCount := len(s.tokens)

	// Clear memory cache
	s.tokens = make(map[string]*StoredToken)

	// Clear token files if file mode is enabled
	if s.fileMode {
		entries, err := os.ReadDir(s.storageDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("failed to read token directory: %w", err)
		}

		fileCount := 0
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
				filePath := filepath.Join(s.storageDir, entry.Name())
				if err := os.Remove(filePath); err != nil {
					return fmt.Errorf("failed to remove token file %s: %w", entry.Name(), err)
				}
				fileCount++
			}
		}

		// SECURITY AUDIT: All tokens cleared
		slog.Info("SECURITY_AUDIT: All OAuth tokens cleared",
			"event", "tokens_cleared",
			"memory_tokens_cleared", tokenCount,
			"file_tokens_cleared", fileCount,
		)
	}

	return nil
}
